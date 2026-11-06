package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/config"
	"nfsrun-server/internal/database"
)

type connState struct {
	Email         string
	Persona       database.Persona
	Authenticated bool
	// UserAnnounced is set once UserSessions UserAdded/UserUpdated were pushed.
	UserAnnounced bool
	// Pending holds server-initiated packets (notifications) to send after the
	// current response.
	Pending []*blaze.Packet

	conn   net.Conn
	kind   string
	wmu    *sync.Mutex
	closed bool
	mm     *matchmakingSession
	Net    netInfo

	peer         string // real client address (through the public front)
	peerResolved bool
	identified   bool
	game         *game
	dsGames      []*game // games this connection hosts as a dedicated server
}

func (st *connState) close() {
	if st.wmu == nil {
		return
	}
	st.wmu.Lock()
	st.closed = true
	if st.mm != nil && st.mm.timer != nil {
		st.mm.timer.Stop()
	}
	st.wmu.Unlock()
}

type Server struct {
	relays relayManager // UDP relays to race servers
	cfg    config.Config
	db     Store
	log    *log.Logger
	wg     sync.WaitGroup

	guard          *guard
	gm             *gameManager
	online         onlineSet
	ds             dsManager
	web            *webStore
	recent         recentPlayers
	pg             playgroups
	invites        inviteBox
	hub            eventHub
	launchers      launcherRegistry
	authChallenges launcherChallengeRegistry
	gameCodes      gameCodeRegistry
	pwLimit        pwLimiter
	bans           banList
	race           raceStore
	adminWeb       adminWeb
	proxies        []*net.IPNet // TRUSTED_PROXIES

	connMu  sync.Mutex
	conns   map[net.Conn]bool
	done    chan struct{} // closed on shutdown
	started time.Time
}

// Store is the persistence the server needs. *database.DB implements it; tests
// use an in-memory fake so the full RPC flow can be exercised without MariaDB.
type Store interface {
	Ping() error
	Touch()
	ValidateSession(key string) bool
	GetOrCreateIdentity(email, name string) (database.Persona, error)
	Migrate() error
	SaveGameReport(personaID uint64, gameType, payloadSHA256, payloadHex, resultsJSON string) (bool, error)
	UpsertPersonalBest(personaID uint64, board string, eventID, timeMs uint64, metadataJSON string) (bool, error)
	LoadBoardEntries(boards []string, events []uint64) ([]database.BoardEntry, error)
	LoadBoard(board string) ([]database.BoardEntry, error)
	ListPersonas(excludeID uint64, limit int) ([]database.PersonaRef, error)
	ListFriends(personaID uint64) ([]database.PersonaRef, error)
	LinkAllFriends() error // FRIENDS_ALL
	PersonaBySessionKey(key string) (database.Persona, bool, error)
	CreateLockerContent(personaID uint64, category, name, desc, attrsJSON, subNames string, perm uint64) (database.LockerContent, error)
	GetLockerContent(id uint64) (database.LockerContent, bool, error)
	SetLockerStatus(id, status, size uint64) error
	UpdateLockerContent(id, ownerID uint64, name, desc, attrsJSON string) error
	DeleteLockerContents(ownerID uint64, ids []uint64) error
	ListLockerContents(category string, ownerID uint64, includePrivate bool, offset, limit int) ([]database.LockerContent, uint64, error)
	SpeedwallOwnedCounts() ([]database.OwnedCount, error)
}

func New(c config.Config, d Store, l *log.Logger) *Server {
	proxies, err := parseTrustedProxies(c.TrustedProxies)
	if err != nil {
		l.Printf("[CONFIG] %v; no proxy is trusted", err)
	}
	return &Server{cfg: c, db: d, log: l, done: make(chan struct{}), started: time.Now(), guard: newGuard(c.ClientGuard, c.GuardAllowIPs, c.GuardMaxConnPerIP), gm: newGameManager(),
		web: &webStore{dir: filepath.Join(filepath.Dir(c.LockerDir), "web")}, proxies: proxies}
}

