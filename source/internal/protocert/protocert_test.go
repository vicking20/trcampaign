package protocert

import (
	"bytes"
	"encoding/asn1"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateMatchesTheOldProtoSSLRecipe(t *testing.T) {
	now := time.Date(2026, 10, 19, 3, 0, 0, 0, time.UTC)
	crt, key, err := Generate("example.net", now)
	if err != nil {
		t.Fatal(err)
	}
	if p := Problems(crt, key, HostsFor("example.net"), now); len(p) != 0 {
		t.Fatal(p)
	}
	info, _ := Inspect(crt, key)
	if info.SubjectCN != "*.example.net" || !info.IssuerOK || !info.OuterPatched || !info.KeyMatches {
		t.Fatalf("%+v", info)
	}
	// exactly one md5WithRSA OID (the TBS one) and one rsaEncryption OID
	// right before the signature, i.e. the "second occurrence 04 -> 01" patch
	der, _ := pem.Decode(crt)
	md5oid := []byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x04}
	if n := bytes.Count(der.Bytes, md5oid); n != 1 {
		t.Fatalf("md5WithRSA OID %d times", n)
	}
	var rc rawCert
	if _, err := asn1.Unmarshal(der.Bytes, &rc); err != nil || !rc.SigAlg.Algorithm.Equal(oidRSAEncryption) {
		t.Fatal("outer algorithm not rsaEncryption", err)
	}
	// size in the range of the certificates that work (handshake 678)
	if l := len(der.Bytes); l < 640 || l > 720 {
		t.Fatalf("DER %d bytes", l)
	}
	// wrong host / wrong domain
	if p := Problems(crt, key, []string{"redirector.other.org"}, now); len(p) != 1 || !strings.Contains(p[0], "does not cover") {
		t.Fatal(p)
	}
	// another key
	_, key2, _ := Generate("example.net", now)
	if p := Problems(crt, key2, nil, now); len(p) != 1 || !strings.Contains(p[0], "private key") {
		t.Fatal(p)
	}
}

func TestMatchesHost(t *testing.T) {
	for _, c := range []struct {
		cn, host string
		ok       bool
	}{
		{"*.a.org", "nfs.a.org", true}, {"*.a.org", "scert-nfs.a.org", true},
		{"*.a.org", "x.y.a.org", false}, {"*.a.org", "a.org", false}, {"nfs.a.org", "NFS.A.ORG", true},
	} {
		if MatchesHost(c.cn, c.host) != c.ok {
			t.Fatal(c)
		}
	}
}

func TestExeHosts(t *testing.T) {
	dir := t.TempDir()
	mk := func(domain string) string {
		b := make([]byte, RedirectorSlots[3].Offset+64)
		copy(b, "MZ")
		b[0x3c] = 0x80
		copy(b[0x80:], "PE\x00\x00")
		for _, s := range RedirectorSlots {
			v := s.Retail
			if domain != "" {
				v = s.Prefix + domain
			}
			copy(b[s.Offset:s.Offset+s.Size], append([]byte(v), make([]byte, s.Size-len(v))...))
		}
		p := filepath.Join(dir, domain+"game.exe")
		os.WriteFile(p, b, 0644)
		return p
	}
	hosts, d, err := ExeHosts(mk("example.net"))
	if err != nil || d != "example.net" || len(hosts) != 4 || hosts[3] != "redirector.example.net" {
		t.Fatal(hosts, d, err)
	}
	if _, _, err := ExeHosts(mk("")); err == nil || !strings.Contains(err.Error(), "patch_client") {
		t.Fatal("retail exe must be reported", err)
	}
}
