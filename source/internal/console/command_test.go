package console

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"nfsrun-server/internal/server"
)

func TestCommandLine(t *testing.T) {
	ring := NewEventRing(5)
	var out syncBuf
	inR, inW := io.Pipe()
	done := make(chan struct{})
	var mu sync.Mutex
	var calls []string
	cmd := func(line string, confirmed bool) server.CommandResult {
		mu.Lock()
		calls = append(calls, line+map[bool]string{true: " [yes]", false: ""}[confirmed])
		mu.Unlock()
		switch {
		case strings.HasPrefix(line, "delete-player") && !confirmed:
			return server.CommandResult{Confirm: "DELETE Ze?"}
		case strings.HasPrefix(line, "delete-player"):
			return server.CommandResult{Lines: []string{"Ze deleted"}}
		}
		return server.CommandResult{Lines: []string{"2 online:", "  Ze", "  Bia"}}
	}
	go Run(func() server.Stats { return server.Stats{} }, ring, Options{LogPath: "logs/server.log", Refresh: time.Hour, Out: &out, In: inR, Command: cmd, Help: []string{"players  who is online"}}, func(string) {}, done)
	defer close(done)
	send := func(s string) { inW.Write([]byte(s)); time.Sleep(80 * time.Millisecond) }
	wait := func(want string) {
		t.Helper()
		for i := 0; i < 40; i++ {
			if strings.Contains(out.String(), want) {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("missing %q in\n%s", want, out.String())
	}
	time.Sleep(100 * time.Millisecond)
	send("c")
	wait("Command (Enter = run")
	send("playerz")
	send("\x08s\r") // backspace fixes the typo
	wait("> players")
	wait("Bia")
	send("x") // any key: back
	send("/delete-player Ze\r")
	wait("DELETE Ze?  Y = yes")
	send("y")
	wait("Ze deleted")
	send("c")
	send("kick Bia\x1b") // Esc: nothing runs
	send("h")
	wait("players  who is online")
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(calls, "|") != "players|delete-player Ze|delete-player Ze [yes]" {
		t.Fatalf("calls %q", calls)
	}
}

func TestCommandLoop(t *testing.T) {
	var got []string
	var out strings.Builder
	CommandLoop(strings.NewReader("players\ndelete-player Ze\nn\ndelete-player Ze\ny\n"), &out, func(line string, ok bool) server.CommandResult {
		got = append(got, line+map[bool]string{true: "!", false: ""}[ok])
		if strings.HasPrefix(line, "delete") && !ok {
			return server.CommandResult{Confirm: "sure?"}
		}
		return server.CommandResult{}
	})
	if strings.Join(got, ",") != "players,delete-player Ze,delete-player Ze,delete-player Ze!" || !strings.Contains(out.String(), "Cancelled.") {
		t.Fatalf("%q %q", got, out.String())
	}
}
