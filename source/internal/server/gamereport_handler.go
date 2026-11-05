package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"nfsrun-server/internal/blaze"
)

const maxStoredPayloadHex = 16000

func (s *Server) handleGameReporting(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	switch p.Command {
	case CmdSubmitGameReport, CmdSubmitOfflineGameReport, CmdSubmitTrustedEndGameRep:
		s.storeGameReport(p, fields, state)
		return s.response(p, nil) // NullStruct reply
	default:
		s.unimplemented(p, "gamereport_handler.go")
		return s.unimplementedReply(p)
	}
}

// storeGameReport never fails the RPC: a database problem is logged and the
// client still gets success, so it does not retry forever or drop the session.
func (s *Server) storeGameReport(p *blaze.Packet, fields []blaze.Field, state *connState) {
	gameType, results := parseGameReport(fields)
	sum := sha256.Sum256(p.Payload)
	var personaID uint64
	if state.Persona.BlazeID != 0 {
		personaID = state.Persona.ID
	}
	payloadHex := hex.EncodeToString(p.Payload)
	if len(payloadHex) > maxStoredPayloadHex {
		// Keeps the mariadb.exe command line well under Windows' 32 KB limit.
		payloadHex = payloadHex[:maxStoredPayloadHex]
	}
	isNew, err := s.db.SaveGameReport(personaID, gameType, hex.EncodeToString(sum[:]), payloadHex, resultsJSON(results))
	if err != nil {
		s.log.Printf("[REPORT] %s store failed: %v", gameType, err)
		return
	}
	if !isNew {
		s.log.Printf("[REPORT] %s duplicate payload ignored", gameType)
		return
	}
	for _, r := range results {
		mine := state.Persona.BlazeID != 0 && r.BlazeID == state.Persona.BlazeID
		s.log.Printf("[REPORT] %s player=%d %s=%d time=%s vehicle=%d win=%t pb=%t first=%t%s",
			gameType, r.BlazeID, r.EventKind, r.EventID, formatRaceTime(r.TimeMs), r.VehicleID, r.Win, r.PB, r.First,
			map[bool]string{true: "", false: " (not the logged-in player: not ranked)"}[mine])
		if !mine || !r.HasTime() || gameType == "" {
			continue
		}
		meta, _ := json.Marshal(map[string]any{"vehicleId": r.VehicleID, "win": r.Win, "fields": r.Fields})
		better, err := s.db.UpsertPersonalBest(state.Persona.ID, gameType, r.EventID, r.TimeMs, string(meta))
		if err != nil {
			s.log.Printf("[REPORT] leaderboard update failed: %v", err)
			continue
		}
		if better {
			s.log.Printf("[REPORT] new personal best on %s event %d: %s", gameType, r.EventID, formatRaceTime(r.TimeMs))
		}
	}
}
