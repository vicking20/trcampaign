package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsWhenKeysAbsent(t *testing.T) {
	c, err := Load(writeEnv(t, "PUBLIC_IP=1.2.3.4\r\nBASE_DOMAIN=ignored.example\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicIP != "1.2.3.4" || c.BlazePort != 10041 || c.AuthMode != "invite" || c.LogTDF {
		t.Fatalf("unexpected config %+v", c)
	}
}

func TestLauncherAuthConfig(t *testing.T) {
	c, err := Load(writeEnv(t, "AUTH_MODE=open\nAUTH_SESSION_HOURS=4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMode != "open" || c.AuthSessionHours != 4 {
		t.Fatalf("unexpected launcher auth config: mode=%q hours=%d", c.AuthMode, c.AuthSessionHours)
	}
	if _, err := Load(writeEnv(t, "AUTH_MODE=tofu\n")); err == nil {
		t.Fatal("old AUTH_MODE name accepted")
	}

	if _, err := Load(writeEnv(t, "AUTH_SESSION_HOURS=0\n")); err == nil {
		t.Fatal("expected invalid AUTH_SESSION_HOURS to fail")
	}
}
