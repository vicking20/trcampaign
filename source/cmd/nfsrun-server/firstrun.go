package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// firstRun writes config/server.env when there is none, with this machine's
// address and a random admin password, and tells the owner once.
func firstRun(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	ip := localIP()
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	pw := hex.EncodeToString(b)
	launcher := ""
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(filepath.Join("tools-linux", "ds_launch.sh")); err == nil {
			launcher = "DS_LAUNCHER=tools-linux/ds_launch.sh\n"
		}
	}
	cfg := fmt.Sprintf(`# Created on the first start. Docs: docs/CONFIGURATION.md
PUBLIC_HOST=%[1]s
PUBLIC_IP=%[1]s
BASE_DOMAIN=nfsrun.test
AUTH_MODE=open

# Race servers: the game is found one folder up (this server in <game folder>\NFSTR_Server).
# DS_GAME_DIR=C:\Games\NFS The Run
DS_KEEP_HOSTS=true
DS_COUNT=2
DS_PUBLIC_IP=%[1]s
%[3]s
# Admin page: http://%[1]s:8090  (keep this file private)
ADMIN_WEB_LISTEN=0.0.0.0:8090
ADMIN_WEB_USER=admin
ADMIN_WEB_PASSWORD=%[2]s
`, ip, pw, launcher)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "could not create", path+":", err)
		return
	}
	fmt.Printf("\nFirst start: created %s\n  address  %s (change PUBLIC_HOST / PUBLIC_IP there if players join from elsewhere)\n  admin    http://%s:8090   user admin   password %s\n\n", path, ip, ip, pw)
}

// localIP: the address of the interface that routes to the internet (no packet is sent).
func localIP() string {
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			return a.IP.String()
		}
	}
	return "127.0.0.1"
}
