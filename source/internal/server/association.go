package server

import (
	"strings"
	"sync"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// AssociationLists (component 0x0019): friend lists (TYPE 1) and recent
// players (TYPE 2). With FRIENDS_ALL every other player is a friend.
//
// Lists { LMAP list<ListMembers{ INFO ListInfo, MEML list<ListMemberInfo{LMID ListMemberId{BLID,PNAM,XREF,XTYP}, TIME}>, OFRC, TOCT }> }

const (
	ComponentAssociationLists uint16 = 0x0019
	ALAddUsersToList          uint16 = 1
	ALRemoveUsersFromList     uint16 = 2
	ALGetLists                uint16 = 6
	ALGetListForUser          uint16 = 5
)

type listRequest struct {
	BOID     [3]uint64
	Flags    uint64
	Name     string
	Type     uint64
	MaxSize  uint64
	PairedID uint64
}

func parseListInfos(fs []blaze.Field) []listRequest {
	var out []listRequest
	for _, li := range fieldStructList(fs, "ALST") {
		r := listRequest{Flags: fieldUintAuth(li, "FLGS"), MaxSize: fieldUintAuth(li, "LMS"), PairedID: fieldUintAuth(li, "PRID")}
		for _, f := range li {
			switch f.Tag {
			case "BOID":
				if t, ok := f.Value.([3]uint64); ok {
					r.BOID = t
				}
			case "LID":
				if g, ok := f.Value.([]blaze.Field); ok {
					r.Name = blaze.FindString(g, "LNM")
					r.Type = fieldUintAuth(g, "TYPE")
				}
			}
		}
		out = append(out, r)
	}
	return out
}

type listMember struct {
	BlazeID uint64
	Name    string
}

func writeListInfo(w *blaze.Writer, tag string, r listRequest) {
	w.GroupStart(tag)
	w.TagTriple("BOID", r.BOID[0], r.BOID[1], r.BOID[2])
	w.TagVar("FLGS", r.Flags)
	w.GroupStart("LID")
	w.TagString("LNM", r.Name)
	w.TagVar("TYPE", r.Type)
	w.GroupEnd()
	w.TagVar("LMS", r.MaxSize)
	w.TagVar("PRID", r.PairedID)
	w.GroupEnd()
}

func buildLists(reqs []listRequest, members map[int][]listMember) []byte {
	var w blaze.Writer
	writeStructListHeader(&w, "LMAP", len(reqs))
	for i, r := range reqs {
		ms := members[i]
		writeListInfo(&w, "INFO", r)
		writeStructListHeader(&w, "MEML", len(ms))
		for _, m := range ms {
			w.GroupStart("LMID")
			w.TagVar("BLID", m.BlazeID)
			w.TagString("PNAM", m.Name)
			w.TagVar("XREF", m.BlazeID)
			w.TagVar("XTYP", 0)
			w.GroupEnd()
			w.TagVar("TIME", 0)
			w.GroupEnd()
		}
		w.TagVar("OFRC", 0)
		w.TagVar("TOCT", uint64(len(ms)))
		w.GroupEnd()
	}
	return w.Bytes()
}

func (s *Server) handleAssociationLists(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	name := blaze.CommandName(p.Component, p.Command)
	switch p.Command {
	case ALAddUsersToList, ALRemoveUsersFromList:
		return s.response(p, s.updateListMembers(p.Command == ALAddUsersToList, fields, state))
	case ALGetListForUser:
		return s.response(p, s.listForUser(fields, state))
	case ALGetLists:
	default:
		s.unimplemented(p, "AssociationLists")
		return s.unimplementedReply(p)
	}
	reqs := parseListInfos(fields)
	members := map[int][]listMember{}
	for i, r := range reqs {
		switch {
		case r.Type == 2:
			// type 2: players met in races, newest first
			members[i] = s.recent.list(state.Persona.ID)
			continue
		case r.Type != 1:
			continue
		}
		limit := int(r.MaxSize)
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		refs, err := s.friendRefs(state.Persona.ID) // friends table
		if len(refs) > limit {
			refs = refs[:limit]
		}
		if err != nil {
			s.log.Printf("[ASSOC] friend list query failed: %v", err)
			continue
		}
		for _, ref := range refs {
			if isDSPersonaName(ref.Name) {
				continue // race servers are not players
			}
			per := personaFromRef(ref)
			members[i] = append(members[i], listMember{BlazeID: per.BlazeID, Name: ref.Name})
		}
	}
	for i, r := range reqs {
		s.log.Printf("[ASSOC] %s: list type=%d name=%q -> %d members", name, r.Type, r.Name, len(members[i]))
	}
	// Every member of every list must be known to the client's UserManager: it
	// waits (waitForAllUsers) until each one exists before the Autolog screens
	// continue.
	var all []listMember
	for i := range reqs {
		all = append(all, members[i]...)
	}
	state.Pending = append(state.Pending, s.listMemberUsers(state, all)...)
	return s.response(p, buildLists(reqs, members))
}

// recentPlayers remembers who raced together (AssociationLists type 2).
type recentPlayers struct {
	mu sync.Mutex
	m  map[uint64][]listMember // persona id -> newest first
}

func (rp *recentPlayers) list(personaID uint64) []listMember {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return append([]listMember(nil), rp.m[personaID]...)
}

// met records that the given players were in one game.
func (rp *recentPlayers) met(players []database.Persona, wire func(database.Persona) uint64) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.m == nil {
		rp.m = map[uint64][]listMember{}
	}
	for _, a := range players {
		for _, b := range players {
			if a.ID == b.ID || isDSPersonaName(a.Name) || isDSPersonaName(b.Name) {
				continue
			}
			lst := []listMember{{BlazeID: wire(b), Name: b.Name}}
			for _, o := range rp.m[a.ID] {
				if o.BlazeID != wire(b) {
					lst = append(lst, o)
				}
			}
			if len(lst) > 50 {
				lst = lst[:50]
			}
			rp.m[a.ID] = lst
		}
	}
}

