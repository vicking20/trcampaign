package server

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Playgroups (component 0x0006): the Multiplayer "Group" menu.
//
// Commands: 1 createPlaygroup, 2 destroyPlaygroup, 3 joinPlaygroup,
// 4 leavePlaygroup, 5 setPlaygroupAttributes, 6 setMemberAttributes,
// 7 kickPlaygroupMember, 8 setPlaygroupJoinControls,
// 9 finalizePlaygroupCreation, 10 lookupPlaygroupInfo, 11 resetPlaygroupSession.
// Notifications: 50 NotifyDestroyPlaygroup, 51 NotifyJoinPlaygroup,
// 52 NotifyMemberJoinedPlaygroup, 53 NotifyMemberRemovedFromPlaygroup,
// 54 NotifyPlaygroupAttributesSet, 75 NotifyMemberAttributesSet,
// 79 NotifyLeaderChange, 80 NotifyMemberPermissionsChange,
// 85 NotifyJoinControlsChange.
//
// A group is joined by PGID, user object, user name or UKEY. The group id is
// the creator's Blaze id, the same number the game puts in its Origin
// SessionId, so joining a friend's session finds the group.

const (
	ComponentPlaygroups uint16 = 0x0006

	PGCreate          uint16 = 1
	PGDestroy         uint16 = 2
	PGJoin            uint16 = 3
	PGLeave           uint16 = 4
	PGSetAttributes   uint16 = 5
	PGSetMemberAttrs  uint16 = 6
	PGKick            uint16 = 7
	PGSetJoinControls uint16 = 8
	PGFinalize        uint16 = 9
	PGLookup          uint16 = 10
	PGReset           uint16 = 11

	PGNotifyDestroy            uint16 = 50
	PGNotifyJoin               uint16 = 51
	PGNotifyMemberJoined       uint16 = 52
	PGNotifyMemberRemoved      uint16 = 53
	PGNotifyAttributesSet      uint16 = 54
	PGNotifyMemberAttrsSet     uint16 = 75
	PGNotifyLeaderChange       uint16 = 79
	PGNotifyJoinControlsChange uint16 = 85

	// any non-zero code shows the game's "could not join" message
	pgErrNotFound uint16 = 0x0065
	pgErrFull     uint16 = 0x0066

	pgReasonLeft      uint64 = 0
	pgReasonKicked    uint64 = 1
	pgReasonDestroyed uint64 = 0
	pgPermLeader      uint64 = 0xFF
)

type pgMember struct {
	st     *connState
	per    database.Persona
	net    netInfo
	slot   uint64
	perm   uint64
	joined uint64
	attrs  []kv
}

type playgroup struct {
	id               uint32
	name, ukey, uuid string
	owner            uint64 // leader's Blaze id
	hostSlot         uint64
	mlim, pres, voip uint64
	ntop, join       uint64
	enbv, uprs       bool
	attrs            []kv
	hnet             netInfo
	xnnc, xses       []byte
	members          []*pgMember
}

func (g *playgroup) member(blazeID uint64) *pgMember {
	for _, m := range g.members {
		if m.per.BlazeID == blazeID {
			return m
		}
	}
	return nil
}

// open: can friends still join (join controls and room left)?
func (g *playgroup) open() bool {
	return g.join == 0 && (g.mlim == 0 || uint64(len(g.members)) < g.mlim)
}

type playgroups struct {
	mu sync.Mutex
	by map[uint32]*playgroup
	of map[*connState]*playgroup
}

func (p *playgroups) init() {
	if p.by == nil {
		p.by = map[uint32]*playgroup{}
		p.of = map[*connState]*playgroup{}
	}
}

// ------------------------------------------------------------------ writers

func writePlaygroupInfoBody(w *blaze.Writer, g *playgroup) {
	writeKVMap(w, "ATTR", g.attrs)
	w.TagBool("ENBV", g.enbv)
	writeNetworkAddress(w, "HNET", g.hnet)
	w.TagVar("HSID", g.hostSlot)
	w.TagVar("JOIN", g.join)
	w.TagVar("MLIM", g.mlim)
	w.TagString("NAME", g.name)
	w.TagVar("NTOP", g.ntop)
	w.TagVar("OWNR", g.owner)
	w.TagVar("PGID", uint64(g.id))
	w.TagVar("PRES", g.pres)
	w.TagString("UKEY", g.ukey)
	w.TagBool("UPRS", g.uprs)
	w.TagString("UUID", g.uuid)
	w.TagVar("VOIP", g.voip)
	w.TagBlob("XNNC", g.xnnc)
	w.TagBlob("XSES", g.xses)
}

