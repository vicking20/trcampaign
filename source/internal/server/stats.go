package server

import (
	"strconv"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Stats (0x0007) leaderboards.
//
// The game sends getLeaderboardGroup {LBID 1, NAME "RunRace"} and
// getFilteredLeaderboard {FILT 1, IDLS [BlazeIds], KSUM {"therun": <event id>}}. The "therun" key scope
// value is the same event id the GameReporting reports carry (RUN/RACE), so the
// leaderboard is served straight from the leaderboards table (any board).
//
// Layouts:
//   LeaderboardGroupResponse { ASCD bool, BNAM str, DESC str, ETYP pair, KSUM map<str,ScopeValues{KSVL map<i64,i64>}>,
//                              LBSZ int, LIST list<StatDescSummary>, META str, NAME str, SNAM str }
//   LeaderboardStatValues    { LDLS list<LeaderboardStatValuesRow{ENAM, ENID, RANK, RSTA, RWFG, STAT list<str>, UATT}> }
// Ranked stat = time in milliseconds (as text), ascending (lower is better).

const (
	ComponentStats              uint16 = 0x0007
	StatsGetLeaderboardGroup    uint16 = 10
	StatsGetLeaderboard         uint16 = 12
	StatsGetCenteredLeaderboard uint16 = 13
	StatsGetFilteredLeaderboard uint16 = 14
	statsRankedStat                    = "time"
	statsKeyScope                      = "therun"
	statsLeaderboardSize               = 100
)

func writeTagPair(w *blaze.Writer, tag string, a, b uint64) {
	w.Tag(tag, blaze.TDFPair)
	w.Var(a)
	w.Var(b)
}

func buildLeaderboardGroupResponse(name string) []byte {
	if name == "" {
		name = "RunRace"
	}
	var w blaze.Writer
	w.TagBool("ASCD", true)
	w.TagString("BNAM", name)
	w.TagString("DESC", name)
	writeTagPair(&w, "ETYP", uint64(ComponentUserSessions), 1) // entity = user
	// KSUM map<string, ScopeValues{KSVL map<int64,int64>}>
	w.Tag("KSUM", blaze.TDFMap)
	w.WriteByte(byte(blaze.TDFString))
	w.WriteByte(byte(blaze.TDFGroup))
	w.Var(1)
	w.Var(uint64(len(statsKeyScope) + 1))
	w.WriteString(statsKeyScope)
	w.WriteByte(0)
	w.EmptyMap("KSVL", blaze.TDFVarInt, blaze.TDFVarInt)
	w.GroupEnd()
	w.TagVar("LBSZ", statsLeaderboardSize)
	writeStructListHeader(&w, "LIST", 1)
	w.TagString("CATG", name)
	w.TagString("DFLT", "0")
	w.TagBool("DRVD", false)
	w.TagString("FRMT", "%d")
	w.TagString("KIND", "")
	w.TagString("LDSC", "Best time (ms)")
	w.TagString("META", "")
	w.TagString("NAME", statsRankedStat)
	w.TagString("SDSC", "Time")
	w.TagVar("TYPE", 0)
	w.GroupEnd()
	w.TagString("META", "")
	w.TagString("NAME", name)
	w.TagString("SNAM", statsRankedStat)
	return w.Bytes()
}

type leaderboardRow struct {
	Name    string
	ID      uint64
	Rank    uint64
	TimeMs  uint64
	Persona uint64
}

func buildLeaderboardStatValues(rows []leaderboardRow) []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "LDLS", len(rows))
	for _, r := range rows {
		t := strconv.FormatUint(r.TimeMs, 10)
		w.TagString("ENAM", r.Name)
		w.TagVar("ENID", r.ID)
		w.TagVar("RANK", r.Rank)
		w.TagString("RSTA", t)
		w.TagBool("RWFG", false)
		w.Tag("STAT", blaze.TDFList)
		w.WriteByte(byte(blaze.TDFString))
		w.Var(1)
		w.Var(uint64(len(t) + 1))
		w.WriteString(t)
		w.WriteByte(0)
		w.TagVar("UATT", 0)
		w.GroupEnd()
	}
	return w.Bytes()
}

