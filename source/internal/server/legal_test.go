package server

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
)

func TestLegalDocs(t *testing.T) {
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	var logs bytes.Buffer
	s := New(cfg, &fakeStore{}, log.New(&logs, "", 0))
	req := func(cmd uint16, lang string) (string, uint64, string) {
		var w blaze.Writer
		w.TagString("CTRY", "US")
		w.TagString("LANG", lang)
		w.TagString("PTFM", "pc")
		w.TagVar("TEXT", 0)
		fs, _ := blaze.DecodeAll(w.Bytes())
		resp, ok := s.handleLegal(&blaze.Packet{Component: 1, Command: cmd}, fs)
		if !ok {
			t.Fatal("not handled", cmd)
		}
		out, err := blaze.DecodeAll(resp.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var text, ver string
		for _, f := range out {
			switch f.Tag {
			case "TCOT":
				text, _ = f.Value.(string)
			case "LDVC":
				ver, _ = f.Value.(string)
			}
		}
		return text, fieldUint(out, "TCOL"), ver
	}
	// first call writes the defaults; TCOL is the byte length of TCOT
	tos, n, ver := req(0x00F6, "")
	if !strings.Contains(tos, "TERMS OF USE") || n != uint64(len(tos)) || ver == "" {
		t.Fatal(n, len(tos), ver)
	}
	pp, n, _ := req(0x002F, "")
	if !strings.Contains(pp, "PRIVACY POLICY") || n != uint64(len(pp)) {
		t.Fatal(n, len(pp))
	}
	dir := s.legalDir()
	if _, err := os.Stat(filepath.Join(dir, "terms_of_service.txt")); err != nil {
		t.Fatal(err)
	}
	// owner's file (CRLF, BOM) and a language variant
	os.WriteFile(filepath.Join(dir, "privacy_policy.txt"), []byte("\xEF\xBB\xBFLine 1\r\nLine 2\r\n"), 0644)
	os.WriteFile(filepath.Join(dir, "privacy_policy.ptBR.txt"), []byte("Politica"), 0644)
	if pp, n, _ = req(0x002F, ""); pp != "Line 1\nLine 2\n" || n != 14 {
		t.Fatalf("%q %d", pp, n)
	}
	if pp, _, _ = req(0x002F, "ptBR"); pp != "Politica\n" {
		t.Fatalf("%q", pp)
	}
	if pp, _, _ = req(0x002F, "../x"); pp != "Line 1\nLine 2\n" {
		t.Fatalf("lang must not escape the folder: %q", pp)
	}
	// getTosInfo: no download host, so the client asks over Blaze
	resp, _ := s.handleLegal(&blaze.Packet{Component: 1, Command: 0x002A}, nil)
	fs, _ := blaze.DecodeAll(resp.Payload)
	for _, f := range fs {
		if v, ok := f.Value.(string); ok && v != "" {
			t.Fatal(f.Tag, v)
		}
	}
}
