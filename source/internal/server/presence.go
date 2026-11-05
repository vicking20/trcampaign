package server

import (
	"nfsrun-server/internal/blaze"
)

// Blaze presence for friends. A Blaze 3 server tells every user about
// the online members of their lists: UserSessions NotifyUserAdded (extended
// data + identification) and NotifyUserUpdated (FLGS online/offline).

func (o *onlineSet) states() []*connState {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]*connState, 0, len(o.by))
	for st := range o.by {
		out = append(out, st)
	}
	return out
}

func userOnlinePackets(st *connState) []*blaze.Packet {
	return []*blaze.Packet{
		notifyPacket(ComponentUserSessions, NotifyUserAdded, buildNotifyUserAdded(st.Persona)),
		notifyPacket(ComponentUserSessions, NotifyUserUpdated, buildUserStatus(st.Persona, userStatusOnline)),
	}
}

// announceOnline tells the other players that self came online.
func (s *Server) announceOnline(self *connState) {
	if !s.cfg.FriendsAll || isDSPersonaName(self.Persona.Name) {
		return
	}
	for _, st := range s.online.states() {
		if st == self || !st.Authenticated || st.Persona.BlazeID == self.Persona.BlazeID || isDSPersonaName(st.Persona.Name) {
			continue
		}
		for _, p := range userOnlinePackets(self) {
			s.send(st, p, true)
		}
	}
}

// announceOffline tells the other players that self went away.
func (s *Server) announceOffline(self *connState) {
	if !s.cfg.FriendsAll || self.Persona.BlazeID == 0 || isDSPersonaName(self.Persona.Name) {
		return
	}
	for _, st := range s.online.states() {
		if st == self || !st.Authenticated || st.Persona.BlazeID == self.Persona.BlazeID {
			continue
		}
		s.send(st, notifyPacket(ComponentUserSessions, NotifyUserUpdated, buildUserStatus(self.Persona, 0)), true)
	}
}

// listMemberUsers: UserSessions UserAdded (+ UserUpdated with the online
// flags) for each list member, online or not, so the client's Blaze
// UserManager knows them (see handleAssociationLists). Online members carry
// their live session data; offline ones are announced with FLGS 0.
func (s *Server) listMemberUsers(self *connState, members []listMember) []*blaze.Packet {
	if len(members) == 0 {
		return nil
	}
	users := s.allUsers()
	byID := map[uint64]knownUser{}
	for _, u := range users {
		byID[u.per.BlazeID] = u
	}
	seen := map[uint64]bool{}
	var out []*blaze.Packet
	for _, m := range members {
		if m.BlazeID == 0 || seen[m.BlazeID] || m.BlazeID == self.Persona.BlazeID {
			continue
		}
		seen[m.BlazeID] = true
		u, ok := byID[m.BlazeID]
		if !ok {
			continue
		}
		if u.online != nil {
			out = append(out, userOnlinePackets(u.online)...)
			continue
		}
		out = append(out,
			notifyPacket(ComponentUserSessions, NotifyUserAdded, buildNotifyUserAdded(u.per)),
			notifyPacket(ComponentUserSessions, NotifyUserUpdated, buildUserStatus(u.per, 0)))
	}
	if len(out) > 0 {
		s.log.Printf("[ASSOC] announced %d list member(s) to the client's user manager", len(out)/2)
	}
	return out
}