func writePlaygroupMemberBody(w *blaze.Writer, m *pgMember) {
	writeKVMap(w, "ATTR", m.attrs)
	w.TagVar("JTIM", m.joined)
	w.TagVar("PERM", m.perm)
	writeNetworkAddress(w, "PNET", m.net)
	w.TagVar("SID", m.slot)
	writeUserIdentification(w, "USER", m.per)
}

// JoinPlaygroupResponse{INFO PlaygroupInfo} (create and join).
func buildJoinPlaygroupResponse(g *playgroup) []byte {
	var w blaze.Writer
	w.GroupStart("INFO")
	writePlaygroupInfoBody(&w, g)
	w.GroupEnd()
	return w.Bytes()
}

// NotifyJoinPlaygroup{INFO, MLST list<PlaygroupMemberInfo>, USER int64}.
func buildNotifyJoinPlaygroup(g *playgroup, user uint64) []byte {
	var w blaze.Writer
	w.GroupStart("INFO")
	writePlaygroupInfoBody(&w, g)
	w.GroupEnd()
	writeStructListHeader(&w, "MLST", len(g.members))
	for _, m := range g.members {
		writePlaygroupMemberBody(&w, m)
		w.GroupEnd()
	}
	w.TagVar("USER", user)
	return w.Bytes()
}

// NotifyMemberJoinedPlaygroup{MEMB PlaygroupMemberInfo, PGID}.
func buildNotifyMemberJoined(g *playgroup, m *pgMember) []byte {
	var w blaze.Writer
	w.GroupStart("MEMB")
	writePlaygroupMemberBody(&w, m)
	w.GroupEnd()
	w.TagVar("PGID", uint64(g.id))
	return w.Bytes()
}

// NotifyMemberRemoveFromPlaygroup{MLST int64, PGID, REAS}.
func buildNotifyMemberRemoved(g *playgroup, who, reason uint64) []byte {
	var w blaze.Writer
	w.TagVar("MLST", who)
	w.TagVar("PGID", uint64(g.id))
	w.TagVar("REAS", reason)
	return w.Bytes()
}

// NotifyDestroyPlaygroup{PGID, REAS}.
func buildNotifyDestroyPlaygroup(id uint32, reason uint64) []byte {
	var w blaze.Writer
	w.TagVar("PGID", uint64(id))
	w.TagVar("REAS", reason)
	return w.Bytes()
}

// NotifyLeaderChange{HSID, LID, PGID}.
func buildNotifyLeaderChange(g *playgroup) []byte {
	var w blaze.Writer
	w.TagVar("HSID", g.hostSlot)
	w.TagVar("LID", g.owner)
	w.TagVar("PGID", uint64(g.id))
	return w.Bytes()
}

// ------------------------------------------------------------------ parsing

func fieldGroupOf(fs []blaze.Field, tag string) []blaze.Field {
	for _, f := range fs {
		if f.Tag == tag {
			g, _ := f.Value.([]blaze.Field)
			return g
		}
	}
	return nil
}

func fieldBlob(fs []blaze.Field, tag string) []byte {
	for _, f := range fs {
		if f.Tag == tag {
			b, _ := f.Value.([]byte)
			return b
		}
	}
	return nil
}

// ------------------------------------------------------------------ handler

