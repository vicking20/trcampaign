package launcherpreset

import (
	"bytes"
	"testing"
)

func TestStampAndParse(t *testing.T) {
	if p := Parse(Block); p != (Preset{}) {
		t.Fatalf("empty block parsed as %+v", p)
	}
	exe := append(append([]byte("MZ....code...."), Block...), []byte("....tail")...)
	pin := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	out, err := Stamp(exe, Preset{"https://play.example.org:8443/", pin, "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(exe) || !bytes.HasSuffix(out, []byte("....tail")) {
		t.Fatal("exe size or tail changed")
	}
	i := bytes.Index(out, []byte(Header))
	if p := Parse(string(out[i : i+len(Block)])); p != (Preset{"https://play.example.org:8443", pin, "example.org"}) {
		t.Fatalf("parsed %+v", p)
	}
	// restamp (e.g. new certificate), then clear
	out2, err := Stamp(out, Preset{ServerHTTP: "http://other.example.org:18080"})
	if err != nil {
		t.Fatal(err)
	}
	if p := Parse(string(out2[i : i+len(Block)])); p != (Preset{ServerHTTP: "http://other.example.org:18080"}) {
		t.Fatalf("restamped %+v", p)
	}
	if _, err := Stamp([]byte("MZ no block"), Preset{ServerHTTP: "https://x"}); err == nil {
		t.Fatal("exe without block accepted")
	}
	if _, err := Stamp(append(exe, Block...), Preset{ServerHTTP: "https://x"}); err == nil {
		t.Fatal("two blocks accepted")
	}
	if _, err := Stamp(exe, Preset{ServerHTTP: "https://x\nEVIL=1"}); err == nil {
		t.Fatal("line break accepted")
	}
}
