package server

import (
	"encoding/json"
	"strings"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Blaze::NFS::AutologComponent (component 0x0801). Command names from the PC
// exe, ids and TDF layouts from the PS3 build's visit() code. See
// docs/AUTOLOG.md.
const (
	ComponentAutolog uint16 = 0x0801

	AutologGetSingleStory               uint16 = 1
	AutologGetAllStories                uint16 = 2
	AutologGetFriendsPlayRecommendation uint16 = 3
	AutologGetSpeedwall                 uint16 = 4
	AutologGetMultipleSpeedwalls        uint16 = 6
	AutologGetAggregateSpeedwall        uint16 = 7
	AutologGenerateStories              uint16 = 8
	AutologGetRoadRules                 uint16 = 10
	AutologGetBestScores                uint16 = 11
	AutologGetCoreStatSpeedwall         uint16 = 12
	AutologGetMultipleStories           uint16 = 13
	AutologSetStoryTag                  uint16 = 14
	AutologSetStoryViewed               uint16 = 15
	AutologGetRival                     uint16 = 16
	AutologGetSpeedwallOwnedCount       uint16 = 17

	speedwallRowLimit = 25
)

// SpeedwallKey identifies one Speedwall: an event of a given type.
type SpeedwallKey struct {
	Board   string // leaderboards.board_key, e.g. "race_single"
	EventID uint64
}

// SpeedwallRow is one line on a Speedwall (wire: SpeedwallResponseRow).
type SpeedwallRow struct {
	WireBlazeID uint64
	TimeMs      uint64
	VehicleID   uint64
	Attempts    uint64
	Win         bool
}

// boardForType maps the Autolog TYPE string ("Race", "Run", ...) to the
// GameReporting GTYP used as leaderboard key ("race_single", "run_single").
func boardForType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return ""
	}
	if strings.Contains(t, "_") {
		return t
	}
	if t == "runrace" || t == "run" { // Autolog TYPE "RunRace" = The Run stages, reported as GTYP run_single
		return "run_single"
	}
	return t + "_single"
}

// speedwallRowFromEntry converts a stored best into a wire row.
func speedwallRowFromEntry(e database.BoardEntry) SpeedwallRow {
	per := database.Persona{ID: e.PersonaID, BlazeID: e.BlazeID}
	row := SpeedwallRow{WireBlazeID: per.BlazeID, TimeMs: e.Score, Attempts: 1}
	var meta struct {
		VehicleID uint64            `json:"vehicleId"`
		Win       bool              `json:"win"`
		Fields    map[string]uint64 `json:"fields"`
	}
	if json.Unmarshal([]byte(e.MetadataJSON), &meta) == nil {
		row.VehicleID = meta.VehicleID
		row.Win = meta.Win
		for _, k := range []string{"ATMP", "RATP"} {
			if v, ok := meta.Fields[k]; ok && v > 0 {
				row.Attempts = v
				break
			}
		}
	}
	return row
}

// ------------------------------------------------------------- wire writers

// writeStructListHeader starts list<struct> with n elements; each element is
// written as its members followed by GroupEnd() (Heat2 struct-in-list form).
func writeStructListHeader(w *blaze.Writer, tag string, n int) {
	w.Tag(tag, blaze.TDFList)
	w.WriteByte(byte(blaze.TDFGroup))
	w.Var(uint64(n))
}

// SpeedwallResponseRow: ATMP uint16, BLID int64, BNTY int32, TIME int32, VEHI uint32, WIN uint32.
func writeSpeedwallRow(w *blaze.Writer, r SpeedwallRow) {
	w.TagVar("ATMP", r.Attempts)
	w.TagVar("BLID", r.WireBlazeID)
	w.TagVar("BNTY", 0)
	w.TagVar("TIME", r.TimeMs)
	w.TagVar("VEHI", r.VehicleID)
	w.TagBool("WIN", r.Win)
	w.GroupEnd()
}

// SpeedwallResponse: ROWS list<SpeedwallResponseRow>.
func writeSpeedwallResponseBody(w *blaze.Writer, rows []SpeedwallRow) {
	writeStructListHeader(w, "ROWS", len(rows))
	for _, r := range rows {
		writeSpeedwallRow(w, r)
	}
}