func (s *Server) handlePlaygroups(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	name := blaze.CommandName(p.Component, p.Command)
	pg := &s.pg
	switch p.Command {
	case PGCreate:
		info := fieldGroupOf(fields, "PGRP")
		s.leavePlaygroup(state, pgReasonLeft) // one group per player
		pg.mu.Lock()
		pg.init()
		id := uint32(state.Persona.BlazeID)
		for id == 0 || pg.by[id] != nil {
			id += 0x10000000
		}
		netw, _ := parseNetworkAddress(info, "HNET")
		if !netw.Set {
			netw = state.Net
		}
		g := &playgroup{
			id: id, name: blaze.FindString(info, "NAME"), ukey: blaze.FindString(info, "UKEY"), uuid: blaze.FindString(info, "UUID"),
			owner: state.Persona.BlazeID, mlim: fieldUintAuth(info, "MLIM"), pres: fieldUintAuth(info, "PRES"),
			voip: fieldUintAuth(info, "VOIP"), ntop: fieldUintAuth(info, "NTOP"), join: fieldUintAuth(info, "JOIN"),
			enbv: fieldUintAuth(info, "ENBV") != 0, uprs: fieldUintAuth(info, "UPRS") != 0,
			attrs: mapKV(info, "ATTR"), hnet: netw, xnnc: fieldBlob(info, "XNNC"), xses: fieldBlob(info, "XSES"),
		}
		if g.name == "" {
			g.name = state.Persona.Name
		}
		if g.ukey == "" {
			g.ukey = fmt.Sprintf("pg-%d-%d", id, time.Now().Unix())
		}
		if g.uuid == "" {
			g.uuid = g.ukey
		}
		if g.mlim == 0 || g.mlim > 16 {
			g.mlim = 4
		}
		g.members = []*pgMember{{st: state, per: state.Persona, net: netw, slot: 0, perm: pgPermLeader, joined: uint64(time.Now().Unix())}}
		pg.by[id], pg.of[state] = g, g
		resp := buildJoinPlaygroupResponse(g)
		notify := buildNotifyJoinPlaygroup(g, state.Persona.BlazeID)
		pg.mu.Unlock()
		s.hub.kick() // presence: joinability changed
		s.log.Printf("[PG] %s: group %d %q created by %q (max %d, join=%d)", name, id, g.name, state.Persona.Name, g.mlim, g.join)
		state.Pending = append(state.Pending, notifyPacket(ComponentPlaygroups, PGNotifyJoin, notify))
		return s.response(p, resp)

	case PGJoin:
		user := fieldGroupOf(fields, "USER")
		uid, uname := fieldUintAuth(user, "ID"), blaze.FindString(user, "NAME")
		want, ukey := uint32(fieldUintAuth(fields, "PGID")), blaze.FindString(fields, "UKEY")
		netw, _ := parseNetworkAddress(fields, "PNET")
		if !netw.Set {
			netw = state.Net
		}
		pg.mu.Lock()
		pg.init()
		g := pg.find(want, ukey, uid, uname)
		if g == nil {
			pg.mu.Unlock()
			s.log.Printf("[PG] %s by %q: no group (PGID=%d UKEY=%q USER=%d/%q)", name, state.Persona.Name, want, ukey, uid, uname)
			return s.errorResp(p, pgErrNotFound)
		}
		if old := pg.of[state]; old == g {
			resp := buildJoinPlaygroupResponse(g)
			notify := buildNotifyJoinPlaygroup(g, state.Persona.BlazeID)
			pg.mu.Unlock()
			state.Pending = append(state.Pending, notifyPacket(ComponentPlaygroups, PGNotifyJoin, notify))
			return s.response(p, resp)
		}
		if g.mlim != 0 && uint64(len(g.members)) >= g.mlim {
			pg.mu.Unlock()
			s.log.Printf("[PG] %s by %q: group %d is full", name, state.Persona.Name, g.id)
			return s.errorResp(p, pgErrFull)
		}
		pg.mu.Unlock()
		s.leavePlaygroup(state, pgReasonLeft)
		pg.mu.Lock()
		if pg.by[g.id] != g { // vanished meanwhile
			pg.mu.Unlock()
			return s.errorResp(p, pgErrNotFound)
		}
		used := map[uint64]bool{}
		for _, m := range g.members {
			used[m.slot] = true
		}
		slot := uint64(0)
		for used[slot] {
			slot++
		}
		m := &pgMember{st: state, per: state.Persona, net: netw, slot: slot, joined: uint64(time.Now().Unix())}
		others := g.memberStates()
		g.members = append(g.members, m)
		pg.of[state] = g
		resp := buildJoinPlaygroupResponse(g)
		notify := buildNotifyJoinPlaygroup(g, state.Persona.BlazeID)
		joined := buildNotifyMemberJoined(g, m)
		pg.mu.Unlock()
		s.hub.kick() // presence: joinability changed
		s.log.Printf("[PG] %s: %q joined group %d (%d/%d)", name, state.Persona.Name, g.id, len(others)+1, g.mlim)
		for _, st := range others {
			s.send(st, notifyPacket(ComponentPlaygroups, PGNotifyMemberJoined, joined), true)
		}
		state.Pending = append(state.Pending, notifyPacket(ComponentPlaygroups, PGNotifyJoin, notify))
		return s.response(p, resp)

	case PGLeave:
		s.leavePlaygroup(state, pgReasonLeft)
		return s.response(p, nil)

	case PGDestroy:
		pg.mu.Lock()
		g := pg.of[state]
		if want := uint32(fieldUintAuth(fields, "PGID")); want != 0 && pg.by[want] != nil {
			g = pg.by[want]
		}
		pg.mu.Unlock()
		if g != nil {
			s.destroyPlaygroup(g, pgReasonDestroyed)
		}
		return s.response(p, nil)

	case PGSetAttributes:
		g := s.pgOf(state, fields)
		if g == nil {
			return s.response(p, nil)
		}
		add := mapKV(fields, "ATTR")
		pg.mu.Lock()
		g.attrs = setKV(g.attrs, add)
		sts := g.memberStates()
		pg.mu.Unlock()
		var w blaze.Writer
		writeKVMap(&w, "ATTR", add)
		w.TagVar("PGID", uint64(g.id))
		s.log.Printf("[PG] %s: group %d %v", name, g.id, add)
		s.notifyMembers(state, sts, PGNotifyAttributesSet, w.Bytes())
		return s.response(p, nil)

	case PGSetMemberAttrs:
		g := s.pgOf(state, fields)
		if g == nil {
			return s.response(p, nil)
		}
		add := mapKV(fields, "ATTR")
		eid := fieldUintAuth(fields, "EID")
		if eid == 0 {
			eid = state.Persona.BlazeID
		}
		pg.mu.Lock()
		if m := g.member(eid); m != nil {
			m.attrs = setKV(m.attrs, add)
		}
		sts := g.memberStates()
		pg.mu.Unlock()
		var w blaze.Writer
		writeKVMap(&w, "ATTR", add)
		w.TagVar("EID", eid)
		w.TagVar("PGID", uint64(g.id))
		s.notifyMembers(state, sts, PGNotifyMemberAttrsSet, w.Bytes())
		return s.response(p, nil)

	case PGSetJoinControls:
		g := s.pgOf(state, fields)
		if g == nil {
			return s.response(p, nil)
		}
		open := fieldUintAuth(fields, "OPEN")
		pg.mu.Lock()
		g.join = open
		sts := g.memberStates()
		pg.mu.Unlock()
		var w blaze.Writer
		w.TagVar("OPEN", open)
		w.TagVar("PGID", uint64(g.id))
		s.hub.kick() // presence: joinability changed
		s.log.Printf("[PG] %s: group %d join controls %d", name, g.id, open)
		s.notifyMembers(state, sts, PGNotifyJoinControlsChange, w.Bytes())
		return s.response(p, nil)

	case PGKick:
		g := s.pgOf(state, fields)
		eid := fieldUintAuth(fields, "EID")
		if g == nil || eid == 0 {
			return s.response(p, nil)
		}
		pg.mu.Lock()
		m := g.member(eid)
		pg.mu.Unlock()
		if m != nil && m.st != nil {
			s.leavePlaygroup(m.st, pgReasonKicked)
		}
		return s.response(p, nil)

	case PGLookup:
		var ids []uint64
		ids = uintList(fields, "PLST")
		pg.mu.Lock()
		pg.init()
		var w blaze.Writer
		var found []*playgroup
		for _, id := range ids {
			if g := pg.by[uint32(id)]; g != nil {
				found = append(found, g)
			}
		}
		writeStructListHeader(&w, "PGPS", len(found))
		for _, g := range found {
			writePlaygroupInfoBody(&w, g)
			w.GroupEnd()
		}
		pg.mu.Unlock()
		return s.response(p, w.Bytes())

	case PGFinalize, PGReset:
		return s.response(p, nil)
	}
	s.unimplemented(p, "Playgroups")
	return s.unimplementedReply(p)
}