// listener: the backends are plaintext (the SSLv3 gateway decrypts in front).
func (s *Server) listener(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
func (s *Server) frontProxyLoop(l net.Listener, kind, target string) {
	defer s.wg.Done()
	for {
		c, e := l.Accept()
		if e != nil {
			return
		}
		s.wg.Add(1)
		s.trackConn(c, true)
		go func(client net.Conn) {
			defer s.wg.Done()
			defer s.trackConn(client, false)
			defer client.Close()
			ok, why, release := s.guard.admit(client.RemoteAddr().String())
			if !ok {
				if s.guard.shouldLog(kind + hostOf(client.RemoteAddr().String())) {
					s.log.Printf("[GUARD] %s %s refused: %s", kind, client.RemoteAddr(), why)
				}
				return
			}
			defer release()
			var head []byte
			if s.cfg.ClientGuard {
				var good bool
				head, good, why = sniffFront(client, kind)
				if !good {
					if s.guard.shouldLog(kind + "sniff" + hostOf(client.RemoteAddr().String())) {
						s.log.Printf("[GUARD] %s %s refused: %s (first bytes % X)", kind, client.RemoteAddr(), why, head)
					}
					return
				}
			}
			s.log.Printf("[FRONT-%s] connect %s -> %s", kind, client.RemoteAddr(), target)
			backend, e := net.DialTimeout("tcp", target, 5*time.Second)
			if e != nil {
				s.log.Printf("[FRONT-%s] %s gateway unavailable: %v", kind, client.RemoteAddr(), e)
				return
			}
			defer backend.Close()
			// Let the backend know who is really behind this local connection.
			s.guard.realAddr.Store(backend.LocalAddr().String(), client.RemoteAddr().String())
			defer s.guard.realAddr.Delete(backend.LocalAddr().String())
			if len(head) > 0 {
				if _, e := backend.Write(head); e != nil {
					return
				}
			}
			s.log.Printf("[FRONT-%s] %s tunnel established", kind, client.RemoteAddr())

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(backend, client)
				if tc, ok := backend.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
			}()
			go func() {
				defer wg.Done()
				_, _ = io.Copy(client, backend)
				if tc, ok := client.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
			}()
			wg.Wait()
			s.log.Printf("[FRONT-%s] %s tunnel closed", kind, client.RemoteAddr())
		}(c)
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if e := s.db.Ping(); e != nil {
		return fmt.Errorf("database ping failed: %w", e)
	}
	if e := s.db.Migrate(); e != nil {
		return fmt.Errorf("database migration failed: %w", e)
	}
	s.db.Touch()
	if s.cfg.FriendsAll {
		// FRIENDS_ALL links everyone in the friends table
		if e := s.db.LinkAllFriends(); e != nil {
			s.log.Printf("[FRIENDS] linking all players as friends failed: %v", e)
		} else {
			s.log.Printf("[FRIENDS] FRIENDS_ALL: every player is a friend of every other player (table friends)")
		}
	}
	red, e := s.listener(s.cfg.RedirectorListen)
	if e != nil {
		return fmt.Errorf("redirector backend listen: %w", e)
	}
	bl, e := s.listener(s.cfg.BlazeListen)
	if e != nil {
		red.Close()
		return fmt.Errorf("blaze backend listen: %w", e)
	}
	frontRed, e := net.Listen("tcp", s.cfg.RedirectorFrontListen)
	if e != nil {
		red.Close()
		bl.Close()
		return fmt.Errorf("redirector public front listen: %w", e)
	}
	frontBlaze, e := net.Listen("tcp", s.cfg.BlazeFrontListen)
	if e != nil {
		red.Close()
		bl.Close()
		frontRed.Close()
		return fmt.Errorf("blaze public front listen: %w", e)
	}
	s.log.Printf("Redirector backend listening on %s", s.cfg.RedirectorListen)
	s.log.Printf("Blaze backend listening on %s", s.cfg.BlazeListen)
	s.log.Printf("Redirector PUBLIC front listening on %s -> legacy SSL gateway %s", s.cfg.RedirectorFrontListen, s.cfg.RedirectorGatewayTarget)
	s.log.Printf("Blaze PUBLIC front listening on %s -> plaintext backend %s", s.cfg.BlazeFrontListen, s.cfg.BlazeListen)
	go s.startDedicatedServers(ctx)
	s.wg.Add(4)
	go s.acceptLoop(red, true)
	go s.acceptLoop(bl, false)
	go s.frontProxyLoop(frontRed, "REDIRECTOR", s.cfg.RedirectorGatewayTarget)
	go s.frontProxyLoop(frontBlaze, "BLAZE", s.cfg.BlazeListen)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "service": "nfsrun-autolog", "dedicatedServers": s.dsStatus(), "games": s.gameSummary()})
	})
	// Web Autolog: profile, friends, News (web.go, docs/AUTOLOG.md).
	mux.HandleFunc("/Persona/", s.handleWebHTTP)
	mux.HandleFunc("/Autolog/", s.handleWebHTTP)
	mux.HandleFunc("/locker/", s.handleLockerHTTP)
	mux.HandleFunc("/nfsrun/players", s.handlePlayersHTTP)
	mux.HandleFunc("/nfsrun/auth/challenge", s.handleLauncherAuthChallenge)
	mux.HandleFunc("/nfsrun/auth/session", s.handleLauncherAuthSession)
	mux.HandleFunc("/nfsrun/auth/gamecode", s.handleGameCodeHTTP)
	mux.HandleFunc("/nfsrun/auth/password", s.handlePasswordLoginHTTP)
	mux.HandleFunc("/nfsrun/presence", s.handlePresenceHTTP)
	mux.HandleFunc("/nfsrun/results", s.handleResultsHTTP)
	mux.HandleFunc("/nfsrun/invite", s.handleInviteHTTP)
	mux.HandleFunc("/nfsrun/stream", s.handleStreamHTTP)
	mux.HandleFunc("/news/", func(w http.ResponseWriter, r *http.Request) {
		if !s.serveNewsImage(w, r) {
			s.httpNotFound(w, r)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !s.serveNewsImage(w, r) { // "/" = default banner for items without NewsIcon
			s.httpNotFound(w, r)
		}
	})
	plain := http.Handler(mux)
	if s.cfg.LauncherHTTPSListen == "" {
		s.log.Printf("[HTTPS] WARNING: LAUNCHER_HTTPS_LISTEN=off, the launcher API is on plain HTTP %s (login tickets unencrypted)", s.cfg.HTTPListen)
	} else {
		plain = noPlainLauncher(mux)
	}
	httpsSrv, e := s.startLauncherHTTPS(s.httpGuard(s.logHTTP(launcherOnly(mux))))
	if e != nil {
		s.log.Printf("[HTTPS] %v -- the launcher API is not available over HTTPS", e)
	}
	httpSrv := &http.Server{Addr: s.cfg.HTTPListen, Handler: s.httpGuard(s.logHTTP(plain)), ReadHeaderTimeout: 10 * time.Second}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.log.Printf("AutoLog HTTP scaffold listening on %s", s.cfg.HTTPListen)
		if e := httpSrv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			s.log.Printf("HTTP error: %v", e)
		}
	}()
	s.startAdminWeb(ctx)
	<-ctx.Done()
	// Close every connection and live channel so Shutdown/Wait return.
	s.log.Printf("shutting down: closing listeners and %d connection(s)", s.connCount())
	close(s.done)
	red.Close()
	bl.Close()
	frontRed.Close()
	frontBlaze.Close()

	s.closeAllConns()
	sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = httpSrv.Shutdown(sctx)
	if httpsSrv != nil {
		_ = httpsSrv.Shutdown(sctx)
		_ = httpsSrv.Close()
	}
	scancel()
	_ = httpSrv.Close()
	waited := make(chan struct{})
	go func() { s.wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		s.log.Printf("shutdown: some connections did not close in 5 s, exiting anyway")
	}
	s.log.Printf("server stopped")
	return nil
}

