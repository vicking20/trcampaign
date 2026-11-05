package server

import (
	"sync"
	"sync/atomic"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

var fakePlayerSeq atomic.Uint64

// sayInGame shows a short text to every player in a game, using the game's own "player joined / player left" notices:
// a made-up player whose name is the text joins and leaves a few seconds later. Nothing is stored, the race server is
// not told, and the player is never in the game's roster. Experimental: how the client shows the text is not known.
// reason is the "player removed" reason the leave notice carries (the client may word the notice by it). Returns how many players it reached.
func (s *Server) sayInGame(text string, reason uint64) int {
	if r := []rune(text); len(r) > 32 {
		text = string(r[:32])
	}
	pid := 0x7E000000 + fakePlayerSeq.Add(1)
	reached := 0
	type target struct {
		g    *game
		to   []*connState
		gone func(w *blaze.Writer)
	}
	var targets []target
	s.gm.mu.Lock()
	for _, g := range s.gm.games {
		fp := &gamePlayer{
			state:    &connState{Persona: database.Persona{Name: text, BlazeID: pid}},
			slot:     g.freeSlot(),
			status:   playerActiveConnected,
			joinedAt: time.Now(),
		}
		payload := g.buildNotifyPlayerJoining(fp)
		var to []*connState
		for _, p := range g.players {
			if !p.held {
				to = append(to, p.state)
				s.gm.out = append(s.gm.out, outMsg{p.state, notifyPacket(ComponentGameManager, GMNotifyPlayerJoining, payload)})
			}
		}
		if len(to) == 0 {
			continue
		}
		reached += len(to)
		gid := uint64(g.id)
		targets = append(targets, target{g, to, func(w *blaze.Writer) {
			w.TagVar("CNTX", 0)
			w.TagVar("GID", gid)
			w.TagVar("PID", pid)
			w.TagVar("REAS", reason)
		}})
	}
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	s.deliver(out, nil)
	if len(targets) > 0 {
		time.AfterFunc(4*time.Second, func() {
			var msgs []outMsg
			for _, t := range targets {
				var w blaze.Writer
				t.gone(&w)
				for _, st := range t.to {
					msgs = append(msgs, outMsg{st, notifyPacket(ComponentGameManager, GMNotifyPlayerRemoved, w.Bytes())})
				}
			}
			s.deliver(msgs, nil)
		})
	}
	return reached
}

// say jobs: a text can be repeated a few times at an interval; "say stop" cancels what is pending.
var sayJobs struct {
	sync.Mutex
	gen   uint64 // bumped by cancel: running jobs notice and stop
	count int    // jobs still running
}

// sayRepeat shows text now and again every `every` until it was shown `count` times in all.
// It returns how many players the first showing reached.
func (s *Server) sayRepeat(text string, reason uint64, count int, every time.Duration) int {
	reached := s.sayInGame(text, reason)
	if count <= 1 {
		return reached
	}
	sayJobs.Lock()
	gen := sayJobs.gen
	sayJobs.count++
	sayJobs.Unlock()
	go func() {
		defer func() { sayJobs.Lock(); sayJobs.count--; sayJobs.Unlock() }()
		for i := 1; i < count; i++ {
			select {
			case <-s.done:
				return
			case <-time.After(every):
			}
			sayJobs.Lock()
			cancelled := sayJobs.gen != gen
			sayJobs.Unlock()
			if cancelled {
				return
			}
			s.sayInGame(text, reason)
		}
	}()
	return reached
}

// saysPending: how many repeating announcements are still running; sayCancel stops them.
func saysPending() int { sayJobs.Lock(); defer sayJobs.Unlock(); return sayJobs.count }
func sayCancel() int {
	sayJobs.Lock()
	defer sayJobs.Unlock()
	sayJobs.gen++
	return sayJobs.count
}
