package server

import (
	"encoding/hex"
	"log"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Payloads captured from the game (submitOfflineGameReport).
const (
	liveRunSingle  = "9AECE80000C32DB40700CB0CB4039E1B6507019F978BAD1F9E1B6503CA5CC00500030181897A9B2CF40001B29CF40041C22CF40001CA1D300000CB5B8000ACFDC9AB02D29B65008DBC16DA5A29009BD1E4D212DE9B8000010000009F2A6400009F4E70010B72756E5F73696E676C650000"
	liveRaceSingle = "9AECE80000C32DB40700CB0CB4039E1B650701AFCBDAE1039E1B6503CA5CC00500030181897A874B7000009B2CF40001B29CF40041C22CF40001CA18E500B3CEEB9513D29B6500889210DA5A2900AEFDC3E70ADE9B8000010000009F2A6400009F4E70010C726163655F73696E676C650000"
)

func decodeHex(t *testing.T, h string) ([]byte, []blaze.Field) {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := blaze.DecodeAll(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b, fs
}

func TestParseLiveRunSingle(t *testing.T) {
	_, fs := decodeHex(t, liveRunSingle)
	gt, rs := parseGameReport(fs)
	if gt != "run_single" || len(rs) != 1 {
		t.Fatalf("gt=%q results=%d", gt, len(rs))
	}
	r := rs[0]
	if r.BlazeID != 1000001 || r.EventKind != "RUN" || r.EventID != 314130284 || r.TimeMs != 184077 || r.VehicleID != 2502726747 || !r.Win || !r.PB || !r.First {
		t.Fatalf("unexpected %+v", r)
	}
	if formatRaceTime(r.TimeMs) != "3:04.077" {
		t.Fatalf("time %s", formatRaceTime(r.TimeMs))
	}
}

func TestParseLiveRaceSingle(t *testing.T) {
	_, fs := decodeHex(t, liveRaceSingle)
	gt, rs := parseGameReport(fs)
	if gt != "race_single" || len(rs) != 1 || rs[0].EventKind != "RACE" || rs[0].EventID != 2573038515 || rs[0].TimeMs != 132232 {
		t.Fatalf("gt=%q %+v", gt, rs)
	}
}

func TestStoreGameReportRanksOnlyLoggedInPlayerAndDedupes(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "P"}}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	st := &connState{Persona: fs.per}
	payload, fields := decodeHex(t, liveRunSingle)
	p := &blaze.Packet{Component: ComponentGameReporting, Command: CmdSubmitOfflineGameReport, Payload: payload}

	resp := s.handleGameReporting(p, fields, st)
	if resp.Error != 0 || resp.QType != blaze.TypeResponse {
		t.Fatal("report must always succeed")
	}
	s.handleGameReporting(p, fields, st) // resend of the same queued report
	if len(fs.reports) != 1 {
		t.Fatalf("reports=%d want 1", len(fs.reports))
	}
	if fs.bests["run_single/314130284/1"] != 184077 {
		t.Fatalf("bests=%v", fs.bests)
	}
	out := logs.String()
	if !strings.Contains(out, "new personal best on run_single event 314130284: 3:04.077") || !strings.Contains(out, "duplicate payload ignored") {
		t.Fatalf("log:\n%s", out)
	}

	// A different player's line in the same report is stored but not ranked.
	st2 := &connState{Persona: database.Persona{ID: 2, BlazeID: 999}}
	payload2, fields2 := decodeHex(t, liveRaceSingle)
	s.handleGameReporting(&blaze.Packet{Component: ComponentGameReporting, Command: CmdSubmitOfflineGameReport, Payload: payload2}, fields2, st2)
	if len(fs.bests) != 1 {
		t.Fatalf("foreign result was ranked: %v", fs.bests)
	}
}
