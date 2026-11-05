package server

import (
	"sort"
	"strings"
	"time"
)

// Stats is a snapshot for the console dashboard.
type Stats struct {
	Uptime        time.Duration
	PlayersOnline []string // logged-in players (race servers excluded)
	InRace        []string // in a game that is IN_GAME
	InLobby       []string // in a game that is not racing yet
	Matchmaking   []string
	Groups        int
	LiveLaunchers int
	Connections   int
	RaceServers   []RaceServerStat
	ServerGames   []GameStat
}

// RaceServerStat: one managed NFSTR_DedicatedServer.exe process.
type RaceServerStat struct {
	N, Port, PID, Restarts int
	State                  string
	Uptime                 time.Duration
}

// GameStat: one Blaze game.
type GameStat struct {
	ID        uint32
	Host      string // race server persona or host player
	State     string
	Players   []string
	Playlist  string
	Dedicated bool
}

func (s *Server) Stats() Stats {
	st := Stats{Uptime: time.Since(s.started), Connections: s.connCount(), LiveLaunchers: s.hub.count()}
	for _, c := range s.online.states() {
		if c.Persona.Name == "" || isDSPersonaName(c.Persona.Name) || isDedicatedServerAccount(c.Email) {
			continue
		}
		st.PlayersOnline = append(st.PlayersOnline, c.Persona.Name)
	}
	sort.Strings(st.PlayersOnline)
	s.gm.mu.Lock()
	for _, mm := range s.gm.pool {
		if !mm.done && mm.state != nil {
			st.Matchmaking = append(st.Matchmaking, mm.state.Persona.Name)
		}
	}
	for _, g := range s.gm.games {
		gs := GameStat{ID: g.id, State: gameStateName(g.gstate), Dedicated: g.dedicated(), Playlist: g.attr("playListId")}
		if g.ds != nil {
			gs.Host = g.ds.Persona.Name
		} else if g.host != nil {
			gs.Host = g.host.state.Persona.Name
		}
		for _, p := range g.players {
			gs.Players = append(gs.Players, p.state.Persona.Name)
			if g.gstate == gameStateInGame {
				st.InRace = append(st.InRace, p.state.Persona.Name)
			} else {
				st.InLobby = append(st.InLobby, p.state.Persona.Name)
			}
		}
		st.ServerGames = append(st.ServerGames, gs)
	}
	s.gm.mu.Unlock()
	sort.Slice(st.ServerGames, func(i, j int) bool { return st.ServerGames[i].ID < st.ServerGames[j].ID })
	s.pg.mu.Lock()
	st.Groups = len(s.pg.by)
	s.pg.mu.Unlock()
	s.ds.mu.Lock()
	for _, i := range s.ds.insts {
		rs := RaceServerStat{N: i.n, Port: i.port, PID: i.pid, Restarts: i.restarts, State: i.state}
		if i.state == "running" && !i.started.IsZero() {
			rs.Uptime = time.Since(i.started)
		}
		st.RaceServers = append(st.RaceServers, rs)
	}
	s.ds.mu.Unlock()
	return st
}

// PlayerList joins names for the dashboard ("-" when empty).
func PlayerList(names []string, max int) string {
	if len(names) == 0 {
		return "-"
	}
	if len(names) > max {
		return strings.Join(names[:max], ", ") + ", ..."
	}
	return strings.Join(names, ", ")
}
