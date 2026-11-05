package server

import (
	"encoding/json"
	"sort"

	"nfsrun-server/internal/blaze"
)

// GameReporting (component 0x001C). A submitOfflineGameReport payload:
//
//	FNSH int 0
//	PRVT intlist []
//	RPRT {
//	  GAME intlist [<id>]
//	  GAME { RES map<BlazeId, { FRST LIST PBST RATP|ATMP RUN|RACE TIME VEHI WIN }> }
//	}
//	GRID int 0
//	GTYP string "run_single" | "race_single"
//
// Each RES entry is one player's result: TIME in milliseconds, RUN/RACE (or
// EVNT/ROAD in pursuit-type reports) the event id hash, VEHI the vehicle hash,
// PBST/FRST/WIN flags.

const (
	ComponentGameReporting     uint16 = 0x001C
	CmdSubmitGameReport        uint16 = 0x0001
	CmdSubmitOfflineGameReport uint16 = 0x0002
	CmdSubmitGameEvents        uint16 = 0x0003
	CmdSubmitTrustedMidGameRep uint16 = 0x0064
	CmdSubmitTrustedEndGameRep uint16 = 0x0065
)

// RaceResult is one player's line in a game report.
type RaceResult struct {
	BlazeID   uint64            `json:"blazeId"`
	GameType  string            `json:"gameType"`
	EventKind string            `json:"eventKind,omitempty"` // "RUN" or "RACE" (tag that carried the id)
	EventID   uint64            `json:"eventId"`
	VehicleID uint64            `json:"vehicleId"`
	TimeMs    uint64            `json:"timeMs"`
	Win       bool              `json:"win"`
	PB        bool              `json:"personalBest"`
	First     bool              `json:"first"`
	Fields    map[string]uint64 `json:"fields"` // every integer member, for later research
}

// HasTime reports whether the entry carries a usable time for a leaderboard.
func (r RaceResult) HasTime() bool { return r.EventID != 0 && r.TimeMs != 0 }

// parseGameReport extracts GTYP and all RES entries, wherever RES sits under RPRT.
func parseGameReport(fields []blaze.Field) (string, []RaceResult) {
	gameType := blaze.FindString(fields, "GTYP")
	var out []RaceResult
	var walk func([]blaze.Field)
	walk = func(fs []blaze.Field) {
		for _, f := range fs {
			if f.Tag == "RES" && f.Type == blaze.TDFMap {
				out = append(out, resultsFromMap(f.Value, gameType)...)
				continue
			}
			if g, ok := f.Value.([]blaze.Field); ok {
				walk(g)
			}
		}
	}
	walk(fields)
	sort.SliceStable(out, func(i, j int) bool { return out[i].BlazeID < out[j].BlazeID })
	return gameType, out
}

func resultsFromMap(v any, gameType string) []RaceResult {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	items, _ := m["items"].([][2]any)
	var out []RaceResult
	for _, kv := range items {
		id, ok := kv[0].(uint64)
		if !ok {
			continue
		}
		body, ok := kv[1].([]blaze.Field)
		if !ok {
			continue
		}
		r := RaceResult{BlazeID: id, GameType: gameType, Fields: map[string]uint64{}}
		for _, f := range body {
			n, ok := f.Value.(uint64)
			if !ok {
				continue
			}
			r.Fields[f.Tag] = n
			switch f.Tag {
			case "RUN", "RACE", "EVNT", "EVID", "ROAD":
				if r.EventID == 0 {
					r.EventID = n
					r.EventKind = f.Tag
				}
			case "TIME":
				r.TimeMs = n
			case "VEHI":
				r.VehicleID = n
			case "WIN":
				r.Win = n != 0
			case "PBST":
				r.PB = n != 0
			case "FRST":
				r.First = n != 0
			}
		}
		out = append(out, r)
	}
	return out
}

func resultsJSON(rs []RaceResult) string {
	b, err := json.Marshal(rs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func formatRaceTime(ms uint64) string {
	m := ms / 60000
	s := (ms / 1000) % 60
	r := ms % 1000
	return itoa(m) + ":" + pad(s, 2) + "." + pad(r, 3)
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func pad(v uint64, n int) string {
	s := itoa(v)
	for len(s) < n {
		s = "0" + s
	}
	return s
}
