package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Web Autolog types from the PC exe's reflection records (docs/AUTOLOG.md):
//
//	XPMultiplier        {Multiplier float}                    getPlaylistMultiplier
//	WeeklyPlaylistIndex {PlaylistIndex uint32}                getPlaylistIndex
//	EventStanding       {BlazeId str, EventId, TimeMs, RaceRewinds,
//	                     AverageSpeed float, StageRestarted, Difficulty}  run leaderboards
//	RaceMetaDataBlob    {BlazeId str, EventId, SplitTimesMS [str]}        split times
//	Friend              31 fields, Status = FriendStatus enum
//	                    (0 Unknown, 1 Offline, 2 Online, 3 Playing, 4 Joinable,
//	                     5 Chatting, 6 Away, 7 NotAvailable, 8 DoNotDisturb)

const (
	friendStatusOffline  = 1
	friendStatusPlaying  = 3
	friendStatusJoinable = 4 // FriendStatus_Joinable
)

// ------------------------------------------------------------ split times

// setRaceMetaDataBlobList bodies are not strict JSON:
//
//	{"RaceMetaDataBlobs" : [{"BlazeId" : {1000001}, "EventId" : {-1721928781}, "SplitTimesMS" : ["23","36"] }, ...]}
var blobRe = regexp.MustCompile(`"BlazeId"\s*:\s*\{?"?(-?\d+)"?\}?\s*,\s*"EventId"\s*:\s*\{?(-?\d+)\}?\s*,\s*"SplitTimesMS"\s*:\s*\[([^\]]*)\]`)

func parseBlobs(body []byte) map[string]map[string][]string {
	out := map[string]map[string][]string{}
	for _, m := range blobRe.FindAllSubmatch(body, -1) {
		var times []string
		for _, t := range strings.Split(string(m[3]), ",") {
			if t = strings.Trim(strings.TrimSpace(t), `"`); t != "" {
				times = append(times, t)
			}
		}
		id, ev := string(m[1]), string(m[2])
		if out[id] == nil {
			out[id] = map[string][]string{}
		}
		out[id][ev] = times
	}
	return out
}

func (ws *webStore) blobPath(id string) string { return filepath.Join(ws.dir, "splits_"+id+".json") }

func (ws *webStore) saveBlobs(all map[string]map[string][]string) int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	n := 0
	for id, evs := range all {
		cur := map[string][]string{}
		if b, err := os.ReadFile(ws.blobPath(id)); err == nil {
			_ = json.Unmarshal(b, &cur)
		}
		for ev, t := range evs {
			if len(t) == 0 {
				continue // the game posts every event, most without times
			}
			cur[ev] = t
			n++
		}
		_ = os.MkdirAll(ws.dir, 0755)
		b, _ := json.Marshal(cur)
		_ = os.WriteFile(ws.blobPath(id), b, 0644)
	}
	return n
}

func (ws *webStore) blob(id, ev string) []string {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	cur := map[string][]string{}
	if b, err := os.ReadFile(ws.blobPath(id)); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	if t := cur[ev]; t != nil {
		return t
	}
	return []string{}
}

// ------------------------------------------------------------ run leaderboard

// eventIDJSON writes a Blaze event id the way the game's own JSON does (signed 32-bit).
func eventIDJSON(ev uint64) int64 { return int64(int32(uint32(ev))) }

// webRunStandings: EventStanding entries from the The Run stage speedwall
// (board run_single); top=true keeps the best entry per stage, otherwise the
// given player's entries.
func (s *Server) webRunStandings(id string, top bool) []map[string]any {
	out := []map[string]any{}
	entries, err := s.db.LoadBoard("run_single")
	if err != nil {
		return out
	}
	ref, _, ok := s.webPersonaByID(id)
	best := map[uint64]int{}
	for i, e := range entries {
		if top {
			if j, seen := best[e.EventID]; !seen || e.Score < entries[j].Score {
				best[e.EventID] = i
			}
			continue
		}
		if ok && e.PersonaID == ref.ID {
			best[e.EventID] = i
		}
	}
	keys := make([]uint64, 0, len(best))
	for k := range best {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
	for _, k := range keys {
		e := entries[best[k]]
		bid := e.BlazeID
		if bid == 0 {
			bid = e.PersonaID
		}
		out = append(out, map[string]any{
			"BlazeId": strconv.FormatUint(bid, 10), "EventId": eventIDJSON(e.EventID),
			"TimeMs": e.Score, "RaceRewinds": 0, "AverageSpeed": 0.0, "StageRestarted": 0, "Difficulty": 0,
		})
	}
	return out
}

// ------------------------------------------------------------ news viewed

func (s *Server) newsViewed(id string) map[int]bool {
	seen := map[int]bool{}
	if v, ok := s.web.load(id)["newsViewed"].([]any); ok {
		for _, x := range v {
			if f, ok := x.(float64); ok {
				seen[int(f)] = true
			}
		}
	}
	return seen
}

func (s *Server) markNewsViewed(id string, newsID int) {
	seen := s.newsViewed(id)
	seen[newsID] = true
	list := []int{}
	for k := range seen {
		list = append(list, k)
	}
	sort.Ints(list)
	s.web.merge(id, map[string]any{"newsViewed": list})
}
