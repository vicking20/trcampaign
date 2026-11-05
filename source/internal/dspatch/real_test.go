package dspatch

import (
	"os"
	"path/filepath"
	"testing"

	"nfsrun-server/internal/protocert"
)

func TestRealExe(t *testing.T) {
	src := os.Getenv("NFSTR_EXE")
	if src == "" {
		t.Skip("NFSTR_EXE not set")
	}
	dst := filepath.Join(t.TempDir(), "ds.exe")
	n, err := Ensure(src, dst, "example.org")
	if err != nil {
		t.Fatal(n, err)
	}
	if _, d, err := protocert.ExeHosts(dst); err != nil || d != "example.org" {
		t.Fatalf("Redirector %q %v", d, err)
	}
	if n, err = Ensure(src, dst, "example.org"); err != nil || len(n) != len(Patches)+1 {
		t.Fatalf("second run %q %v", n, err)
	}
}
