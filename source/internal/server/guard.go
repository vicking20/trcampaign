package server

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/blaze"
)

// Client guard: the public ports serve the game and nothing else.
// Layers (CLIENT_GUARD=false turns them off):
//  1. optional IP allowlist (GUARD_ALLOW_IPS) and per-IP connection limit
//     (GUARD_MAX_CONN_PER_IP) on every public listener;
//  2. protocol sniffing at the public fronts: the Redirector must open with an
//     SSLv3 ClientHello (record version 0x0300), Blaze with Util.preAuth;
//  3. identity checks: getServerInstance and preAuth must carry the game's
//     client name, and every RPC outside the login sequence needs a login;
//  4. HTTP: only the game's ProtoHttp user agent; /health only locally.
//
// This filters scanners and other software; it is not proof of identity. The
// allowlist is the hard restriction for a known set of players.

const (
	guardSniffTimeout = 10 * time.Second
	blazeHeaderLen    = 12
)

type guard struct {
	mu      sync.Mutex
	perIP   map[string]int
	allow   []*net.IPNet
	maxConn int
	enabled bool

	realAddr sync.Map // backend-side local addr -> real client addr (front proxies)

	lastLog map[string]time.Time
}

func newGuard(enabled bool, allowList string, maxConn int) *guard {
	g := &guard{perIP: map[string]int{}, maxConn: maxConn, enabled: enabled, lastLog: map[string]time.Time{}}
	for _, item := range strings.Split(allowList, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if strings.Contains(item, ":") {
				item += "/128"
			} else {
				item += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(item); err == nil {
			g.allow = append(g.allow, n)
		}
	}
	return g
}

func hostOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

// admit applies the allowlist and per-IP limit; release must be called when done.
func (g *guard) admit(remote string) (ok bool, why string, release func()) {
	ip := hostOf(remote)
	if len(g.allow) > 0 {
		pip := net.ParseIP(ip)
		allowed := pip != nil && pip.IsLoopback()
		for _, n := range g.allow {
			if pip != nil && n.Contains(pip) {
				allowed = true
			}
		}
		if !allowed {
			return false, "not in GUARD_ALLOW_IPS", func() {}
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.enabled && g.maxConn > 0 && g.perIP[ip] >= g.maxConn {
		return false, "too many connections from this IP", func() {}
	}
	g.perIP[ip]++
	return true, "", func() {
		g.mu.Lock()
		g.perIP[ip]--
		if g.perIP[ip] <= 0 {
			delete(g.perIP, ip)
		}
		g.mu.Unlock()
	}
}

// shouldLog rate-limits rejection log lines per IP (one per minute).
func (g *guard) shouldLog(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.lastLog[key]; ok && time.Since(t) < time.Minute {
		return false
	}
	g.lastLog[key] = time.Now()
	return true
}

// sniffFront reads the first bytes a client sends on a public front and checks
// they look like the game. It returns the bytes to forward.
func sniffFront(c net.Conn, kind string) ([]byte, bool, string) {
	_ = c.SetReadDeadline(time.Now().Add(guardSniffTimeout))
	defer c.SetReadDeadline(time.Time{})
	switch kind {
	case "REDIRECTOR":
		head := make([]byte, 3)
		if _, err := io.ReadFull(c, head); err != nil {
			return nil, false, "no data"
		}
		// SSL record: handshake, version 3.0
		if head[0] != 0x16 || head[1] != 0x03 || head[2] != 0x00 {
			return head, false, "not an SSLv3 ClientHello"
		}
		return head, true, ""
	default: // BLAZE plaintext
		head := make([]byte, blazeHeaderLen)
		if _, err := io.ReadFull(c, head); err != nil {
			return nil, false, "no data"
		}
		n := binary.BigEndian.Uint16(head[0:2])
		comp := binary.BigEndian.Uint16(head[2:4])
		cmd := binary.BigEndian.Uint16(head[4:6])
		qtype := binary.BigEndian.Uint16(head[8:10])
		// First message of every game session is Util.preAuth (or resumeSession after a drop).
		first := (comp == 0x0009 && cmd == 0x0007) || (comp == ComponentUserSessions && cmd == USResumeSession)
		if !first || qtype&0xF000 != blaze.TypeRequest || n > 8192 {
			return head, false, "first message is not the game's preAuth"
		}
		return head, true, ""
	}
}

// identityOK checks the client identity fields of the first RPC.
func identityOK(p *blaze.Packet, fields []blaze.Field) (bool, string) {
	switch {
	case p.Component == 0x0005 && p.Command == 0x0001: // Redirector.getServerInstance
		if !gameClientName(blaze.FindString(fields, "CLNT")) || !strings.HasPrefix(strings.ToLower(blaze.FindString(fields, "NAME")), "nfs-therun") {
			return false, "Redirector request is not from nfs-therun (CLNT=" + blaze.FindString(fields, "CLNT") + " NAME=" + blaze.FindString(fields, "NAME") + ")"
		}
	case p.Component == 0x0009 && p.Command == 0x0007: // Util.preAuth
		for _, f := range fields {
			if f.Tag == "CINF" {
				if g, ok := f.Value.([]blaze.Field); ok && gameClientName(blaze.FindString(g, "CLNT")) {
					return true, ""
				}
			}
		}
		return false, "preAuth is not from the nfs client"
	}
	return true, ""
}

// gameClientName accepts the game ("nfs client") and its own dedicated server
// mode (same exe started with -dedicated; "nfs server" or similar).
func gameClientName(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "nfs client" || v == "nfs server" || v == "nfs dedicated server" || (strings.HasPrefix(v, "nfs ") && len(v) < 32)
}

// allowedBeforeLogin lists the RPCs a client may send before it has a persona.
func allowedBeforeLogin(p *blaze.Packet) bool {
	switch p.Component {
	case 0x0005: // Redirector
		return true
	case 0x0009: // Util: fetchClientConfig, ping, preAuth, postAuth, getTelemetryServer, setClientMetrics
		switch p.Command {
		case 0x0001, 0x0002, 0x0005, 0x0007, 0x0008, 0x0016:
			return true
		}
	case 0x0001: // Authentication: login, createPersona, loginPersona, originLogin, logout, getPersona
		switch p.Command {
		case 0x0028, 0x0050, 0x006E, 0x0098, 0x0046, 0x005A:
			return true
		}
	case ComponentUserSessions:
		return p.Command == USResumeSession
	}
	return false
}

// httpGuard: only the game's DirtySDK HTTP client is served.
func (s *Server) httpGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// behind a trusted proxy, everything below sees the client's address
		r.RemoteAddr = clientAddrHTTP(r, s.proxies)
		ok, why, release := s.guard.admit(r.RemoteAddr)
		if !ok {
			if s.guard.shouldLog("http-" + hostOf(r.RemoteAddr)) {
				s.log.Printf("[GUARD] HTTP %s from %s refused: %s", r.URL.Path, r.RemoteAddr, why)
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/nfsrun/stream" {
			// the launchers' live channel stays open all game long and does not count
			// against GUARD_MAX_CONN_PER_IP
			release()
		} else {
			defer release()
		}
		if s.cfg.ClientGuard {
			ip := net.ParseIP(hostOf(r.RemoteAddr))
			local := ip != nil && ip.IsLoopback()
			game := strings.Contains(r.UserAgent(), "ProtoHttp") ||
				(strings.HasPrefix(r.UserAgent(), "nfsrun-launcher") && strings.HasPrefix(r.URL.Path, "/nfsrun/")) ||
				// web Autolog: the game always sends its Blaze session key
				(r.Header.Get("X-SESSION-KEY") != "" && webRoute(r.URL.Path) != "")
			if !game && !(local && r.URL.Path == "/health") {
				if s.guard.shouldLog("http-ua-" + hostOf(r.RemoteAddr)) {
					s.log.Printf("[GUARD] HTTP %s %s from %s refused: not the game client (User-Agent %q)", r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
				}
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientAddr returns the real client address for a backend connection that came
// through a public front proxy.
func (s *Server) clientAddr(c net.Conn) string {
	if v, ok := s.guard.realAddr.Load(c.RemoteAddr().String()); ok {
		return v.(string)
	}
	return c.RemoteAddr().String()
}
