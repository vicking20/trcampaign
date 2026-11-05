package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInServerAndOverrides(t *testing.T) {
	oldH, oldP := bakedServerHTTP, bakedServerPin
	defer func() { bakedServerHTTP, bakedServerPin = oldH, oldP }()
	bakedServerHTTP, bakedServerPin = "https://play.example.org:8443/", "aa"

	load := func(ini string) config {
		dir := t.TempDir()
		if ini != "" {
			if err := os.WriteFile(filepath.Join(dir, iniName), []byte(ini), 0644); err != nil {
				t.Fatal(err)
			}
		}
		c, err := loadConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	cases := []struct {
		ini, http, pin string
	}{
		{"", "https://play.example.org:8443", "aa"},                                              // first start: ini created
		{"SERVER_HTTP=\r\nSERVER_CERT_PIN=\r\n", "https://play.example.org:8443", "aa"},          // empty = built-in
		{"SERVER_HTTP=https://other.example.org:8443\r\n", "https://other.example.org:8443", ""}, // other server: no built-in pin
		{"SERVER_HTTP=https://other.example.org:8443\r\nSERVER_CERT_PIN=bb\r\n", "https://other.example.org:8443", "bb"},
		{"SERVER_CERT_PIN=bb\r\n", "https://play.example.org:8443", "bb"}, // new certificate, same server
		{"SERVER_CERT_PIN=off\r\n", "https://play.example.org:8443", ""},
		{"SERVER_HTTP=off\r\n", "", ""},
	}
	for _, tc := range cases {
		c := load(tc.ini)
		if c.ServerHTTP != tc.http || c.ServerCertPin != tc.pin {
			t.Errorf("ini %q -> %q %q, want %q %q", tc.ini, c.ServerHTTP, c.ServerCertPin, tc.http, tc.pin)
		}
	}
	// the created default ini keeps the built-in values
	dir := t.TempDir()
	if _, err := loadConfig(dir); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadConfig(dir); c.ServerHTTP != "https://play.example.org:8443" || c.ServerCertPin != "aa" {
		t.Fatalf("default ini overrides the built-in preset: %q %q", c.ServerHTTP, c.ServerCertPin)
	}
}
