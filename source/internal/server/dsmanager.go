package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/dspatch"
	"nfsrun-server/internal/playlistpatch"
	"nfsrun-server/internal/protocert"
)

// Dedicated race server supervisor.
//
// With DS_ENABLED=true the server builds NFSTR_DedicatedServer.exe from the
// game exe (internal/dspatch) and runs up to DS_COUNT copies:
//
//	NFSTR_DedicatedServer.exe -listen <DS_LISTEN_IP>:<port>-<port>
//	    -serverInstancePath <DS_INSTANCE_DIR>\instance<N> -instanceName "NFSTR DS N"
//
// port = DS_PORT_BASE + N-1. Each copy logs into Blaze (shared server account,
// persona NFS-DS-NN), creates its game and waits for players.

type dsInstance struct {
	n         int
	port      int
	pid       int
	started   time.Time
	restarts  int
	state     string    // stopped | starting | running
	keep      bool      // one of the DS_IDLE servers: restarted when it exits
	cmd       *exec.Cmd // while running
	notBefore time.Time // no new start before (after a quick failure)
	ready     bool      // its game reached RESETABLE in this run
	idleSince time.Time // running without players since
	why       string    // what it was started for
}

type dsManager struct {
	mu    sync.Mutex
	insts []*dsInstance
	names map[*connState]string
	pub   uint64
	pubAt time.Time
	// prepared once at startup; race servers then start on demand
	ready                  bool
	ctx                    context.Context
	exe, gameDir, instRoot string
}

// dsPingSite: QoS ping site of the race servers, shown in their games.
var dsPingSite = "sjc"

func dsPersonaName(n int) string { return fmt.Sprintf("NFS-DS-%02d", n) }

func isDSPersonaName(name string) bool {
	l := strings.ToLower(name)
	return strings.HasPrefix(l, "nfs-ds-") || isDedicatedServerAccount(l+"@ea.com")
}

// allocName gives each server connection its own persona (all copies share one account).
func (m *dsManager) allocName(st *connState) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.names == nil {
		m.names = map[*connState]string{}
	}
	if n, ok := m.names[st]; ok {
		return n
	}
	used := map[string]bool{}
	for _, n := range m.names {
		used[n] = true
	}
	for i := 1; ; i++ {
		if n := dsPersonaName(i); !used[n] {
			m.names[st] = n
			return n
		}
	}
}

func (m *dsManager) release(st *connState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.names, st)
}

// dsPublicIP is the address players use to reach the race servers.
func (s *Server) dsPublicIP() uint64 {
	s.ds.mu.Lock()
	if s.ds.pub != 0 && time.Since(s.ds.pubAt) < 10*time.Minute {
		v := s.ds.pub
		s.ds.mu.Unlock()
		return v
	}
	s.ds.mu.Unlock()
	v := s.lookupDSPublicIP()
	s.ds.mu.Lock()
	s.ds.pub, s.ds.pubAt = v, time.Now()
	s.ds.mu.Unlock()
	return v
}

func (s *Server) lookupDSPublicIP() uint64 {
	if s.cfg.DSPublicIP != "" {
		return ipUint(s.cfg.DSPublicIP)
	}
	if ip := net.ParseIP(s.cfg.PublicIP); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		return ipUint(s.cfg.PublicIP)
	}
	if s.cfg.PublicHost != "" {
		if addrs, err := net.LookupIP(s.cfg.PublicHost); err == nil {
			cf, _ := parseTrustedProxies("cloudflare")
			for _, a := range addrs {
				v4 := a.To4()
				if v4 == nil || v4.IsLoopback() {
					continue
				}
				// A proxied (orange-cloud) name resolves to Cloudflare, which does
				// not carry the race traffic.
				if ipIn(v4, cf) {
					s.log.Printf("[DSMGR] %s resolves to %s (Cloudflare): not usable for race servers; set PUBLIC_IP or DS_PUBLIC_IP", s.cfg.PublicHost, v4)
					continue
				}
				return ipUint(v4.String())
			}
		}
	}
	return 0
}

// publicIPConfigured: PUBLIC_IP or DS_PUBLIC_IP name a usable address.
func (s *Server) publicIPConfigured() bool {
	if s.cfg.DSPublicIP != "" {
		return true
	}
	ip := net.ParseIP(s.cfg.PublicIP)
	return ip != nil && !ip.IsLoopback() && !ip.IsUnspecified()
}

