package server

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// dsCrashReport returns the Windows "Application Error" event (Id 1000) of a
// crashed race server: module, exception code and fault offset (image base
// 0x400000). Empty on other systems or when not found.
func dsCrashReport(pid int, since time.Time) []string {
	if runtime.GOOS != "windows" || pid <= 0 {
		return nil
	}
	time.Sleep(1500 * time.Millisecond) // Windows writes the event a moment later
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := since.Add(-time.Minute).Format("2006-01-02T15:04:05")
	script := `$p=` + strconv.Itoa(pid) + `;` +
		`Get-WinEvent -FilterHashtable @{LogName='Application';Id=1000;StartTime=[datetime]'` + start + `'} -ErrorAction SilentlyContinue |` +
		` Where-Object { $_.Properties.Count -gt 8 -and [int]("0x"+($_.Properties[8].Value -replace '^0x','')) -eq $p } |` +
		` Select-Object -First 1 | ForEach-Object { $_.Message }`
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil
	}
	return crashLines(string(out))
}

// crashLines keeps the useful lines of an Application Error message (they
// are localized; the values are what matter) and adds the code address.
func crashLines(msg string) []string {
	var out []string
	var offset string
	for _, l := range strings.Split(strings.ReplaceAll(msg, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if i := strings.LastIndex(l, ":"); i > 0 {
			v := strings.TrimSpace(l[i+1:])
			low := strings.ToLower(l)
			if strings.HasPrefix(v, "0x") && (strings.Contains(low, "offset") || strings.Contains(low, "deslocamento") || strings.Contains(low, "desplazamiento")) {
				offset = v
			}
		}
		out = append(out, l)
		if len(out) >= 8 {
			break
		}
	}
	if offset != "" {
		if v, err := strconv.ParseUint(strings.TrimPrefix(offset, "0x"), 16, 32); err == nil {
			out = append(out, "code address (image base 0x400000): 0x"+strconv.FormatUint(v+0x400000, 16))
		}
	}
	return out
}

// checkDSFirewall warns on Windows when the inbound UDP rule from
// tools\open_ds_firewall.bat is missing or does not cover the ports players
// send to (the relay range, or the race servers' ports without relay).
func (s *Server) checkDSFirewall(first, last, offset int) {
	if runtime.GOOS != "windows" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "netsh", "advfirewall", "firewall", "show", "rule", "name=NFSTR race servers (UDP)").CombinedOutput()
	rule := string(out)
	relay := s.cfg.RelayEnabled && s.cfg.RelayDSHost == ""
	rFirst, rLast := s.cfg.RelayPortBase, s.cfg.RelayPortBase+s.cfg.RelayPortCount-1
	if err == nil && strings.Contains(rule, "UDP") {
		if relay && !ruleCoversRange(rule, rFirst, rLast) {
			s.log.Printf("[DSMGR] WARNING: the Windows firewall rule \"NFSTR race servers (UDP)\" does not list the relay ports UDP %d-%d that players send to: run tools\\open_ds_firewall.bat again (and open UDP %d-%d in the router / hosting firewall)", rFirst, rLast, rFirst, rLast)
			return
		}
		s.log.Printf("[DSMGR] Windows firewall rule \"NFSTR race servers (UDP)\" found")
		return
	}
	if relay {
		s.log.Printf("[DSMGR] WARNING: no Windows firewall rule \"NFSTR race servers (UDP)\". If UDP %d-%d (relay) is not allowed by another rule, players cannot reach the race servers: run tools\\open_ds_firewall.bat on this machine and open UDP %d-%d in the router / hosting firewall", rFirst, rLast, rFirst, rLast)
		return
	}
	s.log.Printf("[DSMGR] WARNING: no Windows firewall rule \"NFSTR race servers (UDP)\". If UDP %d-%d (and %d-%d) are not allowed by another rule, players cannot reach the race servers: run tools\\open_ds_firewall.bat direct %d %d on this machine and open the same UDP ports in the router / hosting firewall",
		first, last, first+offset, last+offset, first, last-first+1)
}

// ruleCoversRange: does a "netsh advfirewall firewall show rule" listing
// contain a local port entry covering first-last ("6100-6163", or a wider
// range)? The labels are localized, so only the port list is parsed.
func ruleCoversRange(rule string, first, last int) bool {
	for _, tok := range strings.FieldsFunc(rule, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == ':' }) {
		lo, hi := tok, tok
		if i := strings.IndexByte(tok, '-'); i > 0 {
			lo, hi = tok[:i], tok[i+1:]
		}
		a, e1 := strconv.Atoi(lo)
		b, e2 := strconv.Atoi(hi)
		if e1 == nil && e2 == nil && a <= first && b >= last {
			return true
		}
	}
	return false
}

// dsUDPPorts: the UDP ports a process has open, from netstat (Windows only).
func dsUDPPorts(pid int) []int {
	if runtime.GOOS != "windows" || pid <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "UDP").Output()
	if err != nil {
		return nil
	}
	return parseNetstatUDP(string(out), pid)
}

func parseNetstatUDP(out string, pid int) []int {
	var ports []int
	seen := map[int]bool{}
	want := strconv.Itoa(pid)
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		f := strings.Fields(l)
		if len(f) < 4 || !strings.EqualFold(f[0], "UDP") || f[len(f)-1] != want {
			continue
		}
		i := strings.LastIndex(f[1], ":")
		if i < 0 {
			continue
		}
		if p, err := strconv.Atoi(f[1][i+1:]); err == nil && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports
}

// logDSPorts logs a race server's UDP ports and warns when players are sent
// to a port it does not have open.
func (s *Server) logDSPorts(n, pid int) {
	ports := dsUDPPorts(pid)
	if ports == nil {
		return
	}
	s.log.Printf("[DSMGR] server %d (pid %d) has UDP ports open: %v", n, pid, ports)
	mp := s.cfg.DSMeshPort
	if mp == 0 {
		// The race server opens its game socket (-GamePort) only when the first
		// player joins, so it is normally not listed yet. 6000 is VoIP; 9999 appears
		// during a race.
		return
	}
	if mp == 6000 {
		s.log.Printf("[DSMGR] WARNING: DS_MESH_PORT=6000 is the race server's VoIP socket, not its game port. Remove DS_MESH_PORT from server.env (0 = the race server's -gamePort).")
	}
	for _, p := range ports {
		if p == mp {
			return
		}
	}
	s.log.Printf("[DSMGR] WARNING: server %d does not have DS_MESH_PORT %d open; players will not reach it. Set DS_MESH_PORT in server.env to one of %v", n, mp, ports)
}
