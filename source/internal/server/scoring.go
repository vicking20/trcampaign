package server

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scoring modes: a mode turns a finished race (names and seconds, from the race server's results) into points and standings.
// Only cannonball is kept; the rest of reference/modes.py was dropped as too far from the game itself.
const scoreDNFPenalty = 60.0

var scoreTable = []int{10, 8, 6, 4, 3, 2, 1}

// ScorePlayer is one player's line in the standings.
type ScorePlayer struct {
	Points int     `json:"points"`
	Total  float64 `json:"total"`
	Best   float64 `json:"best"` // 0 = none yet
	Wins   int     `json:"wins"`
	Races  int     `json:"races"`
	DNF    int     `json:"dnf"`
	Bonus  int     `json:"bonus"`
}

// Standings is the running table of the active scoring mode, kept in data/standings.json.
type Standings struct {
	Mode    string                  `json:"mode"` // "" = scoring off
	Scheme  string                  `json:"scheme"`
	Races   int                     `json:"races"`
	Field   int                     `json:"field"`
	Players map[string]*ScorePlayer `json:"players"`
	NewsID  int                     `json:"news_id,omitempty"`
	Events  []string                `json:"events,omitempty"` // what the last race changed
}

var scoreMu sync.Mutex

type scoreMode struct {
	desc  string
	apply func(*Standings, []RaceFinish) []string
	rank  func(*Standings) []string
}

func scoreFmt(t float64) string { return fmt.Sprintf("%d:%05.2f", int(t/60), math.Mod(t, 60)) }

func (st *Standings) player(name string) *ScorePlayer {
	p := st.Players[name]
	if p == nil {
		p = &ScorePlayer{}
		st.Players[name] = p
	}
	return p
}

func (st *Standings) add(name string, pts int) {
	p := st.player(name)
	p.Points += pts
	p.Bonus += pts
}

func maxTime(rs []RaceFinish) float64 {
	m := 0.0
	for _, r := range rs {
		if r.Time > m {
			m = r.Time
		}
	}
	return m
}

func (st *Standings) book(r RaceFinish, slowest float64) *ScorePlayer {
	p := st.player(r.Name)
	p.Races++
	if r.Time > 0 {
		p.Total += r.Time
		if p.Best == 0 || r.Time < p.Best {
			p.Best = r.Time
		}
	} else {
		p.DNF++
		p.Total += slowest + scoreDNFPenalty
	}
	return p
}

func placePoints(place, n int, scheme string) int {
	switch scheme {
	case "top3":
		if place < 3 {
			return []int{5, 3, 1}[place]
		}
		return 0
	case "fixed":
		if place < len(scoreTable) {
			return scoreTable[place]
		}
		return 0
	}
	if n-place < 1 {
		return 1
	}
	return n - place
}

func rankPoints(st *Standings) []string {
	names := sortedNames(st)
	sort.SliceStable(names, func(i, j int) bool {
		a, b := st.Players[names[i]], st.Players[names[j]]
		if a.Points != b.Points {
			return a.Points > b.Points
		}
		return a.Total < b.Total
	})
	return names
}

func sortedNames(st *Standings) []string {
	names := make([]string, 0, len(st.Players))
	for n := range st.Players {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func cannonball(st *Standings, rs []RaceFinish) []string {
	var ev []string
	slowest := maxTime(rs)
	if len(rs) > st.Field {
		st.Field = len(rs) // a win keeps its value when players drop out
	}
	for place, r := range rs {
		p := st.book(r, slowest)
		if r.Time > 0 {
			pts := placePoints(place, st.Field, st.Scheme)
			p.Points += pts
			if place == 0 {
				p.Wins++
			}
			ev = append(ev, fmt.Sprintf("%d. %s %s  +%d", place+1, r.Name, scoreFmt(r.Time), pts))
		} else {
			ev = append(ev, "DNF "+r.Name+"  +0")
		}
	}
	st.Races++
	return ev
}

func finishers(rs []RaceFinish) []RaceFinish {
	var f []RaceFinish
	for _, r := range rs {
		if r.Time > 0 {
			f = append(f, r)
		}
	}
	return f
}

// knockout: elimination drops the slowest (a non-finisher first); halving drops the slower half. Players that were
// in earlier races of this round but did not finish this one are scored as non-finishers.
var scoreModes = map[string]scoreMode{
	"cannonball": {"points for every finished race: more points the higher you finish, total time breaks ties; players who do not finish score 0", cannonball, rankPoints},
}

func scoreModeNames() []string {
	var n []string
	for k := range scoreModes {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func (s *Server) standingsPath() string {
	return filepath.Join(filepath.Dir(s.cfg.LockerDir), "standings.json")
}

func (s *Server) loadStandings() *Standings {
	st := &Standings{}
	if b, err := os.ReadFile(s.standingsPath()); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Players == nil {
		st.Players = map[string]*ScorePlayer{}
	}
	if st.Scheme == "" {
		st.Scheme = "field"
	}
	return st
}

func (s *Server) saveStandings(st *Standings) {
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := s.standingsPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, s.standingsPath())
	}
}

// standingsText: the table as it appears in the News item.
func standingsText(st *Standings) string {
	m, ok := scoreModes[st.Mode]
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Mode: %s (%d races)\n\n", st.Mode, st.Races)
	for i, n := range m.rank(st) {
		p := st.Players[n]
		fmt.Fprintf(&b, "%d. %s  %d pts  %s  %d wins\n", i+1, n, p.Points, scoreFmt(p.Total), p.Wins)
	}
	if len(st.Events) > 0 {
		b.WriteString("\nLast race:\n" + strings.Join(st.Events, "\n"))
	}
	return b.String()
}

// scoreRace applies a finished race to the standings and refreshes the News item. No-op while scoring is off.
func (s *Server) scoreRace(results []RaceFinish) {
	scoreMu.Lock()
	defer scoreMu.Unlock()
	st := s.loadStandings()
	m, ok := scoreModes[st.Mode]
	if !ok || len(results) == 0 {
		return
	}
	sort.SliceStable(results, func(i, j int) bool {
		if (results[i].Time == 0) != (results[j].Time == 0) {
			return results[j].Time == 0
		}
		return results[i].Time < results[j].Time
	})
	st.Events = m.apply(st, results)
	for _, e := range st.Events {
		s.log.Printf("[SCORE] %s: %s", st.Mode, e)
	}
	s.publishStandings(st)
	s.saveStandings(st)
}

func (s *Server) publishStandings(st *Standings) {
	if st.NewsID > 0 {
		_, _ = s.deleteNews(st.NewsID)
	}
	id, err := s.announce("Standings: "+st.Mode, standingsText(st))
	if err != nil {
		s.log.Printf("[SCORE] could not publish the standings: %v", err)
		return
	}
	st.NewsID = id
}

// cmdScore: "score" / "score <mode>|off" / "score scheme <field|top3|fixed>" / "score reset".
func (s *Server) cmdScore(args []string) CommandResult {
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	scoreMu.Lock()
	defer scoreMu.Unlock()
	st := s.loadStandings()
	if len(args) == 0 {
		lines := []string{"scoring: " + map[bool]string{true: st.Mode, false: "off"}[st.Mode != ""] + "  (points scheme: " + st.Scheme + ")", "modes:"}
		for _, n := range scoreModeNames() {
			lines = append(lines, fmt.Sprintf("  %-13s %s", n, scoreModes[n].desc))
		}
		if st.Mode != "" {
			lines = append(lines, "", standingsText(st))
		}
		return CommandResult{Lines: lines}
	}
	switch args[0] {
	case "off":
		if st.NewsID > 0 {
			_, _ = s.deleteNews(st.NewsID)
		}
		*st = Standings{Players: map[string]*ScorePlayer{}, Scheme: st.Scheme}
		s.saveStandings(st)
		return say("scoring is off")
	case "reset":
		*st = Standings{Mode: st.Mode, Scheme: st.Scheme, Players: map[string]*ScorePlayer{}, NewsID: st.NewsID}
		if st.Mode != "" {
			s.publishStandings(st)
		}
		s.saveStandings(st)
		return say("standings cleared")
	case "scheme":
		if len(args) != 2 || (args[1] != "field" && args[1] != "top3" && args[1] != "fixed") {
			return say("usage: score scheme <field|top3|fixed>")
		}
		st.Scheme = args[1]
		s.saveStandings(st)
		return say("points scheme: %s", st.Scheme)
	}
	if _, ok := scoreModes[args[0]]; !ok {
		return say("unknown mode %q (type: score)", args[0])
	}
	old := st.NewsID
	*st = Standings{Mode: args[0], Scheme: st.Scheme, Players: map[string]*ScorePlayer{}, NewsID: old}
	s.publishStandings(st)
	s.saveStandings(st)
	return say("scoring: %s; standings cleared, every finished race now scores (%s)", args[0], time.Now().Format("15:04"))
}

// scoringState: what the admin page's Game modes tab shows.
func (s *Server) scoringState() map[string]any {
	scoreMu.Lock()
	defer scoreMu.Unlock()
	st := s.loadStandings()
	type row struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}
	var modes []row
	for _, n := range scoreModeNames() {
		modes = append(modes, row{n, scoreModes[n].desc})
	}
	return map[string]any{"mode": st.Mode, "scheme": st.Scheme, "modes": modes, "races": st.Races, "table": standingsText(st)}
}