// keyScopeEvent reads KSUM {"therun": event}.
func keyScopeEvent(fs []blaze.Field) uint64 {
	for _, f := range fs {
		if f.Tag != "KSUM" {
			continue
		}
		m, _ := f.Value.(map[string]any)
		items, _ := m["items"].([][2]any)
		for _, kv := range items {
			if k, _ := kv[0].(string); k == statsKeyScope {
				v, _ := kv[1].(uint64)
				return v
			}
		}
		for _, kv := range items { // any scope if the name differs
			if v, ok := kv[1].(uint64); ok {
				return v
			}
		}
	}
	return 0
}

func idList(fs []blaze.Field, tag string) []uint64 {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		m, _ := f.Value.(map[string]any)
		items, _ := m["items"].([]any)
		var out []uint64
		for _, it := range items {
			if v, ok := it.(uint64); ok {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

// rankedRows ranks every persona's best for the event (fastest first, one row
// per persona even if several boards hold the same event).
func rankedRows(entries []database.BoardEntry) []leaderboardRow {
	seen := map[uint64]bool{}
	var out []leaderboardRow
	for _, e := range entries {
		if seen[e.PersonaID] {
			continue
		}
		seen[e.PersonaID] = true
		wire := database.Persona{ID: e.PersonaID, BlazeID: e.BlazeID}.BlazeID
		out = append(out, leaderboardRow{Name: e.PersonaName, ID: wire, TimeMs: e.Score, Persona: e.PersonaID})
	}
	// entries come ordered by board then score; re-sort by score across boards
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].TimeMs < out[j-1].TimeMs; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	for i := range out {
		out[i].Rank = uint64(i + 1)
	}
	return out
}

func (s *Server) handleStats(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	name := blaze.CommandName(p.Component, p.Command)
	switch p.Command {
	case StatsGetLeaderboardGroup:
		board := blaze.FindString(fields, "NAME")
		s.log.Printf("[STATS] %s: %q", name, board)
		return s.response(p, buildLeaderboardGroupResponse(board))

	case StatsGetLeaderboard, StatsGetCenteredLeaderboard, StatsGetFilteredLeaderboard:
		event := keyScopeEvent(fields)
		var rows []leaderboardRow
		if event != 0 {
			entries, err := s.db.LoadBoardEntries(nil, []uint64{event})
			if err != nil {
				s.log.Printf("[STATS] %s query failed: %v", name, err)
			}
			rows = rankedRows(entries)
		}
		switch p.Command {
		case StatsGetFilteredLeaderboard:
			want := map[uint64]bool{}
			for _, id := range idList(fields, "IDLS") {
				want[id] = true
			}
			var kept []leaderboardRow
			for _, r := range rows {
				if want[r.ID] {
					kept = append(kept, r)
				}
			}
			rows = kept
		case StatsGetCenteredLeaderboard:
			center := fieldUintAuth(fields, "CENT")
			count := int(fieldUintAuth(fields, "COUN"))
			rows = centerRows(rows, center, count)
		default:
			start := int(fieldUintAuth(fields, "STRT"))
			count := int(fieldUintAuth(fields, "COUN"))
			rows = pageRows(rows, start, count)
		}
		s.log.Printf("[STATS] %s: event=%d rows=%d", name, event, len(rows))
		return s.response(p, buildLeaderboardStatValues(rows))
	}
	s.unimplemented(p, "Stats")
	return s.unimplementedReply(p)
}

func pageRows(rows []leaderboardRow, start, count int) []leaderboardRow {
	if count <= 0 {
		count = statsLeaderboardSize
	}
	if start < 0 || start >= len(rows) {
		return nil
	}
	end := start + count
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

func centerRows(rows []leaderboardRow, center uint64, count int) []leaderboardRow {
	if count <= 0 {
		count = 10
	}
	idx := 0
	for i, r := range rows {
		if r.ID == center {
			idx = i
			break
		}
	}
	start := idx - count/2
	if start < 0 {
		start = 0
	}
	return pageRows(rows, start, count)
}
