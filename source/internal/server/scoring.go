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
// Ported from reference/modes.py. The team modes (teamcannonball, teamrelay, teamcaptain) are not ported yet.
const (
	scorePointsBounty = 3
	scoreComeback     = 1
	scoreStreakBonus  = 2
	scoreStreakCap    = 3
	scoreSurvivorLoss = 2
	scoreRivalBonus   = 2
	scoreJackpotEvery = 3
	scoreUnderdog     = 1.5
	scoreHareCaught   = 3
	scoreHareEscape   = 1
	scorePlagueShare  = 0.5
	scoreMarginMax    = 10
	scoreDNFPenalty   = 60.0
	scorePhotoGap     = 1.0
	scorePhotoBonus   = 2
	scorePersonalBest = 3
	scoreWinnersKept  = 10
)

var scoreTable = []int{10, 8, 6, 4, 3, 2, 1}

// ScorePlayer is one player's line in the standings.
type ScorePlayer struct {
	Points     int     `json:"points"`
	Total      float64 `json:"total"`
	Best       float64 `json:"best"` // 0 = none yet
	Wins       int     `json:"wins"`
	Races      int     `json:"races"`
	DNF        int     `json:"dnf"`
	Bonus      int     `json:"bonus"`
	Eliminated bool    `json:"eliminated"`
}