// isPublicIPv4: a routable IPv4 address (not private, loopback, link-local or CGNAT).
func isPublicIPv4(v uint64) bool {
	if v == 0 {
		return false
	}
	ip := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return !cgnat.Contains(ip)
}

func (s *Server) startDedicatedServers(ctx context.Context) {
	c := s.cfg
	if !c.DSEnabled || c.DSCount <= 0 {
		s.log.Printf("[DSMGR] off (DS_ENABLED=false): start race servers yourself or set DS_ENABLED=true in config\\server.env")
		return
	}
	gameDir, _ := filepath.Abs(c.DSGameDir)
	if c.DSGameDir == "" {
		s.log.Printf("[DSMGR] DS_GAME_DIR is empty: set it to the game folder in config\\server.env")
		return
	}
	// DS_GAME_DIR is the folder, but accept the exe path too (common mistake).
	if strings.EqualFold(filepath.Ext(gameDir), ".exe") {
		s.log.Printf("[DSMGR] DS_GAME_DIR points at a file (%s); using its folder", gameDir)
		gameDir = filepath.Dir(gameDir)
	}
	src := filepath.Join(gameDir, c.DSSourceExe)
	exe := filepath.Join(gameDir, c.DSExe)
	// a retail exe gets the Redirector hosts of BASE_DOMAIN; a patched one keeps its own
	domain := ""
	if _, _, err := protocert.ExeHosts(src); errors.Is(err, protocert.ErrRetailExe) && !c.DSKeepHosts {
		domain = s.cfg.BaseDomain
	}
	notes, err := dspatch.Ensure(src, exe, domain)
	for _, n := range notes {
		s.log.Printf("[DSMGR] %s", n)
	}
	if err != nil {
		s.log.Printf("[DSMGR] cannot prepare %s: %v", exe, err)
		return
	}
	// the extra playlists in the race servers' PlaylistSet (DS_EXTRA_PLAYLISTS)
	if r, err := playlistpatch.Ensure(gameDir, c.DSExtraPlaylists); err != nil {
		s.log.Printf("[DSMGR] playlists: %v (race servers keep the original 6; a player who picks an extra playlist gets no race server)", err)
	} else {
		s.log.Printf("[DSMGR] playlists: %s", r.Note)
	}
	instRoot, _ := filepath.Abs(c.DSInstanceDir)
	if c.DSKillStale && runtime.GOOS == "windows" {
		out, _ := exec.Command("taskkill", "/F", "/IM", filepath.Base(exe)).CombinedOutput()
		if t := strings.TrimSpace(string(out)); t != "" && !strings.Contains(strings.ToLower(t), "not found") {
			s.log.Printf("[DSMGR] stale servers: %s", t)
		}
	}
	pub := s.dsPublicIP()
	idle := c.DSIdle
	if idle > c.DSCount {
		idle = c.DSCount
	}
	s.log.Printf("[DSMGR] up to %d race server(s) from %s, race-server UDP ports %d-%d (-GamePort) and %d-%d (Frostbite -listen), public address %s; %d kept running, the others start when a player starts Quick Match and stop after %d s without players (DS_IDLE, DS_IDLE_EXIT)",
		c.DSCount, exe, c.DSPortBase, c.DSPortBase+c.DSCount-1, c.DSPortBase+c.DSListenOffset, c.DSPortBase+c.DSListenOffset+c.DSCount-1, ipString(pub), idle, c.DSIdleExitSec)
	if kept, dropped := stripTunnelArgs(strings.Fields(c.DSExtraArgs)); len(dropped) > 0 {
		s.log.Printf("[DSMGR] ignoring %q in DS_EXTRA_ARGS: players and race servers must use the same ProtoTunnel setting (the game's default)", strings.Join(dropped, " "))
		s.cfg.DSExtraArgs = strings.Join(kept, " ")
	}
	if c.RelayEnabled && c.RelayDSHost == "" {
		// With the relay, players only send to the relay ports; the race servers'
		// own ports stay on loopback.
		s.log.Printf("[DSMGR] players reach the race servers through the relay: open UDP %d-%d (nfsrun-server.exe) on the firewall and the router / hosting firewall; the race servers' own UDP ports (%d.., 9999, and the random one they open) stay on 127.0.0.1 and need no rule",
			c.RelayPortBase, c.RelayPortBase+c.RelayPortCount-1, c.DSPortBase)
	}
	go s.checkDSFirewall(c.DSPortBase, c.DSPortBase+c.DSCount-1, c.DSListenOffset)
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second): // Blaze listeners first
	}
	s.ds.mu.Lock()
	s.ds.ctx, s.ds.exe, s.ds.gameDir, s.ds.instRoot = ctx, exe, gameDir, instRoot
	for i := 1; i <= c.DSCount; i++ {
		s.ds.insts = append(s.ds.insts, &dsInstance{n: i, port: c.DSPortBase + i - 1, state: "stopped", keep: i <= idle})
	}
	s.ds.ready = true
	s.ds.mu.Unlock()
	for i := 1; i <= idle; i++ {
		s.ds.mu.Lock()
		inst := s.ds.insts[i-1]
		s.startInstanceLocked(inst, "kept running (DS_IDLE)")
		s.ds.mu.Unlock()
		time.Sleep(2 * time.Second) // stagger the level loading
	}
	go s.dsJanitor(ctx)
}

