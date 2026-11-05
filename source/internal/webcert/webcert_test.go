package webcert

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureAndPin(t *testing.T) {
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "certs", "launcher.crt"), filepath.Join(dir, "certs", "launcher.key")
	created, err := Ensure(crt, key, []string{"play.example.org", "203.0.113.10", "play.example.org"})
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if created, err := Ensure(crt, key, nil); err != nil || created {
		t.Fatalf("second Ensure rewrote the files: %v %v", created, err)
	}
	cpem, _ := os.ReadFile(crt)
	kpem, _ := os.ReadFile(key)
	pair, err := tls.X509KeyPair(cpem, kpem)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := leaf(cpem)
	if len(c.DNSNames) != 1 || len(c.IPAddresses) != 1 || c.NotAfter.Before(time.Now().AddDate(9, 0, 0)) {
		t.Fatalf("names=%v ips=%v notAfter=%v", c.DNSNames, c.IPAddresses, c.NotAfter)
	}
	pin, err := Pin(cpem)
	if err != nil || len(pin) != 64 || pin != PinOf(c) {
		t.Fatalf("pin %q %v", pin, err)
	}
	if ss, _ := SelfSigned(cpem); !ss {
		t.Fatal("not self-signed")
	}
	_ = pair
	if n, err := NormalizePin("sha256/" + strings.ToUpper(pin)); err != nil || n != pin {
		t.Fatalf("normalize: %q %v", n, err)
	}
	if _, err := NormalizePin("abc"); err == nil {
		t.Fatal("short pin accepted")
	}
}
