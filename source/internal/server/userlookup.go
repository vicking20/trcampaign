package server

// UserSessions lookups (component 0x7802):
//
//	0x0C lookupUser(UserIdentification)               -> UserData
//	0x0D lookupUsers(LookupUsersRequest{LTYP, ULST})   -> UserDataResponse{ULST list<UserData>}
//	0x0E lookupUsersByPrefix(LookupUsersByPrefixRequest{CAP, PREF}) -> UserDataResponse
//	0x03 fetchExtendedData(FetchExtendedDataRequest{BUID}) -> UserSessionExtendedData
//
// UserData = {EDAT UserSessionExtendedData, FLGS UserDataFlags, USER
// UserIdentification}. LookupType: 0 BLAZE_ID, 1 PERSONA_NAME, 2 EXTERNAL_ID,
// 3 ACCOUNT_ID. Unknown users: USER_ERR_USER_NOT_FOUND for lookupUser, left
// out of the list for lookupUsers.

import (
	"strings"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

const (
	USFetchExtendedData   uint16 = 0x0003
	USLookupUser          uint16 = 0x000C
	USLookupUsers         uint16 = 0x000D
	USLookupUsersByPrefix uint16 = 0x000E

	errUserNotFound uint16 = 0x0001 // USER_ERR_USER_NOT_FOUND (0x0001 << 16 | 0x7802)

	lookupByBlazeID     = 0
	lookupByPersonaName = 1
	lookupByExternalID  = 2
	lookupByAccountID   = 3
)

// knownUser is one player as the server can describe it.
type knownUser struct {
	per    database.Persona
	online *connState
}

// allUsers: every persona in the database plus the online sessions (which
// carry the live network data). Race-server personas are not players.
func (s *Server) allUsers() []knownUser {
	byID := map[uint64]*knownUser{}
	var order []uint64
	if refs, err := s.db.ListPersonas(0, 5000); err == nil {
		for _, r := range refs {
			if isDSPersonaName(r.Name) {
				continue
			}
			p := personaFromRef(r)
			byID[r.ID] = &knownUser{per: p}
			order = append(order, r.ID)
		}
	} else {
		s.log.Printf("[USESS] user list query failed: %v", err)
	}
	for _, st := range s.online.states() {
		if st.Persona.BlazeID == 0 || isDSPersonaName(st.Persona.Name) {
			continue
		}
		u, ok := byID[st.Persona.ID]
		if !ok {
			u = &knownUser{}
			byID[st.Persona.ID] = u
			order = append(order, st.Persona.ID)
		}
		u.per, u.online = st.Persona, st
	}
	out := make([]knownUser, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

func (u knownUser) matches(typ uint64, id uint64, name string) bool {
	switch typ {
	case lookupByPersonaName:
		return name != "" && strings.EqualFold(u.per.Name, name)
	case lookupByExternalID:
		return id != 0 && id == u.per.BlazeID // EXID = BlazeId (writeUserIdentification)
	case lookupByAccountID:
		return id != 0 && id == u.per.AccountID
	default:
		return id != 0 && id == u.per.BlazeID
	}
}

// findUser resolves one UserIdentification-like query.
func (s *Server) findUser(users []knownUser, typ uint64, ident []blaze.Field) (knownUser, bool) {
	var id uint64
	switch typ {
	case lookupByExternalID:
		id = fieldUintAuth(ident, "EXID")
	case lookupByAccountID:
		id = fieldUintAuth(ident, "AID")
	default:
		id = fieldUintAuth(ident, "ID")
	}
	name := blaze.FindString(ident, "NAME")
	for _, u := range users {
		if u.matches(typ, id, name) {
			return u, true
		}
	}
	// lookupUser carries every field it knows: fall back to the name
	if typ == lookupByBlazeID && name != "" {
		for _, u := range users {
			if u.matches(lookupByPersonaName, 0, name) {
				return u, true
			}
		}
	}
	return knownUser{}, false
}

// writeUserDataFields writes UserData's members (EDAT, FLGS, USER).
func (s *Server) writeUserDataFields(w *blaze.Writer, u knownUser) {
	n := netInfo{}
	flags := uint64(0)
	var objs []objectID
	if u.online != nil {
		n = u.online.Net
		flags = userStatusOnline
		objs = s.memberships(u.online, u.per.BlazeID)
	}
	writeUserSessionExtendedDataNet(w, "EDAT", n, objs...)
	w.TagVar("FLGS", flags)
	writeUserIdentification(w, "USER", u.per)
}

func (s *Server) handleUserLookup(p *blaze.Packet, fields []blaze.Field, state *connState) (*blaze.Packet, bool) {
	switch p.Command {
	case USLookupUser:
		u, ok := s.findUser(s.allUsers(), lookupByBlazeID, fields)
		if !ok {
			s.log.Printf("[USESS] lookupUser id=%d name=%q: not found", fieldUintAuth(fields, "ID"), blaze.FindString(fields, "NAME"))
			return s.errorResp(p, errUserNotFound), true
		}
		var w blaze.Writer
		s.writeUserDataFields(&w, u)
		s.log.Printf("[USESS] lookupUser -> %q blazeId=%d online=%t in %v", u.per.Name, u.per.BlazeID, u.online != nil, s.memberships(u.online, u.per.BlazeID))
		return s.response(p, w.Bytes()), true

	case USLookupUsers:
		typ := fieldUintAuth(fields, "LTYP")
		users := s.allUsers()
		var found []knownUser
		for _, ident := range fieldStructList(fields, "ULST") {
			if u, ok := s.findUser(users, typ, ident); ok {
				found = append(found, u)
			}
		}
		s.log.Printf("[USESS] lookupUsers type=%d asked %d -> %d found", typ, len(fieldStructList(fields, "ULST")), len(found))
		return s.response(p, s.buildUserDataResponse(found)), true

	case USLookupUsersByPrefix:
		prefix := strings.ToLower(blaze.FindString(fields, "PREF"))
		limit := int(fieldUintAuth(fields, "CAP"))
		if limit <= 0 || limit > 100 {
			limit = 100
		}
		var found []knownUser
		for _, u := range s.allUsers() {
			if len(found) >= limit {
				break
			}
			if prefix != "" && strings.HasPrefix(strings.ToLower(u.per.Name), prefix) {
				found = append(found, u)
			}
		}
		s.log.Printf("[USESS] lookupUsersByPrefix %q -> %d", prefix, len(found))
		return s.response(p, s.buildUserDataResponse(found)), true

	case USFetchExtendedData:
		id := fieldUintAuth(fields, "BUID")
		for _, u := range s.allUsers() {
			if u.per.BlazeID == id {
				n := netInfo{}
				if u.online != nil {
					n = u.online.Net
				}
				var w blaze.Writer
				writeUserSessionExtendedDataFields(&w, n)
				return s.response(p, w.Bytes()), true
			}
		}
		return s.errorResp(p, errUserNotFound), true
	}
	return nil, false
}

func (s *Server) buildUserDataResponse(users []knownUser) []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "ULST", len(users))
	for _, u := range users {
		s.writeUserDataFields(&w, u)
		w.GroupEnd()
	}
	return w.Bytes()
}
