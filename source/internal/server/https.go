package server

// Launcher API over HTTPS, and client addresses behind a reverse proxy.
//
// The launcher's session ticket only travels over LAUNCHER_HTTPS_LISTEN. The
// plain HTTP port keeps serving what the game itself uses (web Autolog
// backend, photos), and refuses /nfsrun/.

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"nfsrun-server/internal/config"
	"nfsrun-server/internal/webcert"
)

// cloudflareRanges: https://www.cloudflare.com/ips-v4 and /ips-v6.
var cloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// parseTrustedProxies reads TRUSTED_PROXIES: "cloudflare", IPs and CIDRs.
func parseTrustedProxies(v string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		switch {
		case item == "":
			continue
		case strings.EqualFold(item, "cloudflare"):
			for _, c := range cloudflareRanges {
				_, n, _ := net.ParseCIDR(c)
				out = append(out, n)
			}
			continue
		case !strings.Contains(item, "/"):
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP, CIDR or \"cloudflare\"", item)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			item = fmt.Sprintf("%s/%d", ip, bits)
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %v", err)
		}
		out = append(out, n)
	}
	return out, nil
}

func ipIn(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientAddrHTTP returns the client address of a request: the peer, or, when
// the peer is a trusted proxy, the address it reports (CF-Connecting-IP, then
// the right-most untrusted X-Forwarded-For entry).
func clientAddrHTTP(r *http.Request, proxies []*net.IPNet) string {
	peer := net.ParseIP(hostOf(r.RemoteAddr))
	if peer == nil || len(proxies) == 0 || !ipIn(peer, proxies) {
		return r.RemoteAddr
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); ip != nil {
		return net.JoinHostPort(ip.String(), "0")
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			break
		}
		if !ipIn(ip, proxies) {
			return net.JoinHostPort(ip.String(), "0")
		}
	}
	return r.RemoteAddr
}

func listenPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

// LauncherClientConfig returns what a player package needs: the launcher API
// URL and, for a self-signed certificate reached directly, its key pin. It
// creates the certificate when missing.
func LauncherClientConfig(c config.Config) (url, pin string, created bool, err error) {
	if c.LauncherHTTPSListen == "" {
		url = c.LauncherURL
		if url == "" {
			url = "http://" + net.JoinHostPort(c.PublicHost, listenPort(c.HTTPListen))
		}
		return url, "", false, nil
	}
	created, err = webcert.Ensure(c.LauncherTLSCert, c.LauncherTLSKey, []string{c.PublicHost, c.PublicIP, "localhost", "127.0.0.1"})
	if err != nil {
		return "", "", false, fmt.Errorf("launcher certificate: %w", err)
	}
	if c.LauncherURL != "" {
		return c.LauncherURL, "", created, nil
	}
	url = "https://" + net.JoinHostPort(c.PublicHost, listenPort(c.LauncherHTTPSListen))
	cert, err := os.ReadFile(c.LauncherTLSCert)
	if err != nil {
		return "", "", created, err
	}
	if self, _ := webcert.SelfSigned(cert); self {
		if pin, err = webcert.Pin(cert); err != nil {
			return "", "", created, err
		}
	}
	return url, pin, created, nil
}

// launcherOnly serves only the launcher API (and /health) on the HTTPS port.
func launcherOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/nfsrun/") && r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noPlainLauncher refuses the launcher API on the plain HTTP port.
func noPlainLauncher(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/nfsrun/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"https_required","message":"use the server's HTTPS launcher address"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// startLauncherHTTPS starts the HTTPS listener; nil when it is off.
func (s *Server) startLauncherHTTPS(handler http.Handler) (*http.Server, error) {
	if s.cfg.LauncherHTTPSListen == "" {
		return nil, nil
	}
	url, pin, created, err := LauncherClientConfig(s.cfg)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(s.cfg.LauncherTLSCert, s.cfg.LauncherTLSKey)
	if err != nil {
		return nil, fmt.Errorf("launcher certificate: %w", err)
	}
	ln, err := net.Listen("tcp", s.cfg.LauncherHTTPSListen)
	if err != nil {
		return nil, fmt.Errorf("launcher HTTPS listen: %w", err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	}
	if created {
		s.log.Printf("[HTTPS] created a self-signed launcher certificate: %s", s.cfg.LauncherTLSCert)
	}
	note := "certificate from a CA"
	if pin != "" {
		note = "self-signed, pin " + pin
	} else if s.cfg.LauncherURL != "" {
		note = "players use " + url
	}
	s.log.Printf("[HTTPS] launcher API listening on %s (%s)", s.cfg.LauncherHTTPSListen, note)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if e := srv.ServeTLS(ln, "", ""); e != nil && e != http.ErrServerClosed {
			s.log.Printf("HTTPS error: %v", e)
		}
	}()
	return srv, nil
}
