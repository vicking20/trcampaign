package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// RaceSettings are the owner's settings for the race servers, edited on the admin page. -1 = the game's own value.
// They reach a race server as keys in the plugin ini (Scripts\NFSTR_Online.ini [Patch]), read when it starts.
type RaceSettings struct {
	TrafficDensity float64           `json:"traffic_density"` // 0..3 (1 = the game's own maximum), -1 = stock
	TrafficCars    int               `json:"traffic_cars"`    // 0..50 (the game stops at 25 by itself), -1 = stock
	ForcedCars     []string          `json:"forced_cars"`     // car hashes (8 hex digits) every player gets, one at random per player; empty = their own
	CarNames       map[string]string `json:"car_names"`       // hash -> label the owner gave it
	Mode           string            `json:"mode"`            // the mode last applied ("" = custom settings)
}

type raceStore struct {
	mu   sync.Mutex
	set  RaceSettings
	read bool
}

func (s *Server) raceLocked() *RaceSettings {
	if !s.race.read {
		s.race.read = true
		s.race.set = RaceSettings{TrafficDensity: -1, TrafficCars: -1}
		if b, err := os.ReadFile(s.cfg.RaceSettingsFile); err == nil {
			_ = json.Unmarshal(b, &s.race.set)
		}
	}
	return &s.race.set
}

func (s *Server) raceSettings() RaceSettings {
	s.race.mu.Lock()
	defer s.race.mu.Unlock()
	return *s.raceLocked()
}

func (s *Server) saveRaceSettings(r RaceSettings) error {
	s.race.mu.Lock()
	defer s.race.mu.Unlock()
	*s.raceLocked() = r
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.MkdirAll(filepath.Dir(s.cfg.RaceSettingsFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.cfg.RaceSettingsFile, append(b, '\n'), 0o644)
}

func (s *Server) cmdTraffic(args []string) CommandResult {
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	cur := s.raceSettings()
	show := func(r RaceSettings) string {
		if r.TrafficDensity < 0 && r.TrafficCars < 0 {
			return "traffic: the game's own"
		}
		d, c := "game's own", "game's own"
		if r.TrafficDensity >= 0 {
			d = strconv.FormatFloat(r.TrafficDensity, 'f', -1, 64)
		}
		if r.TrafficCars >= 0 {
			c = strconv.Itoa(r.TrafficCars)
		}
		return fmt.Sprintf("traffic: density %s, car limit %s", d, c)
	}
	if len(args) == 0 {
		return say("%s (applies to the next race server that starts)", show(cur))
	}
	next := cur
	if strings.EqualFold(args[0], "off") {
		next.TrafficDensity, next.TrafficCars = -1, -1
	} else {
		d, err := strconv.ParseFloat(args[0], 64)
		if err != nil || d < 0 || d > 3 {
			return say("usage: traffic off | traffic <density 0-3> [<cars 0-50>]")
		}
		next.TrafficDensity = d
		if len(args) > 1 {
			c, err := strconv.Atoi(args[1])
			if err != nil || c < 0 || c > 50 {
				return say("the car limit is 0 to 50")
			}
			next.TrafficCars = c
		}
	}
	next.Mode = ""
	if err := s.saveRaceSettings(next); err != nil {
		return say("could not save: %v", err)
	}
	return say("%s (applies to the next race server that starts; stop the running one to apply it now)", show(next))
}

// writeRaceIni puts the race settings into Scripts\NFSTR_Online.ini of the race server's game folder ([Patch] section;
// everything else in the file stays as it is). A key with no setting is removed.
func (s *Server) writeRaceIni(gameDir string) error {
	r := s.raceSettings()
	keys := map[string]string{"TrafficDensity": "", "TrafficCarLimit": "", "ForceCars": strings.Join(r.ForcedCars, ",")}
	if r.TrafficDensity >= 0 {
		keys["TrafficDensity"] = strconv.FormatFloat(r.TrafficDensity, 'f', -1, 64)
	}
	if r.TrafficCars >= 0 {
		keys["TrafficCarLimit"] = strconv.Itoa(r.TrafficCars)
	}
	path := filepath.Join(gameDir, "Scripts", "NFSTR_Online.ini")
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := setIniKeys(string(old), "Patch", keys)
	if out == string(old) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// setIniKeys sets (or, for an empty value, removes) keys in one [section] of ini text, adding the section if needed.
func setIniKeys(text, section string, keys map[string]string) string {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	in, found := false, false
	done := map[string]bool{}
	flush := func() { // end of the section: append the keys it did not have
		for _, k := range sortedKeys(keys) {
			if in && !done[k] && keys[k] != "" {
				out = append(out, k+"="+keys[k])
				done[k] = true
			}
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			flush()
			in = strings.EqualFold(t[1:len(t)-1], section)
			found = found || in
			out = append(out, l)
			continue
		}
		if in {
			if k, _, ok := strings.Cut(t, "="); ok {
				if v, mine := keys[strings.TrimSpace(k)]; mine {
					done[strings.TrimSpace(k)] = true
					if v != "" {
						out = append(out, strings.TrimSpace(k)+"="+v)
					}
					continue
				}
			}
		}
		out = append(out, l)
	}
	flush()
	res := strings.Join(out, nl)
	if !found {
		var add []string
		for _, k := range sortedKeys(keys) {
			if keys[k] != "" {
				add = append(add, k+"="+keys[k])
			}
		}
		if len(add) > 0 {
			res = strings.TrimRight(res, "\r\n") + nl + nl + "[" + section + "]" + nl + strings.Join(add, nl) + nl
		}
	}
	return res
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j] < ks[j-1]; j-- {
			ks[j], ks[j-1] = ks[j-1], ks[j]
		}
	}
	return ks
}

