//go:build !windows

package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// systemLocale: LANG (pt_BR.UTF-8, ...), for LOCALE=auto.
func systemLocale() string {
	l, _, _ := strings.Cut(os.Getenv("LANG"), ".")
	return l
}

func newUI(console bool) launcherUI { return &consoleUI{} }

func showError(msg string) {
	fmt.Println(msg)
	time.Sleep(3 * time.Second)
}

func trackGame(pid int) error { return nil }

func killGames() {}