func (s *Server) trackConn(c net.Conn, add bool) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conns == nil {
		s.conns = map[net.Conn]bool{}
	}
	if add {
		s.conns[c] = true
	} else {
		delete(s.conns, c)
	}
}

func (s *Server) connCount() int {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return len(s.conns)
}

func (s *Server) closeAllConns() {
	s.connMu.Lock()
	list := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		list = append(list, c)
	}
	s.connMu.Unlock()
	for _, c := range list {
		_ = c.Close()
	}
}
func (s *Server) acceptLoop(l net.Listener, redirect bool) {
	defer s.wg.Done()
	for {
		c, e := l.Accept()
		if e != nil {
			return
		}
		s.wg.Add(1)
		s.trackConn(c, true)
		go func() { defer s.wg.Done(); defer s.trackConn(c, false); defer c.Close(); s.handleConn(c, redirect) }()
	}
}
func (s *Server) handleConn(c net.Conn, redirect bool) {
	kind := "BLAZE"
	if redirect {
		kind = "REDIRECTOR"
	}
	s.log.Printf("[%s] connect %s", kind, c.RemoteAddr())
	state := &connState{conn: c, kind: kind, wmu: &sync.Mutex{}, peer: c.RemoteAddr().String()}
	defer state.close()
	defer s.hub.kick() // runs last: the launchers see the player go offline
	defer s.online.drop(state)
	defer s.announceOffline(state) // runs before drop (LIFO)
	defer s.ds.release(state)
	defer s.gm.leaveAll(s, state)
	defer s.leavePlaygroup(state, pgReasonLeft)
	for {
		_ = c.SetReadDeadline(time.Now().Add(120 * time.Second))
		p, e := blaze.ReadPacket(c)
		if e != nil {
			if e != io.EOF {
				s.log.Printf("[%s] %s closed: %v", kind, c.RemoteAddr(), e)
			}
			return
		}
		if !state.peerResolved { // the front proxy has registered us by the time data arrives
			state.peer, state.peerResolved = s.clientAddr(c), true
			if state.peer != c.RemoteAddr().String() {
				s.log.Printf("[%s] %s is client %s", kind, c.RemoteAddr(), state.peer)
			}
		}
		s.log.Printf("[%s] RX id=%d comp=0x%04X cmd=0x%04X type=0x%04X len=%d (%s)", kind, p.ID, p.Component, p.Command, p.QType, len(p.Payload), blaze.CommandName(p.Component, p.Command))
		fields, e := blaze.DecodeAll(p.Payload)
		if e != nil {
			s.log.Printf("[%s] TDF decode warning: %v raw=%X", kind, e, p.Payload)
		} else if s.cfg.LogTDF {
			s.log.Printf("[%s] TDF:\n%s", kind, blaze.FormatFieldsSafe(fields, "  "))
		}
		if s.cfg.ClientGuard {
			if !state.identified {
				if ok, why := identityOK(p, fields); !ok {
					s.log.Printf("[GUARD] %s %s dropped: %s", kind, state.peer, why)
					s.send(state, s.errorResp(p, 1), false)
					return
				}
				state.identified = true
			}
			if !redirect && state.Persona.ID == 0 && !allowedBeforeLogin(p) {
				s.log.Printf("[GUARD] %s %s dropped: %s before login", kind, state.peer, blaze.CommandName(p.Component, p.Command))
				s.send(state, s.errorResp(p, 1), false)
				return
			}
		}
		resp := s.dispatch(p, fields, redirect, state)
		if resp == nil {
			continue
		}
		// The response and the notifications queued by this handler go out as one
		// batch, so a timer notification cannot get between them.
		if !s.sendResponseAndPending(state, resp) {
			return
		}
	}
}