func (g *playgroup) memberStates() []*connState {
	out := make([]*connState, 0, len(g.members))
	for _, m := range g.members {
		if m.st != nil {
			out = append(out, m.st)
		}
	}
	return out
}

// find: PGID, then UKEY, then a member by Blaze id (the Origin SessionId is
// the creator's id = PGID), then a member by name.
func (p *playgroups) find(id uint32, ukey string, uid uint64, uname string) *playgroup {
	if g := p.by[id]; id != 0 && g != nil {
		return g
	}
	if ukey != "" {
		for _, g := range p.by {
			if g.ukey == ukey {
				return g
			}
		}
	}
	for _, key := range []uint64{uint64(id), uid} {
		if key == 0 {
			continue
		}
		if g := p.by[uint32(key)]; g != nil {
			return g
		}
		for _, g := range p.by {
			if g.member(key) != nil {
				return g
			}
		}
	}
	if uname != "" {
		for _, g := range p.by {
			for _, m := range g.members {
				if strings.EqualFold(m.per.Name, uname) {
					return g
				}
			}
		}
	}
	return nil
}

func (s *Server) pgOf(state *connState, fields []blaze.Field) *playgroup {
	s.pg.mu.Lock()
	defer s.pg.mu.Unlock()
	s.pg.init()
	if id := uint32(fieldUintAuth(fields, "PGID")); id != 0 {
		if g := s.pg.by[id]; g != nil {
			return g
		}
	}
	return s.pg.of[state]
}