func buildSpeedwallResponse(rows []SpeedwallRow) []byte {
	var w blaze.Writer
	writeSpeedwallResponseBody(&w, rows)
	return w.Bytes()
}

// MultipleSpeedwallResponse: ROWS list<SpeedwallResponse>, one per request, same order.
func buildMultipleSpeedwallResponse(walls [][]SpeedwallRow) []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "ROWS", len(walls))
	for _, rows := range walls {
		writeSpeedwallResponseBody(&w, rows)
		w.GroupEnd()
	}
	return w.Bytes()
}

// BestScoresResponse: SCRS list<BestScoreRow{EVNT uint32, TIME int32, UINF uint32}>.
type BestScore struct{ EventID, TimeMs, UserInfo uint64 }

func buildBestScoresResponse(scores []BestScore) []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "SCRS", len(scores))
	for _, s := range scores {
		w.TagVar("EVNT", s.EventID)
		w.TagVar("TIME", s.TimeMs)
		w.TagVar("UINF", s.UserInfo)
		w.GroupEnd()
	}
	return w.Bytes()
}

// FriendsPlayResponse: STOR list<Story>, WISD list<WisdomOfCrowdsStory>.
func buildFriendsPlayResponse() []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "STOR", 0)
	writeStructListHeader(&w, "WISD", 0)
	return w.Bytes()
}

// StoryTagResponse / StoryViewResponse: BTAG bool.
func buildStoryFlagResponse(v bool) []byte {
	var w blaze.Writer
	w.TagBool("BTAG", v)
	return w.Bytes()
}

// ------------------------------------------------------------- request parsing

func fieldUint(fs []blaze.Field, tag string) uint64 {
	for _, f := range fs {
		if f.Tag == tag {
			if v, ok := f.Value.(uint64); ok {
				return v
			}
		}
	}
	return 0
}

func fieldStructList(fs []blaze.Field, tag string) [][]blaze.Field {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		m, ok := f.Value.(map[string]any)
		if !ok {
			return nil
		}
		items, _ := m["items"].([]any)
		var out [][]blaze.Field
		for _, it := range items {
			if g, ok := it.([]blaze.Field); ok {
				out = append(out, g)
			}
		}
		return out
	}
	return nil
}

// speedwallKeysFromRequests reads SpeedwallRequest{BLID,EVNT,TYPE,VETY} entries.
func speedwallKeysFromRequests(reqs [][]blaze.Field) []SpeedwallKey {
	keys := make([]SpeedwallKey, 0, len(reqs))
	for _, r := range reqs {
		keys = append(keys, SpeedwallKey{Board: boardForType(blaze.FindString(r, "TYPE")), EventID: fieldUint(r, "EVNT")})
	}
	return keys
}

// ------------------------------------------------------------- handler