// send writes one packet under the connection's write lock (handlers and
// timers such as matchmaking can both write) and logs it.
func (s *Server) send(state *connState, p *blaze.Packet, notify bool) bool {
	state.wmu.Lock()
	defer state.wmu.Unlock()
	if state.closed {
		return false
	}
	return s.writePacketLocked(state, p, notify)
}

// sendResponseAndPending keeps a handler's response and its queued
// notifications contiguous on the wire.
func (s *Server) sendResponseAndPending(state *connState, resp *blaze.Packet) bool {
	pending := state.Pending
	state.Pending = nil

	state.wmu.Lock()
	defer state.wmu.Unlock()
	if state.closed {
		return false
	}
	if !s.writePacketLocked(state, resp, false) {
		return false
	}
	for _, n := range pending {
		if !s.writePacketLocked(state, n, true) {
			return false
		}
	}
	return true
}

// writePacketLocked writes and logs one packet. state.wmu must be held.
func (s *Server) writePacketLocked(state *connState, p *blaze.Packet, notify bool) bool {
	_ = state.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, e := state.conn.Write(p.Bytes()); e != nil {
		return false
	}
	s.checkSchema(state, p, notify)
	if notify {
		s.log.Printf("[%s] TX NOTIFY comp=0x%04X cmd=0x%04X len=%d (%s)", state.kind, p.Component, p.Command, len(p.Payload), blaze.NotificationName(p.Component, p.Command))
	} else {
		errName := ""
		if p.Error != 0 {
			if n := blaze.GameSchema().ErrorName(p.Component, p.Error); n != "" {
				errName = " " + n
			}
		}
		s.log.Printf("[%s] TX id=%d comp=0x%04X cmd=0x%04X err=0x%04X%s len=%d (%s)", state.kind, p.ID, p.Component, p.Command, p.Error, errName, len(p.Payload), blaze.CommandName(p.Component, p.Command))
	}
	if s.cfg.LogTDF && len(p.Payload) != 0 {
		if outFields, de := blaze.DecodeAll(p.Payload); de == nil {
			label := "TX TDF"
			if notify {
				label = "TX NOTIFY TDF"
			}
			s.log.Printf("[%s] %s:\n%s", state.kind, label, blaze.FormatFieldsSafe(outFields, "  "))
		}
	}
	return true
}

func (s *Server) response(p *blaze.Packet, payload []byte) *blaze.Packet {
	return &blaze.Packet{Component: p.Component, Command: p.Command, Error: 0, QType: blaze.TypeResponse, ID: p.ID, Payload: payload}
}
func (s *Server) errorResp(p *blaze.Packet, code uint16) *blaze.Packet {
	return &blaze.Packet{Component: p.Component, Command: p.Command, Error: code, QType: blaze.TypeError, ID: p.ID}
}
func ipUint(ip string) uint64 {
	v := net.ParseIP(ip)
	if v == nil {
		return 0
	}
	v = v.To4()
	if v == nil {
		return 0
	}
	return uint64(v[0])<<24 | uint64(v[1])<<16 | uint64(v[2])<<8 | uint64(v[3])
}
func (s *Server) dispatch(p *blaze.Packet, fields []blaze.Field, redirect bool, state *connState) *blaze.Packet {
	if p.Component == 0x0005 && p.Command == 0x0001 { // Redirector::getServerInstance
		var w blaze.Writer
		w.UnionStart("ADDR", 0)
		w.GroupStart("VALU")
		w.TagString("HOST", s.cfg.PublicHost)
		w.TagVar("IP", ipUint(s.cfg.PublicIP))
		w.TagVar("PORT", uint64(s.cfg.BlazePort))
		w.GroupEnd()
		// AMAP and NMAP are list<AddressRemapEntry> / list<NameRemapEntry>. SECU is
		// false: the Blaze connection is plaintext behind the gateway.
		w.EmptyList("AMAP", blaze.TDFGroup)
		w.EmptyList("MSGS", blaze.TDFString)
		w.EmptyList("NMAP", blaze.TDFGroup)
		w.TagBool("SECU", false)
		w.TagVar("XDNS", 0)
		return s.response(p, w.Bytes())
	}
	if redirect {
		return s.errorResp(p, 3)
	}
	if resp, ok := s.handleMisc(p, fields, state); ok {
		return resp
	}
	switch p.Component {
	case 0x0009:
		return s.handleUtil(p, fields, state)
	case 0x0001:
		return s.handleAuth(p, fields, state)
	case ComponentGameReporting:
		return s.handleGameReporting(p, fields, state)
	case ComponentAutolog:
		return s.handleAutolog(p, fields, state)
	case ComponentGameManager:
		return s.handleGameManager(p, fields, state)
	case ComponentStats:
		return s.handleStats(p, fields, state)
	case ComponentAssociationLists:
		return s.handleAssociationLists(p, fields, state)
	case ComponentPlaygroups:
		return s.handlePlaygroups(p, fields, state)
	default:
		s.unimplemented(p, "dispatch")
		return s.unimplementedReply(p)
	}
}
func (s *Server) handleUtil(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	switch p.Command {
	case 0x0001: // Util::fetchClientConfig, sections from client_config/<SECTION>.cfg
		section := blaze.FindString(fields, "CFID")
		conf, path, err := loadClientConfig(s.cfg.ClientConfigDir, section)
		switch {
		case err != nil:
			s.log.Printf("[BLAZE] handler=Util.fetchClientConfig(0x0001) section=%q read error %v -- sending empty config", section, err)
		case conf == nil:
			s.log.Printf("[BLAZE] handler=Util.fetchClientConfig(0x0001) section=%q no file (%s) -- sending empty config", section, path)
		default:
			s.log.Printf("[BLAZE] handler=Util.fetchClientConfig(0x0001) section=%q %d keys from %s", section, len(conf), path)
		}
		if strings.EqualFold(section, "NFS12_THERUN") {
			conf = s.withWebAutolog(conf)
		}
		return s.response(p, buildFetchConfigResponse(conf))
	case 0x0002: // Util::ping (keepalive every pingPeriod)
		s.log.Printf("[BLAZE] handler=Util.ping(0x0002)")
		return s.response(p, buildPingResponse(time.Now()))
	case 0x0007: // preAuth
		s.log.Printf("[BLAZE] handler=Util.preAuth(0x0007)")
		// PreAuthResponse; race servers also get the QoS ping sites (they stop after
		// login without their own site).
		if clnt := strings.ToLower(blaze.FindString(fieldGroupOf(fields, "CINF"), "CLNT")); strings.Contains(clnt, "server") {
			sites := []string{dsPingSite}
			s.log.Printf("[DS] preAuth from a race server: QoS ping site %q (%s:%d)", dsPingSite, s.cfg.PublicHost, 17502)
			return s.response(p, buildPreAuthResponseQoS(serverName, &qosSites{Host: s.cfg.PublicHost, Port: 17502, Sites: sites}))
		}
		return s.response(p, buildPreAuthResponse(serverName))
	case 0x0008:
		s.log.Printf("[BLAZE] handler=Util.postAuth(0x0008)")
		// PSS + TELE + TICK + UROP, as Util::PostAuthResponse in the client.
		return s.response(p, buildPostAuthResponse(state.Persona, s.cfg.PublicHost))
	default:
		s.unimplemented(p, "server.go")
		return s.unimplementedReply(p)
	}
}

