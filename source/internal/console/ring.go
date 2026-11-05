// Package console is the server window: a compact dashboard
// instead of the full log, commands typed in the window, and a stop file.
package console

import (
	"strings"
	"sync"
)

// EventRing is an io.Writer for the server logger: it keeps the last
// interesting lines (logins, matchmaking, race servers, games, groups,
// invites, errors) for the dashboard. Everything still goes to the log file.
type EventRing struct {
	mu     sync.Mutex
	lines  []string
	max    int
	seq    uint64
	tail   []string // last raw lines (log view)
	notify chan struct{}
}

func NewEventRing(max int) *EventRing {
	return &EventRing{max: max, notify: make(chan struct{}, 1)}
}

var interesting = []string{
	"[AUTH] loginPersona persona=", "[AUTH] login error", "[AUTH] loginPersona error",
	"[DS] ", "[DSMGR]", "[MM] ", "[GAME] ", "[PG] ", "invite", "[STREAM] ",
	"[ID] refused", "[ADMIN] > ", "shutting down", "server stopped", "starting",
}
var boring = []string{"handler=", " TDF", "[HTTP]", "[WEB]", "TX ", "RX ", "[DSMGR] server 1 console"}

func keep(line string) bool {
	low := strings.ToLower(line)
	if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "warning") || strings.Contains(low, "refused") {
		return !strings.Contains(line, "TX ") && !strings.Contains(line, "RX ")
	}
	for _, b := range boring {
		if strings.Contains(line, b) {
			return false
		}
	}
	for _, k := range interesting {
		if strings.Contains(line, k) {
			return true
		}
	}
	return false
}

// Write takes one log.Printf output (may hold several lines: TDF dumps).
func (r *EventRing) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\r\n")
	first := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		first = text[:i]
	}
	// log prefix "2026/10/12 12:48:43.241519 " -> keep the time only
	msg := first
	if len(msg) > 27 && msg[4] == '/' && msg[10] == ' ' {
		msg = msg[11:19] + " " + msg[27:]
	}
	r.mu.Lock()
	r.tail = append(r.tail, msg)
	if len(r.tail) > 200 {
		r.tail = r.tail[len(r.tail)-200:]
	}
	important := keep(first)
	if important {
		r.lines = append(r.lines, msg)
		if len(r.lines) > r.max {
			r.lines = r.lines[len(r.lines)-r.max:]
		}
		r.seq++
	}
	r.mu.Unlock()
	if important {
		select {
		case r.notify <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

func (r *EventRing) Recent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func (r *EventRing) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tail) > n {
		return append([]string(nil), r.tail[len(r.tail)-n:]...)
	}
	return append([]string(nil), r.tail...)
}

// Changed fires when an interesting line arrived.
func (r *EventRing) Changed() <-chan struct{} { return r.notify }
