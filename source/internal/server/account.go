package server

import (
	"strconv"

	"nfsrun-server/internal/blaze"
)

// Authentication.updateAccount (0x14).
//
// The account options screen sends UpdateAccountRequest {CPWD, CTRY, DOB,
// LANG, MAIL, OPT1, OPT3, PASS, PRNT, UID}. OPT1 = GlobalOptin ("EA may
// contact me"), OPT3 = ThirdPartyOptin; -1 and empty strings mean unchanged.
//
// The two choices are kept per player (persona_<id>.json, "_globalOptin" /
// "_thirdPartyOptin") and returned by getAccount (AccountInfo GOPT / TPOT).
// Nothing else of the account changes here: e-mail, password, country and
// date of birth are not used by this server (login is the launcher key).
// Reply: UpdateAccountResponse{PCTK} -- a new PC login token on EA's servers;
// the launcher's session ticket stays valid, so it is empty.

func (s *Server) accountKey(state *connState) string {
	return strconv.FormatUint(state.Persona.BlazeID, 10)
}

// accountOptins: the stored GOPT / TPOT (0 until the player chose).
func (s *Server) accountOptins(state *connState) (gopt, tpot uint64) {
	if state.Persona.BlazeID == 0 {
		return 0, 0
	}
	m := s.web.load(s.accountKey(state))
	get := func(k string) uint64 {
		if v, ok := m[k].(float64); ok && (v == 0 || v == 1) {
			return uint64(v)
		}
		return 0
	}
	return get("_globalOptin"), get("_thirdPartyOptin")
}

func (s *Server) handleUpdateAccount(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	if state.Persona.BlazeID == 0 {
		return s.errorResp(p, 0x4004) // ERR_AUTHENTICATION_REQUIRED
	}
	set := map[string]any{}
	for _, f := range fields {
		v, ok := f.Value.(uint64)
		if !ok || v > 1 { // -1 / anything else: unchanged
			continue
		}
		switch f.Tag {
		case "OPT1":
			set["_globalOptin"] = v
		case "OPT3":
			set["_thirdPartyOptin"] = v
		}
	}
	if len(set) > 0 {
		s.web.merge(s.accountKey(state), set)
	}
	for _, tag := range []string{"MAIL", "CTRY", "DOB", "LANG", "PRNT"} {
		if v := blaze.FindString(fields, tag); v != "" {
			s.log.Printf("[AUTH] updateAccount: %s change ignored (not used by this server)", tag)
		}
	}
	s.log.Printf("[AUTH] updateAccount %q: %v", state.Persona.Name, set)
	var w blaze.Writer
	w.TagString("PCTK", "")
	return s.response(p, w.Bytes())
}
