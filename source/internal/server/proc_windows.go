//go:build windows

package server

import "os/exec"

func setProcGroup(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func killRaceServer(cmd *exec.Cmd) error { return killGroup(cmd) }