// personaFromOriginAuthCode: a stable "Origin-xxxxxxxx" persona for an EA
// token that is not a game code.
func personaFromOriginAuthCode(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return "Origin-" + hex.EncodeToString(sum[:4])
}

// originIdentityFromLogin: a stable persona derived from TOKN/PASS.
func originIdentityFromLogin(fields []blaze.Field) (string, string) {
	for _, tag := range []string{"TOKN", "PASS"} {
		if v := strings.TrimSpace(blaze.FindString(fields, tag)); v != "" {
			return personaFromOriginAuthCode(v), tag + " (EA token, hashed)"
		}
	}
	return "LocalPlayer", "nothing (no MAIL/TOKN/PASS)"
}

func fieldUintAuth(fs []blaze.Field, tag string) uint64 {
	for _, f := range fs {
		if f.Tag == tag {
			if v, ok := f.Value.(uint64); ok {
				return v
			}
		}
	}
	return 0
}

func normalizePersonaName(email string) string {
	name := strings.TrimSpace(email)
	if at := strings.IndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}
	// the eatest login prefixes the machine-derived persona with "eatest."
	if strings.HasPrefix(strings.ToLower(name), "eatest.") {
		name = name[len("eatest."):]
	}
	if name == "" {
		name = "LocalPlayer"
	}
	return name
}

func writePersonaDetailsFields(w *blaze.Writer, per database.Persona) {
	w.TagString("DSNM", per.Name)
	w.TagVar("LAST", 0)
	// PID is the persona id; the BlazeId (BUID) is 1000000+account.
	w.TagVar("PID", per.ID)
	w.TagVar("STAS", 0)
}

func writePersonaDetails(w *blaze.Writer, tag string, per database.Persona) {
	w.GroupStart(tag)
	writePersonaDetailsFields(w, per)
	w.GroupEnd()
}

func writeSessionInfo(w *blaze.Writer, tag string, per database.Persona, email string) {
	w.GroupStart(tag)
	w.TagVar("BUID", per.BlazeID)
	w.TagBool("FRST", false)
	w.TagString("KEY", per.SessionKey)
	w.TagVar("LLOG", 0)
	w.TagString("MAIL", email)
	writePersonaDetails(w, "PDTL", per)
	w.TagVar("UID", per.AccountID)
	w.GroupEnd()
}

func buildFullLoginResponse(per database.Persona, email string) []byte {
	var w blaze.Writer
	// Field order of FullLoginResponse as in the game's type metadata.
	w.TagBool("AGUP", false)
	w.TagString("LDHT", "")
	w.TagBool("NTOS", false)
	w.TagString("PCTK", per.SessionKey)
	w.TagString("PRIV", "")
	writeSessionInfo(&w, "SESS", per, email)
	w.TagBool("SPAM", false)
	w.TagString("THST", "")
	w.TagString("TSUI", "")
	w.TagString("TURI", "")
	return w.Bytes()
}

