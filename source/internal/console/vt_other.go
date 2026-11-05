//go:build !windows

package console

import (
	"fmt"
	"os"
)

func enableVT() bool { return true }

func setTitle(t string) { fmt.Fprintf(os.Stdout, "\x1b]0;%s\x07", t) }

// IsConsole: stdout is a terminal.
func IsConsole() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