// Standings is the running table of the active scoring mode, kept in data/standings.json.
type Standings struct {
	Mode      string                  `json:"mode"` // "" = scoring off
	Scheme    string                  `json:"scheme"`
	Round     int                     `json:"round"`
	Races     int                     `json:"races"`
	Field     int                     `json:"field"`
	Players   map[string]*ScorePlayer `json:"players"`
	Streak    string                  `json:"streak_name,omitempty"`
	StreakN   int                     `json:"streak_count,omitempty"`
	Hare      string                  `json:"hare,omitempty"`
	Infected  []string                `json:"infected,omitempty"`
	Champions map[string]int          `json:"champions,omitempty"` // rounds won in the knock-out modes
	NewsID    int                     `json:"news_id,omitempty"`
	Events    []string                `json:"events,omitempty"` // what the last race changed
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

func (st *Standings) active(rs []RaceFinish) []RaceFinish {
	var out []RaceFinish
	for _, r := range rs {
		if p := st.Players[r.Name]; p == nil || !p.Eliminated {
			out = append(out, r)
		}
	}
	return out
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

func rankTimeAttack(st *Standings) []string {
	names := sortedNames(st)
	sort.SliceStable(names, func(i, j int) bool {
		a, b := st.Players[names[i]], st.Players[names[j]]
		if (a.Best == 0) != (b.Best == 0) {
			return b.Best == 0
		}
		return a.Best < b.Best
	})
	return names
}

func rankKnockout(st *Standings) []string {
	names := sortedNames(st)
	sort.SliceStable(names, func(i, j int) bool {
		a, b := st.Players[names[i]], st.Players[names[j]]
		if a.Eliminated != b.Eliminated {
			return !a.Eliminated
		}
		return a.Total < b.Total
	})
	return names
}

func rankStage(st *Standings) []string {
	names := sortedNames(st)
	key := func(p *ScorePlayer) float64 {
		if p.Races == 0 {
			return 1e12
		}
		return p.Total
	}
	sort.SliceStable(names, func(i, j int) bool {
		a, b := st.Players[names[i]], st.Players[names[j]]
		if key(a) != key(b) {
			return key(a) < key(b)
		}
		return a.Wins > b.Wins
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

func timeOf(rs []RaceFinish, name string) (float64, bool) {
	for _, r := range rs {
		if r.Name == name {
			return r.Time, true
		}
	}
	return 0, false
}

func timeAttack(st *Standings, rs []RaceFinish) []string {
	var ev []string
	slowest := maxTime(rs)
	for _, r := range rs {
		p := st.book(r, slowest)
		if r.Time > 0 {
			s := scoreFmt(r.Time)
			if p.Best == r.Time {
				s += "  (new best)"
			}
			ev = append(ev, r.Name+" "+s)
		} else {
			ev = append(ev, r.Name+" DNF")
		}
	}
	st.Races++
	return ev
}

// knockout: elimination drops the slowest (a non-finisher first); halving drops the slower half. Players that were
// in earlier races of this round but did not finish this one are scored as non-finishers.
func knockout(halving bool) func(*Standings, []RaceFinish) []string {
	return func(st *Standings, rs []RaceFinish) []string {
		var ev []string
		seen := map[string]bool{}
		for _, r := range rs {
			seen[r.Name] = true
		}
		for _, n := range sortedNames(st) {
			if !seen[n] && !st.Players[n].Eliminated {
				rs = append(rs, RaceFinish{Name: n}) // missing from the results = did not finish
			}
		}
		slowest := maxTime(rs)
		for _, r := range rs {
			st.book(r, slowest)
		}
		act := st.active(rs)
		sort.SliceStable(act, func(i, j int) bool {
			if (act[i].Time == 0) != (act[j].Time == 0) {
				return act[j].Time == 0
			}
			return act[i].Time < act[j].Time
		})
		if len(act) > 1 {
			var out []RaceFinish
			if halving {
				keep := 1
				if len(act) > 2 {
					keep = (len(act) + 1) / 2
				}
				out = act[keep:]
			} else {
				out = act[len(act)-1:]
			}
			for _, r := range out {
				st.player(r.Name).Eliminated = true
				t := "DNF"
				if r.Time > 0 {
					t = scoreFmt(r.Time)
				}
				ev = append(ev, fmt.Sprintf("OUT %s (%s)", r.Name, t))
			}
		}
		var left []string
		for _, n := range sortedNames(st) {
			if !st.Players[n].Eliminated {
				left = append(left, n)
			}
		}
		if len(left) == 1 && len(act) > 1 {
			st.player(left[0]).Wins++
			if st.Champions == nil {
				st.Champions = map[string]int{}
			}
			st.Champions[left[0]]++
			ev = append(ev, "WINNER "+left[0])
		}
		st.Races++
		return ev
	}
}

func bounty(st *Standings, rs []RaceFinish) []string {
	leader := ""
	if st.Races > 0 {
		if r := rankPoints(st); len(r) > 0 {
			leader = r[0]
		}
	}
	ev := cannonball(st, rs)
	fin := finishers(rs)
	if lt, ok := timeOf(rs, leader); ok && len(rs) > 1 {
		for _, r := range fin {
			if r.Name != leader && (lt == 0 || r.Time < lt) {
				st.add(r.Name, scorePointsBounty)
				ev = append(ev, fmt.Sprintf("BOUNTY  %s beat leader %s  +%d", r.Name, leader, scorePointsBounty))
			}
		}
	}
	if len(fin) >= 3 {
		n := fin[len(fin)-1].Name
		st.add(n, scoreComeback)
		ev = append(ev, fmt.Sprintf("COMEBACK  %s  +%d", n, scoreComeback))
	}
	return ev
}

func streak(st *Standings, rs []RaceFinish) []string {
	ev := cannonball(st, rs)
	winner := ""
	if len(rs) > 0 && rs[0].Time > 0 {
		winner = rs[0].Name
	}
	switch {
	case winner == "":
		st.Streak, st.StreakN = "", 0
	case st.Streak == winner:
		st.StreakN++
	default:
		st.Streak, st.StreakN = winner, 1
	}
	if winner != "" && st.StreakN >= 2 {
		n := st.StreakN - 1
		if n > scoreStreakCap {
			n = scoreStreakCap
		}
		st.add(winner, scoreStreakBonus*n)
		ev = append(ev, fmt.Sprintf("STREAK  %s wins x%d  +%d", winner, st.StreakN, scoreStreakBonus*n))
	}
	return ev
}

func survivor(st *Standings, rs []RaceFinish) []string {
	ev := cannonball(st, rs)
	fin := finishers(rs)
	var losers []string
	for _, r := range rs {
		if r.Time == 0 {
			losers = append(losers, r.Name)
		}
	}
	if len(fin) >= 3 {
		losers = append(losers, fin[len(fin)-1].Name)
	}
	for _, n := range losers {
		p := st.player(n)
		lost := scoreSurvivorLoss
		if p.Points < lost {
			lost = p.Points
		}
		if lost > 0 {
			p.Points -= lost
			p.Bonus -= lost
			ev = append(ev, fmt.Sprintf("SURVIVOR  %s  -%d", n, lost))
		}
	}
	return ev
}

func stageRace(st *Standings, rs []RaceFinish) []string {
	slowest := maxTime(rs)
	for place, r := range rs {
		p := st.book(r, slowest)
		if place == 0 && r.Time > 0 {
			p.Wins++
			p.Points = p.Wins // the points column shows stage wins
		}
	}
	st.Races++
	rk := rankStage(st)
	var parts []string
	lead := st.Players[rk[0]].Total
	for i, n := range rk {
		if i >= 6 {
			break
		}
		if i == 0 {
			parts = append(parts, n+" LEAD")
		} else {
			parts = append(parts, n+" +"+scoreFmt(st.Players[n].Total-lead))
		}
	}
	return []string{"STAGE  " + strings.Join(parts, "   ")}
}

func rivals(st *Standings, rs []RaceFinish) []string {
	var before []string
	if st.Races > 0 {
		before = rankPoints(st)
	}
	ev := cannonball(st, rs)
	for i, name := range before {
		rival := ""
		if i > 0 {
			rival = before[i-1]
		} else if len(before) > 1 {
			rival = before[1]
		}
		tn, ok1 := timeOf(rs, name)
		tr, ok2 := timeOf(rs, rival)
		if rival == "" || !ok1 || !ok2 || tn <= 0 {
			continue
		}
		if tr == 0 || tn < tr {
			st.add(name, scoreRivalBonus)
			ev = append(ev, fmt.Sprintf("RIVAL  %s beat %s  +%d", name, rival, scoreRivalBonus))
		}
	}
	return ev
}

func scaled(st *Standings, rs []RaceFinish, factor func(string, []string) float64) []string {
	before := map[string]int{}
	for n, p := range st.Players {
		before[n] = p.Points
	}
	ranked := rankPoints(st)
	ev := cannonball(st, rs)
	for _, r := range rs {
		p := st.Players[r.Name]
		f := factor(r.Name, ranked)
		extra := int(math.Round(float64(p.Points-before[r.Name]) * (f - 1)))
		if extra > 0 {
			p.Points += extra
			p.Bonus += extra
			ev = append(ev, fmt.Sprintf("x%.1f  %s  +%d", f, r.Name, extra))
		}
	}
	return ev
}

func jackpot(st *Standings, rs []RaceFinish) []string {
	double := (st.Races+1)%scoreJackpotEvery == 0
	return scaled(st, rs, func(string, []string) float64 {
		if double {
			return 2
		}
		return 1
	})
}

func underdog(st *Standings, rs []RaceFinish) []string {
	return scaled(st, rs, func(name string, ranked []string) float64 {
		for i, n := range ranked {
			if n == name && len(ranked) >= 3 && i >= len(ranked)-len(ranked)/3 {
				return scoreUnderdog
			}
		}
		return 1
	})
}

func reverse(st *Standings, rs []RaceFinish) []string {
	var ev []string
	slowest := maxTime(rs)
	fin := finishers(rs)
	for _, r := range rs {
		p := st.book(r, slowest)
		if r.Time <= 0 {
			ev = append(ev, "DNF "+r.Name+"  +0")
			continue
		}
		place := 0
		for i, f := range fin {
			if f.Name == r.Name {
				place = i
			}
		}
		pts := place + 1 // the last finisher scores the most
		p.Points += pts
		if place == len(fin)-1 {
			p.Wins++
		}
		ev = append(ev, fmt.Sprintf("%d. %s %s  +%d", place+1, r.Name, scoreFmt(r.Time), pts))
	}
	st.Races++
	return ev
}

func photofinish(st *Standings, rs []RaceFinish) []string {
	ev := cannonball(st, rs)
	fin := finishers(rs)
	if len(fin) >= 2 && fin[1].Time-fin[0].Time < scorePhotoGap {
		st.player(fin[0].Name).Points += scorePhotoBonus
		st.player(fin[1].Name).Points += scorePhotoBonus
		ev = append(ev, fmt.Sprintf("PHOTO FINISH (%.2f s): %s and %s +%d", fin[1].Time-fin[0].Time, fin[0].Name, fin[1].Name, scorePhotoBonus))
	}
	return ev
}

func personalBest(st *Standings, rs []RaceFinish) []string {
	before := map[string]float64{}
	for _, r := range rs {
		if p := st.Players[r.Name]; p != nil {
			before[r.Name] = p.Best
		}
	}
	ev := cannonball(st, rs)
	for _, r := range rs {
		if b := before[r.Name]; r.Time > 0 && b > 0 && r.Time < b {
			st.player(r.Name).Points += scorePersonalBest
			ev = append(ev, fmt.Sprintf("PERSONAL BEST %s  +%d", r.Name, scorePersonalBest))
		}
	}
	return ev
}

func hare(st *Standings, rs []RaceFinish) []string {
	h := st.Hare
	ev := cannonball(st, rs)
	if ht, ok := timeOf(rs, h); ok && len(rs) > 1 {
		for _, r := range rs {
			if r.Name == h {
				continue
			}
			if r.Time > 0 && (ht == 0 || r.Time < ht) {
				st.add(r.Name, scoreHareCaught)
				ev = append(ev, fmt.Sprintf("CAUGHT  %s passed hare %s  +%d", r.Name, h, scoreHareCaught))
			} else if ht > 0 {
				st.add(h, scoreHareEscape)
				ev = append(ev, fmt.Sprintf("ESCAPED  hare %s beat %s  +%d", h, r.Name, scoreHareEscape))
			}
		}
	}
	st.Hare = ""
	if len(rs) > 0 && rs[0].Time > 0 {
		st.Hare = rs[0].Name
		ev = append(ev, "HARE  "+st.Hare)
	}
	return ev
}

func plague(st *Standings, rs []RaceFinish) []string {
	before := map[string]int{}
	for n, p := range st.Players {
		before[n] = p.Points
	}
	ev := cannonball(st, rs)
	has := func(n string) int {
		for i, x := range st.Infected {
			if x == n {
				return i
			}
		}
		return -1
	}
	for _, r := range rs {
		if has(r.Name) >= 0 {
			p := st.Players[r.Name]
			lost := int(float64(p.Points-before[r.Name]) * (1 - scorePlagueShare))
			if lost > 0 {
				p.Points -= lost
				p.Bonus -= lost
				ev = append(ev, fmt.Sprintf("INFECTED  %s keeps %d%% of the points  -%d", r.Name, int(scorePlagueShare*100), lost))
			}
		}
	}
	if len(rs) > 0 && rs[0].Time > 0 {
		if i := has(rs[0].Name); i >= 0 {
			st.Infected = append(st.Infected[:i], st.Infected[i+1:]...)
			ev = append(ev, "CURED  "+rs[0].Name)
		}
	}
	fin := finishers(rs)
	var sick []string
	for _, r := range rs {
		if r.Time == 0 {
			sick = append(sick, r.Name)
		}
	}
	if len(fin) >= 3 {
		sick = append(sick, fin[len(fin)-1].Name)
	}
	for _, n := range sick {
		if has(n) < 0 {
			st.Infected = append(st.Infected, n)
			ev = append(ev, "INFECTED  "+n)
		}
	}
	return ev
}

func margin(st *Standings, rs []RaceFinish) []string {
	var ev []string
	slowest := maxTime(rs)
	lead := 0.0
	if len(rs) > 0 {
		lead = rs[0].Time
	}
	for place, r := range rs {
		p := st.book(r, slowest)
		if lead <= 0 || r.Time <= 0 {
			ev = append(ev, "DNF "+r.Name+"  +0")
			continue
		}
		pts := scoreMarginMax - int((r.Time-lead)/2)
		if pts < 0 {
			pts = 0
		}
		p.Points += pts
		if place == 0 {
			p.Wins++
		}
		ev = append(ev, fmt.Sprintf("%d. %s %s  +%d", place+1, r.Name, scoreFmt(r.Time), pts))
	}
	st.Races++
	return ev
}

var scoreModes = map[string]scoreMode{
	"cannonball":   {"points per place, total time breaks ties", cannonball, rankPoints},
	"timeattack":   {"best single race time wins", timeAttack, rankTimeAttack},
	"elimination":  {"after every race the slowest (or a non-finisher) is out; the last one left wins the round", knockout(false), rankKnockout},
	"halving":      {"knockout: after every race the slower half of the players is out", knockout(true), rankKnockout},
	"streak":       {"cannonball plus a bonus that grows for winning races back to back", streak, rankPoints},
	"survivor":     {"cannonball, but a non-finisher and the last finisher (3+ finishers) lose points", survivor, rankPoints},
	"stagerace":    {"no points: the lowest total time leads, stage wins are counted", stageRace, rankStage},
	"rivals":       {"cannonball plus a bonus for finishing ahead of the player ranked just above you", rivals, rankPoints},
	"jackpot":      {"cannonball where every third race counts double", jackpot, rankPoints},
	"underdog":     {"cannonball where the bottom third of the standings earns more (3+ players)", underdog, rankPoints},
	"hare":         {"catch the leader: the previous winner is the hare", hare, rankPoints},
	"plague":       {"the last finisher and non-finishers are infected and keep half the points until they win", plague, rankPoints},
	"margin":       {"points shrink with the gap to the winner", margin, rankPoints},
	"reverse":      {"the slowest finisher scores the most points", reverse, rankPoints},
	"photofinish":  {"cannonball plus a bonus when the top two finish within a second", photofinish, rankPoints},
	"personalbest": {"cannonball plus a bonus for beating your own best time", personalBest, rankPoints},
	"bounty":       {"cannonball plus a bonus for beating the points leader", bounty, rankPoints},
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
	fmt.Fprintf(&b, "Mode: %s (round %d, %d races)\n\n", st.Mode, st.Round+1, st.Races)
	for i, n := range m.rank(st) {
		p := st.Players[n]
		extra := ""
		if p.Eliminated {
			extra = "  OUT"
		}
		if n == st.Hare {
			extra += "  HARE"
		}
		for _, x := range st.Infected {
			if x == n {
				extra += "  INFECTED"
			}
		}
		fmt.Fprintf(&b, "%d. %s  %d pts  %s  %d wins%s\n", i+1, n, p.Points, scoreFmt(p.Total), p.Wins, extra)
	}
	if len(st.Champions) > 0 {
		var cs []string
		for n, c := range st.Champions {
			cs = append(cs, fmt.Sprintf("%s x%d", n, c))
		}
		sort.Strings(cs)
		b.WriteString("\nRound winners: " + strings.Join(cs, ", ") + "\n")
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
	won := false
	for _, e := range st.Events {
		if strings.HasPrefix(e, "WINNER ") {
			won = true
		}
	}
	for _, e := range st.Events {
		s.log.Printf("[SCORE] %s: %s", st.Mode, e)
	}
	s.publishStandings(st)
	if won { // the round is over: the next race starts a fresh one
		keep := st.Champions
		*st = Standings{Mode: st.Mode, Scheme: st.Scheme, Round: st.Round + 1, Players: map[string]*ScorePlayer{}, Champions: keep, NewsID: st.NewsID}
	}
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
	return map[string]any{"mode": st.Mode, "scheme": st.Scheme, "modes": modes, "round": st.Round + 1, "races": st.Races, "table": standingsText(st)}
}