// buildLoginResponse: Authentication::login (0x28) replies with LoginResponse
// (LDHT NTOS PCTK PLST PRIV SKEY SPAM THST TSUI TURI UID). With the persona in
// PLST the client logs it in directly (loginPersona).
func buildLoginResponse(per database.Persona) []byte {
	var w blaze.Writer
	w.TagString("LDHT", "")
	w.TagBool("NTOS", false)
	w.TagString("PCTK", per.SessionKey)
	writeStructListHeader(&w, "PLST", 1)
	writePersonaDetailsFields(&w, per)
	w.GroupEnd()
	w.TagString("PRIV", "")
	w.TagString("SKEY", per.SessionKey)
	w.TagBool("SPAM", false)
	w.TagString("THST", "")
	w.TagString("TSUI", "")
	w.TagString("TURI", "")
	w.TagVar("UID", per.AccountID)
	return w.Bytes()
}

func buildGetPersonaResponse(per database.Persona) []byte {
	var w blaze.Writer
	// GetPersonaResponse, used by Authentication::getPersona (0x005A).
	w.GroupStart("PINF")
	w.TagString("DSNM", per.Name)
	w.TagString("DTCR", "")
	w.TagVar("LADT", 0)
	w.TagString("NSNM", "cem_ea_id")
	w.TagVar("PID", per.ID)
	w.TagVar("STAS", 0)
	w.GroupEnd()
	w.TagVar("UID", per.AccountID)
	return w.Bytes()
}

func buildCreatePersonaResponse(per database.Persona) []byte {
	var w blaze.Writer
	// Authentication::createPersona (command 80 / 0x0050) returns
	// PersonaDetails directly. It is not wrapped in GetPersonaResponse.
	writePersonaDetailsFields(&w, per)
	return w.Bytes()
}

func buildLoginPersonaResponse(per database.Persona, email string) []byte {
	var w blaze.Writer
	// loginPersona answers with SessionInfo.
	w.TagVar("BUID", per.BlazeID)
	w.TagBool("FRST", false)
	w.TagString("KEY", per.SessionKey)
	w.TagVar("LLOG", 0)
	w.TagString("MAIL", email)
	writePersonaDetails(&w, "PDTL", per)
	w.TagVar("UID", per.AccountID)
	return w.Bytes()
}

// applyBlazeIDMode maps the stored identity to the ids put on the wire.
// The database is never changed; only what the client sees.

func (s *Server) ensurePersona(state *connState, requested string) (database.Persona, error) {
	name := strings.TrimSpace(requested)
	if state.Persona.BlazeID != 0 && !isDedicatedServerAccount(state.Email) {
		// Once the connection has been authenticated (especially through a
		// launcher session), PNAM/createPersona must not fork a second identity.
		return state.Persona, nil
	}
	if name == "" && state.Persona.BlazeID != 0 {
		return state.Persona, nil
	}

	// Some EA app paths reach createPersona/loginPersona without MAIL or ticket.
	// A single authenticated launcher on this peer is then the identity.
	if state.Persona.BlazeID == 0 && !isDedicatedServerAccount(state.Email) && state.peer != "" {
		if per, why, ok, err := s.reconcileObservedIdentity(state); err != nil {
			return database.Persona{}, err
		} else if ok {
			state.Persona, state.Email = per, per.Email
			s.online.set(state, per.Name)
			if name != "" && !strings.EqualFold(name, per.Name) {
				s.launchers.shadow(name, per.ID)
				s.log.Printf("[AUTH] persona %q reconciled to canonical %q blazeId=%d (%s)", name, per.Name, per.BlazeID, why)
			}
			s.friendsChanged(true)
			return per, nil
		}
	}

	if name == "" {
		name = normalizePersonaName(state.Email)
	}

	if !isDedicatedServerAccount(state.Email) {
		original := name
		name = fitPersonaName(name)
		if name != original {
			s.log.Printf("[AUTH] persona normalized %q -> %q", original, name)
		}
		if err := checkPersonaName(name); err != nil {
			return database.Persona{}, err
		}
	}

	if state.Persona.BlazeID != 0 && strings.EqualFold(state.Persona.Name, name) {
		return state.Persona, nil
	}
	// Players log in with a game code (or a stored alias / the single-launcher
	// match above), never by name alone; only race servers create personas here.
	if err := s.refuseNameOnly(name); err != nil && state.Persona.BlazeID == 0 && !state.Authenticated && !isDedicatedServerAccount(state.Email) {
		return database.Persona{}, err
	}
	per, err := s.db.GetOrCreateIdentity(state.Email, name)
	if err != nil {
		return database.Persona{}, err
	}
	state.Persona = per
	s.online.set(state, per.Name)
	s.friendsChanged(true)
	return per, nil
}

// maxPersonaName is the longest persona the retail game reliably handles.
// Longer names are normalized before they reach the database or the client.
const maxPersonaName = 16

