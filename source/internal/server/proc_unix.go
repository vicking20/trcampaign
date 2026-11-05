//go:build !windows

package server

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcGroup puts a race server in its own process group, so stopping it also ends the
// launcher's children (a Proton wrapper starts several processes).
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killRaceServer(cmd) }
}

func killGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// killRaceServer ends a race server completely: the launcher's process group and, because Proton starts
// the game outside that group, every process whose command line carries the server's name argument
// (NFSTR_DS_<n>_port<p>), so its Blaze connection drops at once.
func killRaceServer(cmd *exec.Cmd) error {
	err := killGroup(cmd)
	for _, a := range cmd.Args {
		if strings.HasPrefix(a, "NFSTR_DS_") {
			killMatching(a)
		}
	}
	return err
}

func killMatching(marker string) {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err == nil && strings.Contains(strings.ReplaceAll(string(b), "\x00", " "), marker) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
