package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.log")
	r, err := openRotating(p, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		r.Write([]byte(strings.Repeat("x", 30) + "\n"))
	}
	r.Close()
	st, _ := os.Stat(p)
	if st.Size() > 100 {
		t.Fatalf("size %d", st.Size())
	}
	old, _ := globOld(p)
	if len(old) != 1 {
		t.Fatalf("rotated copies %v", old)
	}
}
