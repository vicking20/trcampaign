package console

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/server"
)

// Options for the window.
type Options struct {
	Public   string // host shown in the header
	LogPath  string
	StopFile string        // run\stop.request: stop_server.bat creates it
	Refresh  time.Duration // full redraw ("clean-up") period
	Out      io.Writer
	In       io.Reader // keys (default os.Stdin)
	// Command runs a server-owner command typed after C (server.Command).
	Command func(line string, confirmed bool) server.CommandResult
	// Help lists the commands for H.
	Help []string
}

var (
	restoreMu sync.Mutex
	restoreFn func()
)

// Restore puts the console back the way Run found it (echo, line input,
// cursor). Safe to call more than once and when Run never ran.
func Restore() {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if restoreFn != nil {
		restoreFn()
		restoreFn = nil
	}
}

// Run draws the dashboard until stop is called or done closes. Keys: Q (then
// Y) stops the server, L toggles the log view, H shows help, C (or /) opens
// the command line. stop is also called when the stop file appears.
func Run(stats func() server.Stats, ring *EventRing, o Options, stop func(reason string), done <-chan struct{}) {
	restore := func() {}
	raw := o.In != nil // a given reader delivers keys (tests)
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.Out == nil {
		o.Out = os.Stdout
		restoreInput, ok := rawInput()
		raw = raw || ok
		_, _ = io.WriteString(o.Out, "\x1b[?25l") // hide the cursor
		restore = func() {
			restoreInput()
			_, _ = io.WriteString(os.Stdout, "\x1b[?25h")
		}
	}
	restoreMu.Lock()
	restoreFn = restore
	restoreMu.Unlock()
	defer Restore()
	if o.Refresh <= 0 {
		o.Refresh = 15 * time.Second
	}
	vt := enableVT()
	var mu sync.Mutex
	logView := false
	note := ""
	cmdMode := false     // typing a command after C
	var cmdBuf []rune    // what was typed
	var outView []string // a command's output (any key goes back)
	outTitle := ""
	prompt := func() string { return "  > " + string(cmdBuf) }
	draw := func() {
		mu.Lock()
		defer mu.Unlock()
		var b strings.Builder
		if vt {
			b.WriteString("\x1b[H\x1b[2J\x1b[3J")
		} else {
			b.WriteString(strings.Repeat("\r\n", 3))
		}
		if outView != nil {
			fmt.Fprintf(&b, " %s\r\n\r\n", outTitle)
			lines := outView
			if len(lines) > 44 {
				lines = append(append([]string{}, lines[:43]...), fmt.Sprintf("... %d more lines in %s", len(outView)-43, o.LogPath))
			}
			for _, l := range lines {
				b.WriteString("  " + cut(l, 150) + "\r\n")
			}
			b.WriteString("\r\n  any key: back   C: another command\r\n")
			_, _ = io.WriteString(o.Out, b.String())
			return
		}
		if logView {
			fmt.Fprintf(&b, " LOG VIEW (last lines of %s)  -  press L to go back\r\n\r\n", o.LogPath)
			for _, l := range ring.Tail(40) {
				b.WriteString(" " + cut(l, 150) + "\r\n")
			}
			_, _ = io.WriteString(o.Out, b.String())
			return
		}
		st := stats()
		setTitle(fmt.Sprintf("NFS The Run Server - %d online, %d racing", len(st.PlayersOnline), len(st.InRace)))
		line := strings.Repeat("=", 78)
		fmt.Fprintf(&b, " %s\r\n", line)
		fmt.Fprintf(&b, "  NFS The Run Private Server   up %-10s  %s\r\n", dur(st.Uptime), time.Now().Format("15:04:05"))
		fmt.Fprintf(&b, "  %s\r\n", o.Public)
		fmt.Fprintf(&b, " %s\r\n\r\n", line)
		fmt.Fprintf(&b, "  Players online : %-3d %s\r\n", len(st.PlayersOnline), cut(server.PlayerList(st.PlayersOnline, 8), 56))
		fmt.Fprintf(&b, "  Racing         : %-3d %s\r\n", len(st.InRace), cut(server.PlayerList(st.InRace, 8), 56))
		fmt.Fprintf(&b, "  In lobby       : %-3d %s\r\n", len(st.InLobby), cut(server.PlayerList(st.InLobby, 8), 56))
		fmt.Fprintf(&b, "  Matchmaking    : %-3d %s\r\n", len(st.Matchmaking), cut(server.PlayerList(st.Matchmaking, 8), 56))
		fmt.Fprintf(&b, "  Groups %d   Live launchers %d   Connections %d\r\n\r\n", st.Groups, st.LiveLaunchers, st.Connections)
		b.WriteString("  RACE SERVERS\r\n")
		if len(st.RaceServers) == 0 {
			b.WriteString("    (none managed: DS_ENABLED=false)\r\n")
		}
		for _, r := range st.RaceServers {
			up := ""
			if r.Uptime > 0 {
				up = "up " + dur(r.Uptime)
			}
			fmt.Fprintf(&b, "    #%d  UDP %-5d %-8s pid %-6d %-10s restarts %d\r\n", r.N, r.Port, r.State, r.PID, up, r.Restarts)
		}
		b.WriteString("\r\n  GAMES\r\n")
		if len(st.ServerGames) == 0 {
			b.WriteString("    (none)\r\n")
		}
		for i, g := range st.ServerGames {
			if i == 6 {
				fmt.Fprintf(&b, "    ... %d more\r\n", len(st.ServerGames)-6)
				break
			}
			kind := "peer"
			if g.Dedicated {
				kind = "race server"
			}
			fmt.Fprintf(&b, "    game %-4d %-12s %-11s %-10s %d player(s) %s\r\n", g.ID, g.State, kind, cut(g.Host, 10), len(g.Players), cut(server.PlayerList(g.Players, 4), 30))
		}
		b.WriteString("\r\n  RECENT\r\n")
		recent := ring.Recent()
		if len(recent) == 0 {
			b.WriteString("    (nothing yet)\r\n")
		}
		for _, l := range recent {
			b.WriteString("    " + cut(l, 112) + "\r\n")
		}
		fmt.Fprintf(&b, "\r\n  Full log: %s\r\n", o.LogPath)
		if note != "" {
			fmt.Fprintf(&b, "  %s\r\n", cut(note, 150))
		}
		if cmdMode {
			b.WriteString("  Command (Enter = run, Esc = cancel, help = list):\r\n")
			b.WriteString(prompt())
			_, _ = io.WriteString(o.Out, b.String()+"\x1b[?25h")
			return
		}
		b.WriteString("  [C] command   [Q] stop the server   [L] log view   [H] help\r\n")
		_, _ = io.WriteString(o.Out, b.String()+"\x1b[?25l")
	}
	// echo: rewrite only the prompt line while typing (no full redraw)
	echo := func() {
		if !vt {
			draw()
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = io.WriteString(o.Out, "\r\x1b[2K"+prompt())
	}
	confirmLine := "" // a command waiting for Y
	// runCommand runs a typed line and shows its result.
	runCommand := func(line string, confirmed bool) {
		if o.Command == nil {
			mu.Lock()
			note = "commands are not available"
			mu.Unlock()
			return
		}
		res := o.Command(line, confirmed)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case res.Confirm != "":
			confirmLine, note = line, res.Confirm+"  Y = yes, any other key = no"
		case len(res.Lines) > 1:
			outView, outTitle, note = res.Lines, "> "+strings.TrimSpace(line), ""
		case len(res.Lines) == 1:
			note = res.Lines[0]
		default:
			note = ""
		}
	}

	// keys; anything else is ignored. Without a raw console (not Windows, or
	// input that is not a console) keys arrive as lines: each line is a key
	// (q, y, l, h) or a command.
	go func() {
		in := bufio.NewReader(o.In)
		if !raw && o.Command != nil {
			back := func() { mu.Lock(); outView = nil; mu.Unlock() }
			lineInput(in, &mu, &note, &confirmLine, runCommand, back, draw, stop)
			return
		}
		confirm := false
		for {
			r, _, err := in.ReadRune()
			if err != nil {
				return
			}
			mu.Lock()
			if cmdMode {
				switch {
				case r == '\r' || r == '\n':
					line := strings.TrimSpace(string(cmdBuf))
					cmdMode, cmdBuf = false, nil
					mu.Unlock()
					if line != "" {
						runCommand(line, false)
					}
					draw()
				case r == 0x1b: // Esc
					cmdMode, cmdBuf, note = false, nil, ""
					mu.Unlock()
					draw()
				case r == 0x08 || r == 0x7f: // Backspace
					if len(cmdBuf) > 0 {
						cmdBuf = cmdBuf[:len(cmdBuf)-1]
					}
					mu.Unlock()
					echo()
				case r >= 0x20 && len(cmdBuf) < 200:
					cmdBuf = append(cmdBuf, r)
					mu.Unlock()
					echo()
				default:
					mu.Unlock()
				}
				continue
			}
			if r == '\r' || r == '\n' || r == ' ' {
				mu.Unlock()
				continue
			}
			key := r | 0x20 // lower case (ASCII)
			if confirmLine != "" {
				line := confirmLine
				confirmLine = ""
				if key == 'y' {
					note = "..."
					mu.Unlock()
					runCommand(line, true)
				} else {
					note = "Cancelled."
					mu.Unlock()
				}
				draw()
				continue
			}
			if outView != nil {
				outView = nil
				if key == 'c' || r == '/' {
					cmdMode, cmdBuf, note = true, nil, ""
				}
				mu.Unlock()
				draw()
				continue
			}
			switch {
			case confirm && key == 'y':
				note = "Stopping..."
				mu.Unlock()
				draw()
				stop("Q pressed in the server window")
				return
			case confirm:
				confirm, note = false, "Stop cancelled."
			case key == 'q':
				confirm, note = true, "Stop the server, race servers, gateway and database?  Y = yes, any other key = no"
			case key == 'c' || r == '/' || r == ':':
				cmdMode, cmdBuf, note = true, nil, ""
			case key == 'l':
				logView, note = !logView, ""
			case key == 'h' || r == '?':
				outTitle = "HELP"
				outView = append([]string{
					"Keys:  C or /  command line      Q then Y  stop everything (same as stop_server.bat)",
					"       L  log view                H  this help",
					"Full log: " + o.LogPath, "", "Commands:",
				}, o.Help...)
				note = ""
			default:
				mu.Unlock()
				continue
			}
			mu.Unlock()
			draw()
		}
	}()

	draw()
	full := time.NewTicker(o.Refresh)
	defer full.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	last := time.Now()
	pending := false
	for {
		select {
		case <-done:
			return
		case <-full.C:
			draw()
			last, pending = time.Now(), false
		case <-ring.Changed():
			pending = true
		case <-poll.C:
			if o.StopFile != "" {
				if _, err := os.Stat(o.StopFile); err == nil {
					_ = os.Remove(o.StopFile)
					stop("stop_server.bat (" + filepath.Base(o.StopFile) + ")")
				}
			}
			mu.Lock()
			lv := logView
			mu.Unlock()
			// news shows at once, at most every 2 s; the log view follows live
			if (pending && time.Since(last) >= 2*time.Second) || lv {
				draw()
				last, pending = time.Now(), false
			}
		}
	}
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "~"
}

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm%02ds", m, sec)
}

