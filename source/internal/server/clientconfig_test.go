package server

import (
	"os"
	"path/filepath"
	"testing"

	"nfsrun-server/internal/blaze"
)

func TestClientConfigFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "NFS12_THERUN.cfg"), []byte("\uFEFF# comment\r\nautologUrl = http://autolog.example:18080/\r\nempty=\r\nbad line\r\n"), 0644)
	conf, _, err := loadClientConfig(dir, "NFS12_THERUN")
	if err != nil || conf["autologUrl"] != "http://autolog.example:18080/" || len(conf) != 2 {
		t.Fatalf("conf=%v err=%v", conf, err)
	}
	fs, err := blaze.DecodeAll(buildFetchConfigResponse(conf))
	if err != nil || len(fs) != 1 || fs[0].Tag != "CONF" {
		t.Fatalf("fs=%v err=%v", fs, err)
	}
	if c, _, _ := loadClientConfig(dir, "MISSING"); c != nil {
		t.Fatal("missing section must be nil")
	}
	if c, _, _ := loadClientConfig(dir, "../../etc/passwd"); c != nil {
		t.Fatal("path traversal must be rejected")
	}
}