// startInstanceLocked starts one race server process (s.ds.mu held).
func (s *Server) startInstanceLocked(inst *dsInstance, why string) {
	inst.state, inst.why, inst.idleSince = "starting", why, time.Time{}
	dir := filepath.Join(s.ds.instRoot, fmt.Sprintf("instance%d", inst.n))
	_ = os.MkdirAll(dir, 0755)
	go s.runDSInstance(s.ds.ctx, inst, s.ds.exe, s.ds.gameDir, dir)
}

// ensureRaceServer starts a race server for a waiting Quick Match unless one
// is already starting or all DS_COUNT are busy.
func (s *Server) ensureRaceServer(why string) {
	s.gm.mu.Lock()
	withGame := map[int]bool{} // instance ports whose race server has a game in Blaze
	for _, g := range s.gm.games {
		if g.dedicated() {
			withGame[int(g.ds.Net.IntPort)] = true
		}
	}
	s.gm.mu.Unlock()
	s.ds.mu.Lock()
	defer s.ds.mu.Unlock()
	if !s.ds.ready {
		return
	}
	for _, i := range s.ds.insts {
		if i.state == "starting" || (i.state == "running" && !withGame[i.port]) {
			return // on its way
		}
	}
	for _, i := range s.ds.insts {
		if i.state != "stopped" {
			continue
		}
		if wait := time.Until(i.notBefore); wait > 0 {
			s.log.Printf("[DSMGR] server %d failed to start just before; next try in %s", i.n, wait.Round(time.Second))
			// offer the waiting sessions again when the pause ends
			s.offerWaitingSessionsAfter(wait + 100*time.Millisecond)
			return
		}
		s.log.Printf("[DSMGR] starting race server %d for %s", i.n, why)
		s.startInstanceLocked(i, why)
		return
	}
	s.log.Printf("[DSMGR] all %d race server(s) are busy (DS_COUNT); %s waits for one to free up", len(s.ds.insts), why)
}

// dsJanitor stops on-demand race servers that had no players for
// DS_IDLE_EXIT seconds while no Quick Match is waiting.
func (s *Server) dsJanitor(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.gm.mu.Lock()
		busy := map[int]bool{}
		waiting := 0
		for _, o := range s.gm.pool {
			if !o.done {
				waiting++
			}
		}
		for _, g := range s.gm.games {
			if g.dedicated() && (len(g.players) > 0 || g.gstate != gameStateResetable) {
				busy[int(g.ds.Net.IntPort)] = true
			}
		}
		s.gm.mu.Unlock()
		s.ds.mu.Lock()
		for _, i := range s.ds.insts {
			if i.keep || i.state != "running" || i.cmd == nil {
				continue
			}
			if busy[i.port] || waiting > 0 || time.Since(i.started) < 30*time.Second {
				i.idleSince = time.Time{}
				continue
			}
			if i.idleSince.IsZero() {
				i.idleSince = time.Now()
				continue
			}
			if time.Since(i.idleSince) >= time.Duration(s.cfg.DSIdleExitSec)*time.Second {
				s.log.Printf("[DSMGR] stopping race server %d: no players for %d s (DS_IDLE_EXIT)", i.n, s.cfg.DSIdleExitSec)
				i.state = "stopping"
				_ = killRaceServer(i.cmd)
			}
		}
		s.ds.mu.Unlock()
	}
}