// lineInput: keys and commands as whole lines (no raw console).
func lineInput(in *bufio.Reader, mu *sync.Mutex, note, confirmLine *string, run func(string, bool), back, draw func(), stop func(string)) {
	stopAsked := false
	for {
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		back() // an empty line (Enter) goes back from a command's output
		if line == "" && err == nil {
			draw()
		}
		if line != "" {
			low := strings.ToLower(line)
			mu.Lock()
			pending := *confirmLine
			*confirmLine = ""
			mu.Unlock()
			switch {
			case pending != "":
				if low == "y" || low == "yes" {
					run(pending, true)
				} else {
					mu.Lock()
					*note = "Cancelled."
					mu.Unlock()
				}
			case stopAsked && (low == "y" || low == "yes"):
				stop("Q typed in the server window")
				return
			case low == "q" || low == "quit" || low == "stop":
				stopAsked = true
				mu.Lock()
				*note = "Stop the server, race servers, gateway and database?  y = yes"
				mu.Unlock()
			default:
				stopAsked = false
				run(strings.TrimPrefix(line, "/"), false)
			}
			draw()
		}
		if err != nil {
			return
		}
	}
}

// CommandLoop reads commands from r, one per line (CONSOLE_MODE=log): the
// results are in the log ([ADMIN] lines); questions go to w.
func CommandLoop(r io.Reader, w io.Writer, run func(line string, confirmed bool) server.CommandResult) {
	in := bufio.NewReader(r)
	pending := ""
	for {
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case pending != "":
			if l := strings.ToLower(line); l == "y" || l == "yes" {
				run(pending, true)
			} else {
				fmt.Fprintln(w, "Cancelled.")
			}
			pending = ""
		default:
			if res := run(strings.TrimPrefix(line, "/"), false); res.Confirm != "" {
				pending = line
				fmt.Fprintln(w, res.Confirm+"  (y/n)")
			}
		}
		if err != nil {
			return
		}
	}
}