func (s *Server) handleAutolog(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	name := blaze.CommandName(p.Component, p.Command)
	switch p.Command {
	case AutologGetMultipleSpeedwalls:
		keys := speedwallKeysFromRequests(fieldStructList(fields, "REQ"))
		walls := s.loadSpeedwalls(keys)
		filled := 0
		for _, w := range walls {
			if len(w) > 0 {
				filled++
			}
		}
		s.log.Printf("[AUTOLOG] %s: %d events requested, %d with times", name, len(keys), filled)
		return s.response(p, buildMultipleSpeedwallResponse(walls))

	case AutologGetSpeedwall:
		keys := speedwallKeysFromRequests([][]blaze.Field{fields})
		walls := s.loadSpeedwalls(keys)
		s.log.Printf("[AUTOLOG] %s: board=%s event=%d rows=%d", name, keys[0].Board, keys[0].EventID, len(walls[0]))
		return s.response(p, buildSpeedwallResponse(walls[0]))

	case AutologGetBestScores:
		reqs := fieldStructList(fields, "REQS")
		keys := speedwallKeysFromRequests(reqs)
		walls := s.loadSpeedwalls(keys)
		me := state.Persona.BlazeID
		var scores []BestScore
		for i, rows := range walls {
			for _, r := range rows {
				if r.WireBlazeID == me {
					scores = append(scores, BestScore{EventID: keys[i].EventID, TimeMs: r.TimeMs, UserInfo: fieldUint(reqs[i], "UINF")})
					break
				}
			}
		}
		s.log.Printf("[AUTOLOG] %s: %d requested, %d with a personal best", name, len(keys), len(scores))
		return s.response(p, buildBestScoresResponse(scores))

	case AutologGetFriendsPlayRecommendation:
		gen := blaze.FindString(fields, "GEN")
		me := fieldUint(fields, "BLID")
		if me == 0 {
			me = state.Persona.BlazeID
		}
		entries, err := s.db.LoadBoard(boardForType(gen))
		if err != nil {
			s.log.Printf("[AUTOLOG] %s: board query failed: %v", name, err)
			return s.response(p, buildFriendsPlayResponse())
		}
		stories, popular := buildRecommends(entries, me)
		s.log.Printf("[AUTOLOG] %s: GEN=%q board=%s -> %d beat-you, %d popular", name, gen, boardForType(gen), len(stories), len(popular))
		return s.response(p, buildFriendsPlayStories(gen, stories, popular))

	case AutologGetSpeedwallOwnedCount:
		// PC layout (differs from PS3): request BLID list<int64>;
		// response SCRS list<SpeedWallOwnedRow{BLID int64, OACC uint32, OCC uint32, OSC uint32}>.
		ids := idList(fields, "BLID")
		if len(ids) == 0 {
			if v := fieldUint(fields, "BLID"); v != 0 { // PS3-style single id
				ids = []uint64{v}
			}
		}
		counts := map[uint64]uint64{}
		if owned, err := s.db.SpeedwallOwnedCounts(); err != nil {
			s.log.Printf("[AUTOLOG] owned-count query failed: %v", err)
		} else {
			for _, o := range owned {
				wire := database.Persona{ID: o.PersonaID, BlazeID: o.BlazeID}.BlazeID
				counts[wire] = o.Count
			}
		}
		var w blaze.Writer
		writeStructListHeader(&w, "SCRS", len(ids))
		for _, id := range ids {
			w.TagVar("BLID", id)
			w.TagVar("OACC", 0)
			w.TagVar("OCC", 0)
			w.TagVar("OSC", counts[id])
			w.GroupEnd()
		}
		s.log.Printf("[AUTOLOG] %s: %d players, counts=%v", name, len(ids), counts)
		return s.response(p, w.Bytes())

	case AutologSetStoryTag, AutologSetStoryViewed:
		return s.response(p, buildStoryFlagResponse(fieldUint(fields, "RTAG") != 0))

	default:
		// Empty payload = every member at its default (empty lists, zero counts),
		// which is a valid "nothing to show" reply for every Autolog response.
		s.log.Printf("[AUTOLOG] %s -- default (empty) response; layout in docs/AUTOLOG.md", name)
		return s.response(p, nil)
	}
}

// loadSpeedwalls returns, for each key in order, the best times of every
// persona on this server (fastest first, at most speedwallRowLimit rows).
func (s *Server) loadSpeedwalls(keys []SpeedwallKey) [][]SpeedwallRow {
	out := make([][]SpeedwallRow, len(keys))
	if len(keys) == 0 {
		return out
	}
	boards := map[string]bool{}
	events := map[uint64]bool{}
	for _, k := range keys {
		if k.Board != "" && k.EventID != 0 {
			boards[k.Board] = true
			events[k.EventID] = true
		}
	}
	if len(events) == 0 {
		return out
	}
	entries, err := s.db.LoadBoardEntries(setKeys(boards), setKeysU(events))
	if err != nil {
		s.log.Printf("[AUTOLOG] speedwall query failed: %v", err)
		return out
	}
	byKey := map[SpeedwallKey][]SpeedwallRow{}
	for _, e := range entries { // already ordered by score
		k := SpeedwallKey{Board: e.Board, EventID: e.EventID}
		if len(byKey[k]) < speedwallRowLimit {
			byKey[k] = append(byKey[k], speedwallRowFromEntry(e))
		}
	}
	for i, k := range keys {
		out[i] = byKey[k]
	}
	return out
}

func setKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func setKeysU(m map[uint64]bool) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
