package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nfsrun-server/internal/webcert"
)

func usePin(t *testing.T, pin string) {
	t.Helper()
	serverPin, serverTransport, transportOnce = "", nil, sync.Once{}
	if err := setServerPin(pin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverPin, serverTransport, transportOnce = "", nil, sync.Once{} })
}

func TestGameCodeOverPinnedHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path != "/nfsrun/auth/gamecode":
			http.NotFound(w, r)
		case r.Header.Get("X-NFSRun-Session") != "ticket":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			_, _ = w.Write([]byte(`{"code":"nfsrun-g1.0123456789abcdef0123456789abcdef","expiresIn":300}`))
		}
	}))
	defer srv.Close()
	pin := webcert.PinOf(srv.Certificate())

	usePin(t, "sha256/"+strings.ToUpper(pin))
	code, err := fetchGameCode(srv.URL, "ticket")
	if err != nil || code != "nfsrun-g1.0123456789abcdef0123456789abcdef" {
		t.Fatalf("pinned: %q %v", code, err)
	}
	if _, err := fetchGameCode(srv.URL, "wrong"); err == nil {
		t.Fatal("unknown session accepted")
	}

	usePin(t, strings.Repeat("ab", 32))
	if _, err := fetchGameCode(srv.URL, "ticket"); err == nil || !strings.Contains(err.Error(), "SERVER_CERT_PIN") {
		t.Fatalf("wrong pin: %v", err)
	}

	// no pin: a self-signed certificate is not trusted
	usePin(t, "")
	if _, err := fetchGameCode(srv.URL, "ticket"); err == nil {
		t.Fatal("self-signed certificate accepted without a pin")
	}
}