// fitPersonaName shortens an overlong persona to a stable displayable name:
// drops a trailing ".0" (COMPUTERNAME.0) when that is enough, otherwise keeps
// a prefix plus a 4-hex hash so names with the same start stay distinct.
func fitPersonaName(name string) string {
	name = strings.TrimSpace(name)
	if len([]rune(name)) <= maxPersonaName {
		return name
	}

	if strings.HasSuffix(name, ".0") {
		base := strings.TrimSpace(strings.TrimSuffix(name, ".0"))
		if base != "" && len([]rune(base)) <= maxPersonaName {
			return base
		}
	}

	sum := sha256.Sum256([]byte(strings.ToLower(name)))
	suffix := "-" + hex.EncodeToString(sum[:2]) // 5 chars: -abcd
	keep := maxPersonaName - len([]rune(suffix))
	runes := []rune(name)
	if len(runes) > keep {
		runes = runes[:keep]
	}
	return string(runes) + suffix
}

func checkPersonaName(name string) error {
	n := len([]rune(name))
	if n == 0 {
		return fmt.Errorf("empty persona name")
	}
	if n > maxPersonaName {
		return fmt.Errorf("persona name %q has %d characters (max %d): set PLAYER_NAME= in nfsrun-launcher.ini", name, n, maxPersonaName)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("persona name %q has control characters", name)
		}
	}
	return nil
}