// hasArg reports whether args has the engine setting name ("-Section.Field").
func hasArg(args []string, name string) bool {
	for _, a := range args {
		if strings.EqualFold(strings.TrimLeft(a, "-+"), name) {
			return true
		}
	}
	return false
}

// stripTunnelArgs removes "-NfsOnline.EnableProtoTunnel <value>": players and
// race servers must agree on ProtoTunnel, so both keep the game's default.
func stripTunnelArgs(args []string) (kept, dropped []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.EqualFold(strings.TrimLeft(a, "-+"), "NfsOnline.EnableProtoTunnel") {
			dropped = append(dropped, a)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				dropped = append(dropped, args[i])
			}
			continue
		}
		kept = append(kept, a)
	}
	return kept, dropped
}

func (s *Server) runDSInstance(ctx context.Context, inst *dsInstance, exe, gameDir, dir string) {
	c := s.cfg
	backoff := time.Duration(c.DSRestartSec) * time.Second
	for ctx.Err() == nil {
		name := fmt.Sprintf("NFSTR_DS_%d_port%d", inst.n, inst.port)
		// Server arguments. The engine's tokenizer splits on spaces and ignores
		// quotes, so no value may contain a space.
		args := []string{
			"-Server.ServerName", name,
			// -GamePort is the Blaze mesh (ConnApi) peer port reported in HNET. The
			// Frostbite -listen socket must use another port.
			"-GamePort", strconv.Itoa(inst.port),
			"-listen", fmt.Sprintf("%s:%d-%d", c.DSListenIP, inst.port+c.DSListenOffset, inst.port+c.DSListenOffset),
			"-serverInstancePath", dir,
			"-instanceName", name,
		}
		// the online career layer and the Blaze backend; without
		// Server.IsPresenceEnabled a server always runs Backend_Lan (0x013CEB50)
		// and never logs into Blaze
		args = append(args, "-Game.DefaultLayerInclusion", "StartupMode=Game;GameMode=CareerMode",
			"-NfsOnline.Backend", "Backend_Blaze", "-Server.IsPresenceEnabled", "true")
		// AllowSinglePlayerOnline keeps a playlist running with one player; without
		// it the race server ends the session at the first intermission.
		if c.DSAllowSolo && !hasArg(strings.Fields(c.DSExtraArgs), "NfsOnline.AllowSinglePlayerOnline") {
			args = append(args, "-NfsOnline.AllowSinglePlayerOnline", "true")
		}
		args = append(args, strings.Fields(c.DSExtraArgs)...)
		if strings.ContainsAny(dir, " \t") {
			s.log.Printf("[DSMGR] WARNING: the instance folder %q has a space; the engine cannot take it as -serverInstancePath (set DS_INSTANCE_DIR to a path without spaces)", dir)
		}
		var cmd *exec.Cmd
		if c.DSLauncher != "" { // e.g. a Proton wrapper: DS_LAUNCHER <exe> <arguments...>
			cmd = exec.CommandContext(ctx, c.DSLauncher, append([]string{exe}, args...)...)
		} else {
			cmd = exec.CommandContext(ctx, exe, args...)
		}
		cmd.Dir = gameDir
		if err := s.writeRaceIni(gameDir); err != nil {
			s.log.Printf("[DSMGR] race settings not written: %v", err)
		}
		setProcGroup(cmd)
		// The engine writes init errors to stdout; keep them in console.log.
		conPath := filepath.Join(dir, "console.log")
		con, _ := os.Create(conPath)
		if con != nil {
			cmd.Stdout, cmd.Stderr = con, con
		}
		start := time.Now()
		if err := cmd.Start(); err != nil {
			s.log.Printf("[DSMGR] server %d: start failed: %v", inst.n, err)
		} else {
			s.ds.mu.Lock()
			inst.pid, inst.started, inst.state, inst.cmd = cmd.Process.Pid, start, "running", cmd
			inst.ready = false
			s.ds.mu.Unlock()
			s.log.Printf("[DSMGR] server %d started (%s): pid %d, UDP %d, args %q", inst.n, inst.why, cmd.Process.Pid, inst.port, args)
			go func(n, pid int) {
				time.Sleep(15 * time.Second)
				s.logDSPorts(n, pid)
			}(inst.n, cmd.Process.Pid)
			err := cmd.Wait()
			up := time.Since(start).Round(time.Second)
			s.ds.mu.Lock()
			stopping := inst.state == "stopping"
			inst.state, inst.pid, inst.cmd = "stopped", 0, nil
			inst.restarts++
			s.ds.mu.Unlock()
			if ctx.Err() != nil {
				s.log.Printf("[DSMGR] server %d stopped (emulator shutting down)", inst.n)
				return
			}
			if stopping {
				s.log.Printf("[DSMGR] server %d stopped after %s", inst.n, up)
			} else {
				s.log.Printf("[DSMGR] server %d exited after %s (%v)", inst.n, up, err)
				keepRunLogs(gameDir, dir)
			}
			if err != nil && strings.Contains(err.Error(), "0xc0") {
				for _, l := range dsCrashReport(cmd.Process.Pid, start) {
					s.log.Printf("[DSMGR] server %d crash: %s", inst.n, l)
				}
			}
			if con != nil {
				con.Close()
				con = nil
				for _, l := range tailLines(conPath, 12) {
					s.log.Printf("[DSMGR] server %d console: %s", inst.n, l)
				}
			}
			if up > 2*time.Minute {
				backoff = time.Duration(c.DSRestartSec) * time.Second
			}
		}
		if con != nil {
			con.Close()
		}
		if !inst.keep {
			// on demand: the race server exits with its game; the next Quick Match
			// starts it again
			s.ds.mu.Lock()
			inst.state = "stopped"
			// only a server that never got its game ready counts as a failed start
			if time.Since(start) < 20*time.Second && !inst.ready {
				inst.notBefore = time.Now().Add(backoff)
			}
			s.ds.mu.Unlock()
			s.offerWaitingSessionsAfter(time.Second)
			return
		}
		s.log.Printf("[DSMGR] server %d: restart in %s", inst.n, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 5*time.Minute {
			backoff *= 2
		}
	}
}

// dsStatus for /health.
func (s *Server) dsStatus() []map[string]any {
	s.ds.mu.Lock()
	defer s.ds.mu.Unlock()
	var out []map[string]any
	for _, i := range s.ds.insts {
		out = append(out, map[string]any{"n": i.n, "port": i.port, "pid": i.pid, "state": i.state, "restarts": i.restarts})
	}
	return out
}

// dsWaitHint says why no race server took a matchmaking session.
func (s *Server) dsWaitHint() string {
	if !s.cfg.DSEnabled || s.cfg.DSCount <= 0 {
		return "DS_ENABLED=false and no race server logged in; start NFSTR_DedicatedServer.exe"
	}
	s.ds.mu.Lock()
	defer s.ds.mu.Unlock()
	running := 0
	for _, i := range s.ds.insts {
		if i.state == "running" {
			running++
		}
	}
	if running == 0 {
		return "no race server running yet: one is started now"
	}
	return "race servers running but none logged into Blaze yet or all busy"
}

// tailLines returns the last n non-empty lines of a small text file.
func tailLines(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// dsMarkReady: the race server on this -GamePort got its game to RESETABLE.
func (s *Server) dsMarkReady(port int) {
	s.ds.mu.Lock()
	defer s.ds.mu.Unlock()
	for _, i := range s.ds.insts {
		if i.port == port {
			i.ready = true
		}
	}
}

// stopRaceServer ends race server n by hand (admin). Its game ends with it; players go back to the menu.
func (s *Server) stopRaceServer(n int) string {
	s.ds.mu.Lock()
	defer s.ds.mu.Unlock()
	for _, i := range s.ds.insts {
		if i.n != n {
			continue
		}
		if i.cmd == nil || (i.state != "running" && i.state != "starting") {
			return fmt.Sprintf("race server %d is not running", n)
		}
		s.log.Printf("[DSMGR] stopping race server %d (asked by the server owner)", n)
		i.state = "stopping"
		_ = killRaceServer(i.cmd)
		return fmt.Sprintf("race server %d stopped", n)
	}
	return fmt.Sprintf("there is no race server %d", n)
}

// keepRunLogs copies the plugin logs of the race server that just ended into its instance folder
// (the next start overwrites them): last_plugin.log and last_netfix.log.
func keepRunLogs(gameDir, instDir string) {
	for from, to := range map[string]string{"NFSTR_Online_server.log": "last_plugin.log", "NFSTR_NetFix.log": "last_netfix.log"} {
		if b, err := os.ReadFile(filepath.Join(gameDir, "Scripts", from)); err == nil {
			_ = os.WriteFile(filepath.Join(instDir, to), b, 0o644)
		}
	}
}
