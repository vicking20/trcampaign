package server

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// banList: banned player names (case-insensitive), kept in cfg.BansFile, one per line.
type banList struct {
	mu    sync.Mutex
	names map[string]string // lower case -> name as entered
	path  string
	read  bool
}

func (b *banList) load(path string) {
	if b.read && b.path == path {
		return
	}
	b.path, b.read, b.names = path, true, map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n := strings.TrimSpace(sc.Text()); n != "" && !strings.HasPrefix(n, "#") {
			b.names[strings.ToLower(n)] = n
		}
	}
}

func (b *banList) save() error {
	var list []string
	for _, n := range b.names {
		list = append(list, n)
	}
	sort.Strings(list)
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(b.path, []byte(strings.Join(list, "\n")+"\n"), 0o644)
}

func (s *Server) isBanned(name string) bool {
	s.bans.mu.Lock()
	defer s.bans.mu.Unlock()
	s.bans.load(s.cfg.BansFile)
	_, ok := s.bans.names[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// setBan adds or removes a ban; it reports whether anything changed.
func (s *Server) setBan(name string, banned bool) (bool, error) {
	s.bans.mu.Lock()
	defer s.bans.mu.Unlock()
	s.bans.load(s.cfg.BansFile)
	key := strings.ToLower(strings.TrimSpace(name))
	_, had := s.bans.names[key]
	if banned == had {
		return false, nil
	}
	if banned {
		s.bans.names[key] = strings.TrimSpace(name)
	} else {
		delete(s.bans.names, key)
	}
	return true, s.bans.save()
}

func (s *Server) banned() []string {
	s.bans.mu.Lock()
	defer s.bans.mu.Unlock()
	s.bans.load(s.cfg.BansFile)
	var list []string
	for _, n := range s.bans.names {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i]) < strings.ToLower(list[j]) })
	return list
}