func (s *Server) handleAuth(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	switch p.Command {
	case 0x0028: // Authentication::login (MAIL/PASS/TOKN/TYPE/DVID)
		s.log.Printf("[BLAZE] handler=Authentication.login(0x0028)")

		// One-time game code from the launcher; the persona name is not a
		// credential.
		for _, tag := range []string{"TOKN", "PASS"} {
			if v := strings.TrimSpace(blaze.FindString(fields, tag)); isGameCode(v) {
				per, err := s.loginWithGameCode(state, v, "login "+tag)
				if err != nil {
					s.log.Printf("[AUTH] login game code rejected: %v", err)
					return s.errorResp(p, 1)
				}
				return s.response(p, buildLoginResponse(per))
			}
		}

		state.Email = strings.TrimSpace(blaze.FindString(fields, "MAIL"))
		name := normalizePersonaName(state.Email)
		if isDedicatedServerAccount(state.Email) {
			// every race server copy uses the same built-in account: one persona each
			name = s.ds.allocName(state)
			s.log.Printf("[DS] race server login (%s) -> persona %s", state.Email, name)
		}
		if state.Email == "" {
			// Origin-style login without MAIL: an unknown EA token matched to the
			// single active launcher session.
			persona, how := originIdentityFromLogin(fields)
			name = persona
			state.Email = "eatest." + persona + "@ea.com"
			s.log.Printf("[AUTH] login TYPE=%d without MAIL: persona %q from %s", fieldUintAuth(fields, "TYPE"), persona, how)

			var aliases []string
			// DVID is only a device hint, never a credential.
			if v := strings.TrimSpace(blaze.FindString(fields, "TOKN")); v != "" && !strings.HasPrefix(v, "nfsrun-") {
				if alias := externalAuthAlias("token", v); alias != "" {
					aliases = append(aliases, alias)
				}
			}
			if per, why, ok, err := s.reconcileObservedIdentity(state, aliases...); err != nil {
				s.log.Printf("[AUTH] login identity reconciliation error: %v", err)
				return s.errorResp(p, 1)
			} else if ok {
				state.Persona, state.Email, state.Authenticated = per, per.Email, true
				s.online.set(state, per.Name)
				s.friendsChanged(true)
				s.log.Printf("[AUTH] login reconciled %q -> %q blazeId=%d (%s)", name, per.Name, per.BlazeID, why)
				return s.response(p, buildLoginResponse(per))
			}
		} else if !isDedicatedServerAccount(state.Email) {
			// The EA app may send an eatest.<machine>@ea.com identity while the launcher
			// chose another persona. Only token fingerprints are stored as aliases.
			var aliases []string
			if v := strings.TrimSpace(blaze.FindString(fields, "TOKN")); v != "" && !strings.HasPrefix(v, "nfsrun-") {
				if alias := externalAuthAlias("token", v); alias != "" {
					aliases = append(aliases, alias)
				}
			}
			if per, why, ok, err := s.reconcileObservedIdentity(state, aliases...); err != nil {
				s.log.Printf("[AUTH] login identity reconciliation error: %v", err)
				return s.errorResp(p, 1)
			} else if ok {
				observedEmail, observedName := state.Email, name
				state.Persona, state.Email, state.Authenticated = per, per.Email, true
				s.online.set(state, per.Name)
				s.launchers.shadow(observedName, per.ID)
				s.friendsChanged(true)
				s.log.Printf("[AUTH] login reconciled email=%q persona=%q -> canonical=%q blazeId=%d (%s)", observedEmail, observedName, per.Name, per.BlazeID, why)
				return s.response(p, buildLoginResponse(per))
			}
		}

		per, err := s.ensurePersona(state, name)
		if err != nil {
			s.log.Printf("[AUTH] login db error: %v", err)
			return s.errorResp(p, 1)
		}
		state.Authenticated = true
		s.log.Printf("[AUTH] login accountId=%d email=%q persona=%q personaId=%d blazeId=%d", per.AccountID, state.Email, per.Name, per.ID, per.BlazeID)
		return s.response(p, buildLoginResponse(per))

	case AuthListUserEntitlements2:
		groups := stringList(fields, "GNLS")
		s.log.Printf("[AUTH] listUserEntitlements2 groups=%v -> ONLINE_ACCESS granted", groups)
		return s.response(p, buildEntitlements(groups, state.Persona, time.Now()))

	case 0x0098: // Authentication::originLogin (AUTH, TYPE)
		s.log.Printf("[BLAZE] handler=Authentication.originLogin(0x0098)")
		auth := strings.TrimSpace(blaze.FindString(fields, "AUTH"))
		if isGameCode(auth) {
			per, err := s.loginWithGameCode(state, auth, "originLogin AUTH")
			if err != nil {
				s.log.Printf("[AUTH] originLogin game code rejected: %v", err)
				return s.errorResp(p, 1)
			}
			return s.response(p, buildFullLoginResponse(per, state.Email))
		}
		persona := personaFromOriginAuthCode(auth)
		// same account key as the eatest login
		state.Email = "eatest." + persona + "@ea.com"

		// Unknown EA auth codes are not identities. If the private launcher is
		// active, bind the token fingerprint to its canonical persona.
		if auth != "" {
			alias := externalAuthAlias("auth", auth)
			if per, why, ok, err := s.reconcileObservedIdentity(state, alias); err != nil {
				s.log.Printf("[AUTH] originLogin reconciliation error: %v", err)
				return s.errorResp(p, 1)
			} else if ok {
				state.Persona, state.Email, state.Authenticated = per, per.Email, true
				s.online.set(state, per.Name)
				s.friendsChanged(true)
				s.log.Printf("[AUTH] originLogin reconciled %q -> %q blazeId=%d (%s)", persona, per.Name, per.BlazeID, why)
				return s.response(p, buildFullLoginResponse(per, state.Email))
			}
		}

		per, err := s.ensurePersona(state, persona)
		if err != nil {
			s.log.Printf("[AUTH] originLogin db error: %v", err)
			return s.errorResp(p, 1)
		}
		state.Authenticated = true
		s.log.Printf("[AUTH] originLogin persona=%q accountId=%d blazeId=%d", per.Name, per.AccountID, per.BlazeID)
		return s.response(p, buildFullLoginResponse(per, state.Email))

	case 0x0050: // Authentication::createPersona (PNAM)
		s.log.Printf("[BLAZE] handler=Authentication.createPersona(0x0050)")
		name := blaze.FindString(fields, "PNAM")
		per, err := s.ensurePersona(state, name)
		if err != nil {
			s.log.Printf("[AUTH] createPersona db error: %v", err)
			return s.errorResp(p, 1)
		}
		s.log.Printf("[AUTH] createPersona persona=%q blazeId=%d", per.Name, per.BlazeID)
		return s.response(p, buildCreatePersonaResponse(per))

	case 0x005a: // Authentication::getPersona
		s.log.Printf("[BLAZE] handler=Authentication.getPersona(0x005A)")
		per, err := s.ensurePersona(state, "")
		if err != nil {
			s.log.Printf("[AUTH] getPersona db error: %v", err)
			return s.errorResp(p, 1)
		}
		return s.response(p, buildGetPersonaResponse(per))

	case 0x006e: // Authentication::loginPersona (PNAM, may be empty)
		s.log.Printf("[BLAZE] handler=Authentication.loginPersona(0x006E)")
		name := blaze.FindString(fields, "PNAM")
		var per database.Persona
		var err error
		if state.Persona.BlazeID != 0 {
			// One persona per account: race servers log in with the shared server
			// account and then ask for their computer name; keep the login's persona.
			per = state.Persona
			if name != "" && !strings.EqualFold(name, per.Name) {
				s.log.Printf("[AUTH] loginPersona asked for %q: keeping the account's persona %q", name, per.Name)
			}
		} else if per, err = s.ensurePersona(state, name); err != nil {
			s.log.Printf("[AUTH] loginPersona error: %v", err)
			return s.errorResp(p, 1)
		}
		state.Authenticated = true
		s.log.Printf("[AUTH] loginPersona persona=%q blazeId=%d", per.Name, per.BlazeID)
		if !state.UserAnnounced {
			// Announce the local user so the client's UserManager registers it.
			state.Pending = append(state.Pending, loginNotifications(per)...)
			state.UserAnnounced = true
			s.log.Printf("[AUTH] queued UserSessions UserAdded+UserUpdated for blazeId=%d", per.BlazeID)
			s.announceOnline(state) // friends see this player come online
		}
		return s.response(p, buildLoginPersonaResponse(per, state.Email))

	case 0x0046: // Authentication::logout
		s.log.Printf("[BLAZE] handler=Authentication.logout(0x0046)")
		state.Authenticated = false
		return s.response(p, nil)

	default:
		s.unimplemented(p, "Authentication")
		return s.unimplementedReply(p)
	}
}
