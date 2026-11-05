package server

// Playgroups in games, and joining a player's game.
//
// A playgroup leader's startMatchmaking / joinGame carries the group as BTPL
// (BlazeObjectId 6/1/<pgid>). Every other member is put in the same game and
// gets NotifyGameSetup with IndirectMatchmakingSetupContext or
// IndirectJoinGameSetupContext {GRID = the group}; the client checks GRID
// against its own playgroup (0x007F7700, "onUserGroupJoinGame").
//
// joinGame with GID 0 and USER is "join this player's game": the friends list
// "join" and accepted invites (fb::BlazeClientGameManager::joinPlayer).

import (
	"fmt"
	"strings"

	"nfsrun-server/internal/blaze"
)

const (
	gmErrInvalidGameID  uint16 = 0x0002 // GAMEMANAGER_ERR_INVALID_GAME_ID
	gmErrGameFull       uint16 = 0x0004 // GAMEMANAGER_ERR_GAME_FULL
	gmErrPlayerNotFound uint16 = 0x0065 // GAMEMANAGER_ERR_PLAYER_NOT_FOUND
)

func fieldTriple(fs []blaze.Field, tag string) objectID {
	for _, f := range fs {
		if f.Tag == tag {
			if v, ok := f.Value.([3]uint64); ok {
				return objectID(v)
			}
		}
	}
	return objectID{}
}

// groupOf: the playgroup named by BTPL when state is in it, and its other
// online members. Their own matchmaking (if any) is stopped.
func (s *Server) groupOf(state *connState, btpl objectID) (objectID, []*connState) {
	if btpl[0] != uint64(ComponentPlaygroups) || btpl[2] == 0 {
		return objectID{}, nil
	}
	s.pg.mu.Lock()
	s.pg.init()
	g := s.pg.by[uint32(btpl[2])]
	var members []*connState
	if g != nil && g.member(state.Persona.BlazeID) != nil {
		for _, m := range g.members {
			if m.st != nil && m.st != state {
				members = append(members, m.st)
			}
		}
	}
	s.pg.mu.Unlock()
	if g == nil {
		return objectID{}, nil
	}
	for _, m := range members {
		s.endMatchmaking(m, 0)
	}
	return playgroupObject(g.id), members
}

func groupNote(group objectID, members []*connState) string {
	if group[2] == 0 {
		return ""
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Persona.Name)
	}
	return fmt.Sprintf(", playgroup %d with %v", group[2], names)
}

func playerNames(gps []*gamePlayer) []string {
	out := make([]string, 0, len(gps))
	for _, gp := range gps {
		out = append(out, gp.state.Persona.Name)
	}
	return out
}

// addSessionLocked adds a matchmaking session's player and its playgroup
// members to g (gm.mu held). silent: players joining together with them.
func (s *Server) addSessionLocked(g *game, mm *matchmakingSession, silent ...*gamePlayer) []*gamePlayer {
	leader := s.addPlayerLocked(g, mm.state, mm.req.PAttr, mm.req.Net, mm.msid, silent...)
	leader.group = mm.group
	gps := []*gamePlayer{leader}
	for _, st := range mm.members {
		if st.game == g && g.player(st) != nil {
			continue
		}
		gp := s.addPlayerLocked(g, st, mm.req.PAttr, st.Net, mm.msid, append(append([]*gamePlayer(nil), silent...), gps...)...)
		gp.group, gp.indirect, gp.leader = mm.group, true, mm.state.Persona.BlazeID
		gps = append(gps, gp)
	}
	return gps
}

// gameOfPlayerLocked: the game a player (by Blaze id or name) is in.
func (s *Server) gameOfPlayerLocked(id uint64, name string) (*game, bool) {
	found := false
	for _, st := range s.online.states() {
		if isDSPersonaName(st.Persona.Name) {
			continue
		}
		if (id != 0 && st.Persona.BlazeID == id) || (name != "" && strings.EqualFold(st.Persona.Name, name)) {
			found = true
			if g := st.game; g != nil && s.gm.games[g.id] == g && g.player(st) != nil {
				return g, true
			}
		}
	}
	return nil, found
}

// joinGame: join a game by id (browser) or the game of a player (GID 0 +
// USER: friends list "join", accepted invites), with the joiner's playgroup.
func (s *Server) joinGame(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	gid := uint32(fieldUintAuth(fields, "GID"))
	user := fieldGroupOf(fields, "USER")
	uid, uname := fieldUintAuth(user, "ID"), blaze.FindString(user, "NAME")
	pnet, _ := parseNetworkAddress(fields, "PNET")
	if !pnet.Set {
		pnet = state.Net
	}
	group, members := s.groupOf(state, fieldTriple(fields, "BTPL"))
	s.endMatchmaking(state, 0)
	target := fmt.Sprintf("game %d", gid)
	if gid == 0 {
		target = fmt.Sprintf("the game of %q (%d)", uname, uid)
	}

	s.gm.mu.Lock()
	var g *game
	if gid != 0 {
		g = s.gm.games[gid]
	} else if uid != 0 || uname != "" {
		var known bool
		g, known = s.gameOfPlayerLocked(uid, uname)
		if g == nil {
			s.gm.mu.Unlock()
			code := gmErrInvalidGameID
			if !known {
				code = gmErrPlayerNotFound
			}
			s.log.Printf("[GAME] joinGame: %q -> %s refused (%s)", state.Persona.Name, target, map[bool]string{true: "not in a game", false: "player not online"}[known])
			return s.errorResp(p, code)
		}
	}
	if g == nil {
		s.gm.mu.Unlock()
		s.log.Printf("[GAME] joinGame: %q -> %s refused (no such game)", state.Persona.Name, target)
		return s.errorResp(p, gmErrInvalidGameID)
	}
	if g.player(state) != nil {
		s.gm.mu.Unlock()
		s.log.Printf("[GAME] joinGame: %q is already in game %d", state.Persona.Name, g.id)
		return s.response(p, joinGameResponse(g.id))
	}
	if uint64(len(g.players)+1+len(members)) > g.req.PCap {
		s.gm.mu.Unlock()
		s.log.Printf("[GAME] joinGame: %q -> %s refused (game %d is full)", state.Persona.Name, target, g.id)
		return s.errorResp(p, gmErrGameFull)
	}
	jgp := s.addPlayerLocked(g, state, mapKV(fields, "ATTR"), pnet, 0)
	jgp.group = group
	jgp.reason = &setupReason{key: reasonDataless, dataless: datalessJoinGame}
	gps := []*gamePlayer{jgp}
	for _, st := range members {
		if st.game == g && g.player(st) != nil {
			continue
		}
		gp := s.addPlayerLocked(g, st, nil, st.Net, 0, gps...)
		gp.group, gp.indirect, gp.leader = group, true, state.Persona.BlazeID
		gp.reason = &setupReason{key: reasonIndirectJoin, grid: group}
		gps = append(gps, gp)
	}
	held := !g.setupSince.IsZero()
	for _, gp := range gps {
		if held {
			gp.held = true
			continue
		}
		s.gm.out = append(s.gm.out, outMsg{gp.state, notifyPacket(ComponentGameManager, GMNotifyGameSetup, s.setupFor(g, gp, *gp.reason))})
	}
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	note := ""
	if held {
		note = " (held until the race server is in PRE_GAME)"
	}
	s.log.Printf("[GAME] joinGame: %v joined %s = game %d (%s)%s", playerNames(gps), target, g.id, gameStateName(g.gstate), note)
	resp := s.response(p, joinGameResponse(g.id))
	s.deliver(out, state)
	return resp
}
