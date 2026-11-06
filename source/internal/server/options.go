package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// serverOptions are the owner's switches on the admin page (config/server_options.json).
// Zero values are the defaults: late join on, login codes remembered, DS_COUNT race servers.
type serverOptions struct {
	NoLateJoin   bool `json:"no_late_join"`  // true: players cannot join a race that has started
	ForgetLogins bool `json:"forget_logins"` // true: game login codes are one-time and forgotten at restart
	MaxServers   int  `json:"max_servers"`   // race servers at the same time; 0 = DS_COUNT
}

type optionStore struct {
	mu   sync.Mutex
	set  serverOptions
	read bool
}

func (s *Server) optionsPath() string {
	return filepath.Join(filepath.Dir(s.cfg.RaceSettingsFile), "server_options.json")
}

func (s *Server) options() serverOptions {
	s.opts.mu.Lock()
	defer s.opts.mu.Unlock()
	if !s.opts.read {
		s.opts.read = true
		if b, err := os.ReadFile(s.optionsPath()); err == nil {
			_ = json.Unmarshal(b, &s.opts.set)
		}
	}
	return s.opts.set
}

func (s *Server) saveOptions(o serverOptions) error {
	s.options()
	s.opts.mu.Lock()
	defer s.opts.mu.Unlock()
	s.opts.set = o
	b, _ := json.MarshalIndent(o, "", "  ")
	if err := os.MkdirAll(filepath.Dir(s.optionsPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.optionsPath(), append(b, '\n'), 0o644)
}

// dsLimit: how many race servers may run at the same time.
func (s *Server) dsLimit() int {
	n := s.options().MaxServers
	if n <= 0 {
		n = s.cfg.DSCount
	}
	return max(1, min(n, 32))
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// cmdOption: latejoin | logins | servers
func (s *Server) cmdOption(name string, args []string) CommandResult {
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	o := s.options()
	flag := func(cur bool, label string) (bool, CommandResult, bool) {
		if len(args) == 0 {
			return cur, say("%s: %s", label, onOff(cur)), false
		}
		switch strings.ToLower(args[0]) {
		case "on", "1", "true", "yes":
			return true, CommandResult{}, true
		case "off", "0", "false", "no":
			return false, CommandResult{}, true
		}
		return cur, say("%s: on or off", label), false
	}
	switch name {
	case "latejoin":
		v, r, ok := flag(!o.NoLateJoin, "players can join a race that has started")
		if !ok {
			return r
		}
		o.NoLateJoin = !v
		if err := s.saveOptions(o); err != nil {
			return say("cannot save: %v", err)
		}
		return say("joining a race that has started: %s (applies to the next Quick Match)", onOff(v))
	case "logins":
		v, r, ok := flag(!o.ForgetLogins, "remember game login codes")
		if !ok {
			return r
		}
		o.ForgetLogins = !v
		if err := s.saveOptions(o); err != nil {
			return say("cannot save: %v", err)
		}
		return say("remembering game login codes: %s (applies to codes issued from now on)", onOff(v))
	default: // servers
		if len(args) == 0 {
			return say("race servers at the same time: %d (DS_COUNT %d; 0 = DS_COUNT)", s.dsLimit(), s.cfg.DSCount)
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 || n > 32 {
			return say("servers <0-32>   0 = DS_COUNT (%d)", s.cfg.DSCount)
		}
		o.MaxServers = n
		if err := s.saveOptions(o); err != nil {
			return say("cannot save: %v", err)
		}
		return say("race servers at the same time: %d (more start on demand; fewer once the extra ones are idle)", s.dsLimit())
	}
}
