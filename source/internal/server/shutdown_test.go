package server

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"testing"
	"time"
)

// Serve must return quickly even with open game connections and a
// launcher's live channel.
func TestServeStopsWithOpenConnections(t *testing.T) {
	cfg := testConfig()
	cfg.RedirectorListen, cfg.BlazeListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.RedirectorFrontListen, cfg.BlazeFrontListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.DSEnabled = false
	cfg.HTTPListen = freeAddr(t)
	cfg.LauncherHTTPSListen = freeAddr(t)
	var logs syncBuf
	s := New(cfg, &fakeStore{}, log.New(&logs, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	time.Sleep(300 * time.Millisecond)
	// a live channel that never ends
	_, pin, _, _ := LauncherClientConfig(cfg)
	req, _ := http.NewRequest("GET", "https://"+cfg.LauncherHTTPSListen+"/nfsrun/stream?persona=x", nil)
	req.Header.Set("User-Agent", "nfsrun-launcher/test")
	c := pinnedClient(pin)
	c.Timeout = 0
	go c.Do(req)
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case <-done:
		if time.Since(start) > 6*time.Second {
			t.Fatal("slow stop")
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("Serve did not return\n%s", logs.String())
	}
	_ = bytes.MinRead
}
