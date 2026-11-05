package server

import (
	"os"
	"path/filepath"
	"sync"

	"nfsrun-server/internal/config"
)

// testConfig: the protocol fixtures log in by name, which production
// never allows.
func testConfig() config.Config {
	allowNameLogin = true
	c := config.Default()
	c.AuthMode = "open"
	// Never write data\ (playlists.txt, legal\, news.json) into the
	// package folder; a playlists.txt left there made the Quick Match default
	// playlist random in TestDedicatedServerFlow.
	c.LockerDir = filepath.Join(testDataRoot(), "locker")
	c.LauncherHTTPSListen = "127.0.0.1:0"
	c.LauncherTLSCert = filepath.Join(testDataRoot(), "certs", "launcher.crt")
	c.LauncherTLSKey = filepath.Join(testDataRoot(), "certs", "launcher.key")
	return c
}

var (
	testDataOnce sync.Once
	testDataDir  string
)

func testDataRoot() string {
	testDataOnce.Do(func() {
		d, err := os.MkdirTemp("", "nfsrun-test-data-")
		if err != nil {
			panic(err)
		}
		testDataDir = d
	})
	return testDataDir
}
