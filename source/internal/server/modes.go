package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// A Mode is a named bundle of race settings the owner switches between on the admin page. It sets the race servers'
// traffic and forced cars (applies from the next race server start). Built-in modes are below; the owner's own modes
// are kept in config/modes.json next to the race settings.
type Mode struct {
	Name           string   `json:"name"`
	Desc           string   `json:"desc"`
	TrafficDensity float64  `json:"traffic_density"` // -1 = the game's own
	TrafficCars    int      `json:"traffic_cars"`    // -1 = the game's own
	ForcedCars     []string `json:"forced_cars"`     // empty = players keep their own cars
	Builtin        bool     `json:"builtin,omitempty"`
}

// carYearOf: the model year (two digits) in a car id such as AST_ONE_77_10_PP_STOCK_1: the last two-digit token.
func carYearOf(id string) (int, bool) {
	year, found := 0, false
	for _, t := range strings.Split(id, "_")[1:] {
		if len(t) == 2 && t[0] >= '0' && t[0] <= '9' && t[1] >= '0' && t[1] <= '9' {
			year, _ = strconv.Atoi(t)
			found = true
		}
	}
	return year, found
}

// carsWhere lists the hashes of the known cars the filter accepts.
func (s *Server) carsWhere(ok func(k knownCar) bool) []string {
	list, _ := s.knownCars()
	var out []string
	for _, k := range list {
		if ok(k) {
			out = append(out, k.Hash)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) builtinModes() []Mode {
	stock := s.carsWhere(func(k knownCar) bool {
		return strings.HasSuffix(k.Name, " - Stock") || strings.HasSuffix(k.Name, " - Stock (PP)")
	})
	nfse := s.carsWhere(func(k knownCar) bool { return strings.HasSuffix(k.Name, " - NFS Edition") })
	classic := s.carsWhere(func(k knownCar) bool {
		y, ok := carYearOf(k.ID)
		return ok && !strings.Contains(k.Name, "Performance pack") && y >= 30 && y <= 90 // 1930 to 1990
	})
	return []Mode{
		{Name: "Normal", Desc: "The game's own traffic, everyone drives their own car.", TrafficDensity: -1, TrafficCars: -1, Builtin: true},
		{Name: "Rush hour", Desc: "Dense traffic: a busy road all race.", TrafficDensity: 1.5, TrafficCars: 50, Builtin: true},
		{Name: "Gridlock", Desc: "As much traffic as the road holds; it comes in waves.", TrafficDensity: 3, TrafficCars: 50, Builtin: true},
		{Name: "Calm cruise", Desc: "Light traffic, room to drive.", TrafficDensity: 0.3, TrafficCars: 10, Builtin: true},
		{Name: "Empty roads", Desc: "No traffic at all.", TrafficDensity: 0, TrafficCars: 0, Builtin: true},
		{Name: "Stock cars only", Desc: "Everyone gets a stock car at random.", TrafficDensity: -1, TrafficCars: -1, ForcedCars: stock, Builtin: true},
		{Name: "NFS Edition cars", Desc: "Everyone gets a random NFS Edition car.", TrafficDensity: -1, TrafficCars: -1, ForcedCars: nfse, Builtin: true},
		{Name: "Classics", Desc: "Cars of 1990 and older, one at random each.", TrafficDensity: -1, TrafficCars: -1, ForcedCars: classic, Builtin: true},
	}
}

func (s *Server) modesPath() string {
	return filepath.Join(filepath.Dir(s.cfg.RaceSettingsFile), "modes.json")
}

func (s *Server) customModes() []Mode {
	var out []Mode
	if b, err := os.ReadFile(s.modesPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (s *Server) allModes() []Mode {
	return append(s.builtinModes(), s.customModes()...)
}

func (s *Server) findMode(name string) (Mode, bool) {
	for _, m := range s.allModes() {
		if strings.EqualFold(m.Name, strings.TrimSpace(name)) {
			return m, true
		}
	}
	return Mode{}, false
}

func (s *Server) applyMode(m Mode) error {
	cur := s.raceSettings()
	cur.TrafficDensity, cur.TrafficCars, cur.ForcedCars, cur.Mode = m.TrafficDensity, m.TrafficCars, m.ForcedCars, m.Name
	return s.saveRaceSettings(cur)
}

func (s *Server) cmdMode(cmd string, args []string) CommandResult {
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	if cmd == "modes" || len(args) == 0 {
		cur := s.raceSettings().Mode
		out := []string{"modes (* = active):"}
		for _, m := range s.allModes() {
			mark := " "
			if strings.EqualFold(m.Name, cur) {
				mark = "*"
			}
			extra := ""
			if len(m.ForcedCars) > 0 {
				extra = fmt.Sprintf(", %d cars", len(m.ForcedCars))
			}
			out = append(out, fmt.Sprintf("  %s %-18s %s%s", mark, m.Name, m.Desc, extra))
		}
		return CommandResult{Lines: out}
	}
	switch strings.ToLower(args[0]) {
	case "save":
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		if name == "" || len(name) > 30 {
			return say("usage: mode save <name> (the current traffic and car settings become a mode)")
		}
		for _, b := range s.builtinModes() {
			if strings.EqualFold(b.Name, name) {
				return say("%q is a built-in mode: pick another name", name)
			}
		}
		cur := s.raceSettings()
		var keep []Mode
		for _, m := range s.customModes() {
			if !strings.EqualFold(m.Name, name) {
				keep = append(keep, m)
			}
		}
		keep = append(keep, Mode{Name: name, Desc: "Your own mode.", TrafficDensity: cur.TrafficDensity, TrafficCars: cur.TrafficCars, ForcedCars: cur.ForcedCars})
		b, _ := json.MarshalIndent(keep, "", "  ")
		if err := os.WriteFile(s.modesPath(), b, 0o644); err != nil {
			return say("could not save: %v", err)
		}
		cur.Mode = name
		_ = s.saveRaceSettings(cur)
		return say("mode %q saved from the current settings", name)
	case "delete":
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		var keep []Mode
		found := false
		for _, m := range s.customModes() {
			if strings.EqualFold(m.Name, name) {
				found = true
			} else {
				keep = append(keep, m)
			}
		}
		if !found {
			return say("there is no custom mode %q (built-in modes cannot be deleted)", name)
		}
		b, _ := json.MarshalIndent(keep, "", "  ")
		if err := os.WriteFile(s.modesPath(), b, 0o644); err != nil {
			return say("could not save: %v", err)
		}
		return say("mode %q deleted", name)
	}
	name := strings.TrimSpace(strings.Join(args, " "))
	m, ok := s.findMode(name)
	if !ok {
		return say("there is no mode %q (modes lists them)", name)
	}
	if err := s.applyMode(m); err != nil {
		return say("could not apply: %v", err)
	}
	return say("mode %q is set: it applies from the next race server that starts (stop the running one to apply it now)", m.Name)
}