// addUsersToList / removeUsersFromList: UpdateListMembersRequest{BIDL, LID,
// MUTA, VALA, VALD} -> UpdateListMembersResponse{LMID, REM}. With FRIENDS_ALL
// everyone is already a friend: an add returns the resolved members, a remove
// the removed ones.
func (s *Server) updateListMembers(add bool, fields []blaze.Field, state *connState) []byte {
	users := s.allUsers()
	var resolved []listMember
	for _, m := range fieldStructList(fields, "BIDL") {
		id, nm := fieldUintAuth(m, "BLID"), blaze.FindString(m, "PNAM")
		for _, u := range users {
			if (id != 0 && u.per.BlazeID == id) || (id == 0 && nm != "" && strings.EqualFold(u.per.Name, nm)) {
				if u.per.BlazeID != state.Persona.BlazeID {
					resolved = append(resolved, listMember{BlazeID: u.per.BlazeID, Name: u.per.Name})
				}
				break
			}
		}
	}
	if add {
		// the client waits for these users as in getLists
		state.Pending = append(state.Pending, s.listMemberUsers(state, resolved)...)
	}
	var w blaze.Writer
	if add {
		writeStructListHeader(&w, "LMID", len(resolved))
		for _, m := range resolved {
			writeListMemberID(&w, "LMID", m)
			w.TagVar("TIME", 0)
			w.GroupEnd()
		}
		w.EmptyList("REM", blaze.TDFGroup)
	} else {
		w.EmptyList("LMID", blaze.TDFGroup)
		writeStructListHeader(&w, "REM", len(resolved))
		for _, m := range resolved {
			writeListMemberIDFields(&w, m)
			w.GroupEnd()
		}
	}
	s.log.Printf("[ASSOC] %s %d of %d members ", map[bool]string{true: "addUsersToList", false: "removeUsersFromList"}[add], len(resolved), len(fieldStructList(fields, "BIDL")))
	return w.Bytes()
}

// getListForUser(GetListForUserRequest{BID, LID, MXRC, OFRC}) -> ListMembers.
func (s *Server) listForUser(fields []blaze.Field, state *connState) []byte {
	var r listRequest
	for _, f := range fields {
		if g, ok := f.Value.([]blaze.Field); ok && f.Tag == "LID" {
			r.Name, r.Type = blaze.FindString(g, "LNM"), fieldUintAuth(g, "TYPE")
		}
	}
	owner := fieldUintAuth(fields, "BID")
	var members []listMember
	for _, u := range s.allUsers() {
		if u.per.BlazeID != owner && (r.Type == 1 && s.cfg.FriendsAll) {
			members = append(members, listMember{BlazeID: u.per.BlazeID, Name: u.per.Name})
		}
	}
	var w blaze.Writer
	writeListInfo(&w, "INFO", r)
	writeStructListHeader(&w, "MEML", len(members))
	for _, m := range members {
		writeListMemberID(&w, "LMID", m)
		w.TagVar("TIME", 0)
		w.GroupEnd()
	}
	w.TagVar("OFRC", 0)
	w.TagVar("TOCT", uint64(len(members)))
	return w.Bytes()
}

func writeListMemberID(w *blaze.Writer, tag string, m listMember) {
	w.GroupStart(tag)
	writeListMemberIDFields(w, m)
	w.GroupEnd()
}

func writeListMemberIDFields(w *blaze.Writer, m listMember) {
	w.TagVar("BLID", m.BlazeID)
	w.TagString("PNAM", m.Name)
	w.TagVar("XREF", m.BlazeID)
	w.TagVar("XTYP", 0)
}