var hexHash = func(h string) (string, bool) {
	h = strings.ToUpper(strings.TrimSpace(h))
	if len(h) != 8 {
		return "", false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return "", false
		}
	}
	return h, true
}

// seenCars: the car hashes the race server has met (Scripts\car_hashes.txt, written by the plugin).
func (s *Server) seenCars() []string {
	b, err := os.ReadFile(filepath.Join(s.cfg.DSGameDir, "Scripts", "car_hashes.txt"))
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Fields(string(b)) {
		if h, ok := hexHash(l); ok && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

type carRow struct {
	Hash   string `json:"hash"`
	Name   string `json:"name"`
	Forced bool   `json:"forced"`
}

// knownCar: a car of the game's garage table (data/cars.json: hash -> name and internal id, read out of a running race server).
type knownCar struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

var knownCache struct {
	sync.Mutex
	path string
	list []knownCar
	by   map[string]knownCar
}

func (s *Server) knownCars() ([]knownCar, map[string]knownCar) {
	path := filepath.Join(filepath.Dir(s.cfg.LockerDir), "cars.json")
	knownCache.Lock()
	defer knownCache.Unlock()
	if knownCache.path == path {
		return knownCache.list, knownCache.by
	}
	knownCache.path, knownCache.list, knownCache.by = path, []knownCar{}, map[string]knownCar{}
	var raw map[string]struct{ Name, ID string }
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &raw) == nil {
		for h, c := range raw {
			if hh, ok := hexHash(h); ok {
				k := knownCar{hh, c.Name, c.ID}
				knownCache.list = append(knownCache.list, k)
				knownCache.by[hh] = k
			}
		}
		sort.Slice(knownCache.list, func(i, j int) bool { return knownCache.list[i].Name < knownCache.list[j].Name })
	}
	return knownCache.list, knownCache.by
}

// carList: every car hash seen or named or forced, with its label.
func (s *Server) carList() []carRow {
	r := s.raceSettings()
	forced := map[string]bool{}
	for _, h := range r.ForcedCars {
		forced[h] = true
	}
	seen := map[string]bool{}
	var hashes []string
	for _, h := range append(append([]string{}, s.seenCars()...), r.ForcedCars...) {
		if !seen[h] {
			seen[h] = true
			hashes = append(hashes, h)
		}
	}
	for h := range r.CarNames {
		if !seen[h] {
			seen[h] = true
			hashes = append(hashes, h)
		}
	}
	sort.Strings(hashes)
	_, by := s.knownCars()
	rows := []carRow{}
	for _, h := range hashes {
		name := r.CarNames[h]
		if name == "" {
			name = by[h].Name
		}
		rows = append(rows, carRow{h, name, forced[h]})
	}
	return rows
}

func (s *Server) cmdCars(args []string) CommandResult {
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	cur := s.raceSettings()
	if len(args) == 0 {
		rows := s.carList()
		if len(rows) == 0 {
			return say("no cars seen yet: the list fills as players pick cars in a race server")
		}
		out := []string{"cars (hash, forced, name):"}
		for _, c := range rows {
			f := " "
			if c.Forced {
				f = "*"
			}
			out = append(out, fmt.Sprintf("  %s %s %s", c.Hash, f, c.Name))
		}
		return CommandResult{Lines: out}
	}
	switch strings.ToLower(args[0]) {
	case "force":
		var list []string
		if len(args) > 1 && !strings.EqualFold(args[1], "off") {
			for _, a := range strings.Split(strings.Join(args[1:], ","), ",") {
				if strings.TrimSpace(a) == "" {
					continue
				}
				h, ok := hexHash(a)
				if !ok {
					return say("%q is not a car hash (8 hex digits)", a)
				}
				list = append(list, h)
			}
		}
		cur.ForcedCars = list
		cur.Mode = ""
		if err := s.saveRaceSettings(cur); err != nil {
			return say("could not save: %v", err)
		}
		if len(list) == 0 {
			return say("players keep their own cars (applies to the next race server that starts)")
		}
		return say("every player gets one of %d car(s): %s (applies to the next race server that starts)", len(list), strings.Join(list, ", "))
	case "name":
		if len(args) < 2 {
			return say("usage: cars name <hash> <label>")
		}
		h, ok := hexHash(args[1])
		if !ok {
			return say("%q is not a car hash (8 hex digits)", args[1])
		}
		names := map[string]string{}
		for k, v := range cur.CarNames {
			names[k] = v
		}
		label := strings.TrimSpace(strings.Join(args[2:], " "))
		if label == "" {
			delete(names, h)
		} else {
			names[h] = label
		}
		cur.CarNames = names
		if err := s.saveRaceSettings(cur); err != nil {
			return say("could not save: %v", err)
		}
		return say("%s is called %q", h, label)
	}
	return say("usage: cars | cars force <hash,hash...|off> | cars name <hash> <label>")
}