// notifyMembers: the caller gets it after its response, the others now.
func (s *Server) notifyMembers(self *connState, sts []*connState, cmd uint16, payload []byte) {
	for _, st := range sts {
		pkt := notifyPacket(ComponentPlaygroups, cmd, payload)
		if st == self {
			self.Pending = append(self.Pending, pkt)
		} else {
			s.send(st, pkt, true)
		}
	}
}

// leavePlaygroup removes state from its group (leave, kick, disconnect). The
// leader leaving hands the group to the next member; the last one destroys it.
func (s *Server) leavePlaygroup(state *connState, reason uint64) {
	pg := &s.pg
	pg.mu.Lock()
	pg.init()
	g := pg.of[state]
	if g == nil {
		pg.mu.Unlock()
		return
	}
	delete(pg.of, state)
	who := state.Persona.BlazeID
	var rest []*pgMember
	for _, m := range g.members {
		if m.st != state {
			rest = append(rest, m)
		}
	}
	g.members = rest
	if len(rest) == 0 {
		delete(pg.by, g.id)
		pg.mu.Unlock()
		s.hub.kick() // presence: joinability changed
		s.log.Printf("[PG] %q left group %d: group closed", state.Persona.Name, g.id)
		s.send(state, notifyPacket(ComponentPlaygroups, PGNotifyDestroy, buildNotifyDestroyPlaygroup(g.id, pgReasonDestroyed)), true)
		return
	}
	newLeader := false
	if g.owner == who {
		sort.Slice(rest, func(i, j int) bool { return rest[i].joined < rest[j].joined })
		g.owner, g.hostSlot, rest[0].perm = rest[0].per.BlazeID, rest[0].slot, pgPermLeader
		g.hnet = rest[0].net
		newLeader = true
	}
	removed := buildNotifyMemberRemoved(g, who, reason)
	leader := buildNotifyLeaderChange(g)
	sts := g.memberStates()
	pg.mu.Unlock()
	s.hub.kick() // presence: joinability changed
	s.log.Printf("[PG] %q left group %d (reason %d), %d left", state.Persona.Name, g.id, reason, len(sts))
	s.send(state, notifyPacket(ComponentPlaygroups, PGNotifyMemberRemoved, removed), true)
	for _, st := range sts {
		s.send(st, notifyPacket(ComponentPlaygroups, PGNotifyMemberRemoved, removed), true)
		if newLeader {
			s.send(st, notifyPacket(ComponentPlaygroups, PGNotifyLeaderChange, leader), true)
		}
	}
}

func (s *Server) destroyPlaygroup(g *playgroup, reason uint64) {
	pg := &s.pg
	pg.mu.Lock()
	if pg.by[g.id] != g {
		pg.mu.Unlock()
		return
	}
	delete(pg.by, g.id)
	sts := g.memberStates()
	for _, st := range sts {
		delete(pg.of, st)
	}
	g.members = nil
	pg.mu.Unlock()
	s.hub.kick() // presence: joinability changed
	s.log.Printf("[PG] group %d destroyed", g.id)
	for _, st := range sts {
		s.send(st, notifyPacket(ComponentPlaygroups, PGNotifyDestroy, buildNotifyDestroyPlaygroup(g.id, reason)), true)
	}
}

// pgStatus: group id and joinability of a player (by Blaze id) for presence.
func (s *Server) pgStatus(blazeID uint64) (id uint32, joinable bool, ok bool) {
	s.pg.mu.Lock()
	defer s.pg.mu.Unlock()
	for _, g := range s.pg.by {
		if g.member(blazeID) != nil {
			return g.id, g.open(), true
		}
	}
	return 0, false, false
}
