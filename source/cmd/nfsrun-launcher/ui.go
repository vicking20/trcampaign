package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"nfsrun-server/internal/lsx"
)

// launcherUI: what launch() shows the player. Windows gets the
// launcher window (gui_windows.go); -console and other systems get a text
// console.
type launcherUI interface {
	// Run runs fn (the launcher) and returns when the launcher is done: the
	// window was closed, or fn returned.
	Run(fn func())
	LogWriter() io.Writer
	Console() bool

	SetPlayer(persona string)
	SetServer(state serverState)
	SetLocal(text string)
	SetStatus(kind statusKind, text, detail string)
	SetFriends(list []lsx.Friend)
	SetPlayable(on bool)
	SetGame(running bool)

	// WaitPlay blocks until the player asks to start the game; false = the
	// launcher is closing.
	WaitPlay() bool
	// WaitClose blocks until the launcher is closed.
	WaitClose()
	// Fatal shows an error that stops the launcher and waits until it is closed.
	Fatal(text, detail string)
	// AskInvite asks whether to accept a friend's invite (blocks).
	AskInvite(from string) bool
	// Confirm asks a yes/no question (blocks).
	Confirm(title, text string) bool
}

type statusKind int

const (
	statusBusy statusKind = iota
	statusOK
	statusError
)

type serverState int

const (
	serverNone serverState = iota
	serverConnecting
	serverOnline
	serverFailed
)

// consoleUI: the text window. It starts the game once and ends with it.
type consoleUI struct{ played bool }

func (c *consoleUI) Run(fn func())                        { fn() }
func (c *consoleUI) LogWriter() io.Writer                 { return os.Stdout }
func (c *consoleUI) Console() bool                        { return true }
func (c *consoleUI) SetPlayer(string)                     {}
func (c *consoleUI) SetServer(serverState)                {}
func (c *consoleUI) SetLocal(string)                      {}
func (c *consoleUI) SetStatus(statusKind, string, string) {}
func (c *consoleUI) SetFriends([]lsx.Friend)              {}
func (c *consoleUI) SetPlayable(bool)                     {}
func (c *consoleUI) SetGame(bool)                         {}
func (c *consoleUI) WaitClose()                           { select {} }
func (c *consoleUI) AskInvite(string) bool                { return true }

func (c *consoleUI) Confirm(title, text string) bool {
	fmt.Printf("\n%s\n%s (y/n) ", title, text)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes" || a == "s" || a == "sim"
}

func (c *consoleUI) WaitPlay() bool {
	if c.played {
		return false
	}
	c.played = true
	return true
}

func (c *consoleUI) Fatal(text, detail string) {
	fmt.Println()
	fmt.Println(text)
	if detail != "" {
		fmt.Println(detail)
	}
	pause()
}

func pause() {
	fmt.Println("\nPress Enter to close.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
