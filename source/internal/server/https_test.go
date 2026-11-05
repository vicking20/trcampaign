package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nfsrun-server/internal/webcert"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// pinnedClient trusts only the certificate key with the given pin.
func pinnedClient(pin string) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			c, err := x509.ParseCertificate(raw[0])
			if err != nil || webcert.PinOf(c) != pin {
				return errors.New("pin mismatch")
			}
			return nil
		},
	}}}
}

func serveForTest(t *testing.T) (*Server, string, string, context.CancelFunc) {
	t.Helper()
	cfg := testConfig()
	cfg.RedirectorListen, cfg.BlazeListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.RedirectorFrontListen, cfg.BlazeFrontListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.DSEnabled = false
	cfg.HTTPListen = freeAddr(t)
	cfg.LauncherHTTPSListen = freeAddr(t)
	cfg.PublicHost = "127.0.0.1"
	var logs syncBuf
	s := New(cfg, &fakeStore{}, log.New(&logs, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx) }()
	time.Sleep(300 * time.Millisecond)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	return s, cfg.HTTPListen, cfg.LauncherHTTPSListen, cancel
}

func TestLauncherAPIOnlyOverHTTPS(t *testing.T) {
	s, plainAddr, tlsAddr, cancel := serveForTest(t)
	defer cancel()
	url, pin, _, err := LauncherClientConfig(s.cfg)
	if err != nil || pin == "" || !strings.HasPrefix(url, "https://127.0.0.1:") {
		t.Fatalf("client config url=%q pin=%q err=%v", url, pin, err)
	}
	get := func(c *http.Client, u string) (int, string) {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "nfsrun-launcher/test")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(pinnedClient(pin), "https://"+tlsAddr+"/nfsrun/players?persona=x"); code == http.StatusForbidden || code == http.StatusNotFound {
		t.Fatalf("HTTPS launcher API: %d %s", code, body)
	}
	if code, body := get(http.DefaultClient, "http://"+plainAddr+"/nfsrun/players?persona=x"); code != http.StatusForbidden || !strings.Contains(body, "https_required") {
		t.Fatalf("plain launcher API: %d %s", code, body)
	}
	if code, _ := get(pinnedClient(pin), "https://"+tlsAddr+"/Persona/1"); code != http.StatusNotFound && code != http.StatusForbidden {
		t.Fatalf("game route on the HTTPS port: %d", code)
	}
	// a client pinning another key must not connect
	if _, err := pinnedClient(strings.Repeat("0", 64)).Get("https://" + tlsAddr + "/health"); err == nil {
		t.Fatal("wrong pin accepted")
	}
}

func TestClientAddrBehindTrustedProxy(t *testing.T) {
	cf, err := parseTrustedProxies("cloudflare, 10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(remote string, h map[string]string) *http.Request {
		r := httptest.NewRequest("GET", "/nfsrun/players", nil)
		r.RemoteAddr = remote
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		remote string
		h      map[string]string
		want   string
	}{
		{"173.245.48.10:443", map[string]string{"CF-Connecting-IP": "198.51.100.7"}, "198.51.100.7"},
		{"203.0.113.9:1000", map[string]string{"CF-Connecting-IP": "198.51.100.7"}, "203.0.113.9"},
		{"10.0.0.5:1000", map[string]string{"X-Forwarded-For": "198.51.100.8, 173.245.48.10"}, "198.51.100.8"},
		{"[2606:4700::1]:443", map[string]string{"CF-Connecting-IP": "2001:db8::5"}, "2001:db8::5"},
		{"173.245.48.10:443", nil, "173.245.48.10"},
	}
	for _, c := range cases {
		if got := hostOf(clientAddrHTTP(mk(c.remote, c.h), cf)); got != c.want {
			t.Errorf("%s %v -> %s, want %s", c.remote, c.h, got, c.want)
		}
	}
	if _, err := parseTrustedProxies("cloudflare, nonsense"); err == nil {
		t.Fatal("bad TRUSTED_PROXIES accepted")
	}
}
