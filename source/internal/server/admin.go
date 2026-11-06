package server

// Server-owner commands, typed in the server window (C opens the command
// line) or on stdin with CONSOLE_MODE=log. Every command and its result goes
// to the log as [ADMIN].

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"nfsrun-server/internal/database"
)

// CommandResult is what a command prints. Confirm != "": nothing happened yet;
// ask that question and run the same line again with confirmed=true.
type CommandResult struct {
	Lines   []string
	Confirm string
}

type adminStore interface {
	FindPersona(name string) (database.PersonaInfo, bool, error)
	ListPersonaInfo(limit int) ([]database.PersonaInfo, error)
	DeletePersona(name string) (database.PersonaInfo, []uint64, error)
	RenamePersona(oldName, newName string) (database.PersonaInfo, error)
	ResetLauncherDevice(name string) error
	CreateLauncherClaim(name string, ttl time.Duration) (string, time.Time, error)
	ListLauncherClaims() ([]database.LauncherClaim, error)
	CancelLauncherClaim(name string) (bool, error)
}

// CommandHelp lists the commands (help, and the console's H key).
var CommandHelp = []string{
	"players                 who is online, and where",
	"players all             every player in the database",
	"games                   games and race servers, with the advertised address",
	"kick <name>             disconnect a player (they can come back)",
	"ban <name>              refuse that player's logins and disconnect them;  unban <name>;  bans lists them",
	"latejoin [on|off]       let players join a race that has started (default on);  logins [on|off]  remember game login codes (default on);  servers [n]  race servers at the same time (0 = DS_COUNT)",
	"traffic [off | <density 0-3> [<cars 0-50>]]  show or set the race servers' traffic (next race server start)",
	"cars [force <hash,...|off> | name <hash> <label>]  cars players used; force every player into chosen cars",
	"announce \"<title>\" \"<text>\"  post a news item players see in the game's news feed;  news lists them;  news delete <id>",
	"say [-n <times>] [-every <seconds>] <text>  show a short text to everyone in a game now, optionally repeated;  say stop cancels (the game shows it as \"<text> HAS LEFT\")",
	"modes  |  mode <name>  |  mode save <name>  |  mode delete <name>  list the race modes, switch to one, keep the current settings as your own",
	"score  |  score <mode>  |  score off  |  score reset  |  score scheme <field|top3|fixed>   scoring modes: standings from the race results, shown in the News",
	"stop <n>                stop race server number n (its game ends, players return to the menu)",
	"adduser <name>          player with a name + generated password (no launcher needed)",
	"passwd <name>           new password for that player (old sessions end)",
	"rename <old> <new>      new name, same records, photos and launcher key",
	"delete-player <name>    delete a player and all their data (asks first)",
	"help                    this list",
	"Names with spaces go in quotes: kick \"Ze Silva\"",
}

// Command runs one server-owner command.
func (s *Server) Command(line string, confirmed bool) (res CommandResult) {
	args := splitCommand(line)
	if len(args) == 0 {
		return CommandResult{}
	}
	if !confirmed {
		s.log.Printf("[ADMIN] > %s", strings.TrimSpace(line))
	}
	defer func() {
		if res.Confirm != "" {
			s.log.Printf("[ADMIN]   %s", res.Confirm)
		}
		for _, l := range res.Lines {
			s.log.Printf("[ADMIN]   %s", l)
		}
	}()
	cmd := strings.ToLower(args[0])
	args = args[1:]
	say := func(f string, a ...any) CommandResult { return CommandResult{Lines: []string{fmt.Sprintf(f, a...)}} }
	db, ok := s.db.(adminStore)
	needDB := map[string]bool{"adduser": true, "passwd": true, "rename": true, "delete-player": true}
	if needDB[cmd] && !ok {
		return say("not available: the database does not support it")
	}
	switch cmd {
	case "help", "?", "h":
		return CommandResult{Lines: CommandHelp}

	case "players", "who", "online":
		if len(args) > 0 && strings.EqualFold(args[0], "all") {
			if !ok {
				return say("not available: the database does not support it")
			}
			return s.cmdAllPlayers(db)
		}
		return s.cmdPlayers()

	case "games":
		return s.cmdGames()

	case "ban":
		if len(args) != 1 {
			return say("usage: ban <name>")
		}
		changed, err := s.setBan(args[0], true)
		if err != nil {
			return say("could not save the ban list: %v", err)
		}
		n := s.kickPlayer(args[0], "banned by the server owner")
		if !changed {
			return say("%q was already banned (%d disconnected)", args[0], n)
		}
		return say("%q banned (%d disconnected)", args[0], n)

	case "unban":
		if len(args) != 1 {
			return say("usage: unban <name>")
		}
		changed, err := s.setBan(args[0], false)
		if err != nil {
			return say("could not save the ban list: %v", err)
		}
		if !changed {
			return say("%q is not banned", args[0])
		}
		return say("%q unbanned", args[0])

	case "bans":
		list := s.banned()
		if len(list) == 0 {
			return say("nobody is banned")
		}
		return CommandResult{Lines: append([]string{fmt.Sprintf("%d banned:", len(list))}, list...)}

	case "say":
		if len(args) == 1 && strings.EqualFold(args[0], "stop") {
			return say("%d repeating announcement(s) cancelled", sayCancel())
		}
		reason, count, every := uint64(0), 1, 30*time.Second
		for len(args) > 1 {
			switch {
			case strings.HasPrefix(args[0], "#"):
				v, err := strconv.Atoi(args[0][1:])
				if err != nil || v < 0 || v > 20 {
					return say("usage: say [-n <times 1-20>] [-every <seconds 10-600>] <text>  |  say stop")
				}
				reason, args = uint64(v), args[1:]
			case args[0] == "-n" && len(args) > 2:
				v, err := strconv.Atoi(args[1])
				if err != nil || v < 1 || v > 20 {
					return say("-n is how many times, 1 to 20")
				}
				count, args = v, args[2:]
			case args[0] == "-every" && len(args) > 2:
				v, err := strconv.Atoi(args[1])
				if err != nil || v < 10 || v > 600 {
					return say("-every is the seconds between showings, 10 to 600")
				}
				every, args = time.Duration(v)*time.Second, args[2:]
			default:
				goto parsed
			}
		}
	parsed:
		text := strings.TrimSpace(strings.Join(args, " "))
		if text == "" {
			return say("usage: say [-n <times 1-20>] [-every <seconds 10-600>] <text>  |  say stop")
		}
		n := s.sayRepeat(text, reason, count, every)
		if n == 0 && count <= 1 {
			return say("nobody is in a game right now")
		}
		if count > 1 {
			return say("shown to %d player(s) now, then %d more time(s) every %d s (say stop cancels)", n, count-1, int(every.Seconds()))
		}
		return say("sent to %d player(s): the game shows it as a player leaving", n)

	case "announce", "news":
		return s.cmdNews(cmd, args)

	case "modes", "mode":
		return s.cmdMode(cmd, args)

	case "score", "scoring":
		return s.cmdScore(args)

	case "cars":
		return s.cmdCars(args)

	case "traffic":
		return s.cmdTraffic(args)
	case "latejoin", "logins", "servers":
		return s.cmdOption(strings.ToLower(cmd), args)

	case "stop":
		if len(args) != 1 {
			return say("usage: stop <race server number>")
		}
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return say("usage: stop <race server number>")
		}
		return say("%s", s.stopRaceServer(n))

	case "kick":
		if len(args) != 1 {
			return say("usage: kick <name>")
		}
		if n := s.kickPlayer(args[0], "kicked by the server owner"); n == 0 {
			return say("%q is not online", args[0])
		}
		return say("%q disconnected", args[0])

	case "adduser", "passwd":
		ps, ok := s.db.(passwordStore)
		if !ok || len(args) != 1 {
			return say("usage: %s <name>   (makes or resets a password login; the password is shown once)", cmd)
		}
		name := strings.TrimSpace(args[0])
		if _, exists, err := db.FindPersona(name); err != nil {
			return say("%v", err)
		} else if cmd == "passwd" && !exists {
			return say("%q does not exist; use: adduser %s", name, quoteName(name))
		}
		pw, err := database.GeneratePassword()
		if err != nil {
			return say("%v", err)
		}
		created, err := ps.SetPassword(name, pw)
		if err != nil {
			return say("%v", err)
		}
		verb := "password reset for"
		if created {
			verb = "player created:"
		}
		if !created {
			s.kickPlayer(name, "password reset")
		}
		return CommandResult{Lines: []string{fmt.Sprintf("%s %q", verb, name), "name     : " + name, "password : " + pw,
			"Give both to the player (ini: SERVER, NAME, PASSWORD). The password cannot be shown again."}}

	case "rename":
		if len(args) != 2 {
			return say("usage: rename <old name> <new name>")
		}
		pi, err := db.RenamePersona(args[0], args[1])
		if err != nil {
			return say("%v", err)
		}
		online := s.kickPlayer(args[0], "renamed") > 0
		s.launchers.forget(pi.ID)
		s.friendsChanged(false)
		out := say("%q is now %q (records, photos, friends and launcher key kept)", args[0], pi.Name)
		if online {
			out.Lines = append(out.Lines, "they were online and were disconnected: the new name shows after they log in again")
		}
		return out

	case "delete-player":
		if len(args) != 1 {
			return say("usage: delete-player <name>")
		}
		pi, found, err := db.FindPersona(args[0])
		if err != nil {
			return say("%v", err)
		}
		if !found {
			return say("player %q not found", args[0])
		}
		if !confirmed {
			last := pi.LastLogin
			if last == "" {
				last = "never"
			}
			return CommandResult{Confirm: fmt.Sprintf("DELETE %q (blazeId %d, %d photo(s), last login %s) with all records, friends and photos? This cannot be undone.", pi.Name, pi.BlazeID, pi.Photos, last)}
		}
		s.kickPlayer(pi.Name, "deleted by the server owner")
		del, photos, err := db.DeletePersona(pi.Name)
		if err != nil {
			return say("%v", err)
		}
		s.launchers.forget(del.ID)
		files := s.removePlayerFiles(del, photos)
		s.friendsChanged(false)
		return say("%q deleted (database rows, %d photo(s), %d file(s) in data\\)", del.Name, len(photos), files)

	}
	return say("unknown command %q; type help", cmd)
}

func quoteName(n string) string {
	if strings.ContainsAny(n, " \t") {
		return `"` + n + `"`
	}
	return n
}

// splitCommand splits a command line on spaces; "double quotes" keep spaces.
func splitCommand(line string) []string {
	var out []string
	var cur strings.Builder
	inQ, have := false, false
	for _, r := range strings.TrimSpace(line) {
		switch {
		case r == '"':
			inQ, have = !inQ, true
		case (r == ' ' || r == '\t') && !inQ:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

// kickPlayer closes a player's game connections and live launcher channels.
// Returns the number of game connections closed.
func (s *Server) kickPlayer(name, why string) int {
	n := 0
	for _, st := range s.online.states() {
		if st.conn == nil || isDSPersonaName(st.Persona.Name) || !strings.EqualFold(st.Persona.Name, name) {
			continue
		}
		s.log.Printf("[ADMIN] closing %s connection of %q (%s): %s", st.kind, st.Persona.Name, st.peer, why)
		_ = st.conn.Close()
		n++
	}
	return n
}

// removePlayerFiles deletes a deleted player's photos and web Autolog files.
func (s *Server) removePlayerFiles(pi database.PersonaInfo, photos []uint64) int {
	n := 0
	for _, id := range photos {
		dir := filepath.Join(s.cfg.LockerDir, strconv.FormatUint(id, 10))
		if _, err := os.Stat(dir); err == nil && os.RemoveAll(dir) == nil {
			n++
		}
	}
	ids := map[string]bool{strconv.FormatUint(pi.BlazeID, 10): true}
	wire := database.Persona{ID: pi.ID, AccountID: pi.AccountID, BlazeID: pi.BlazeID, Name: pi.Name}
	ids[strconv.FormatUint(wire.BlazeID, 10)] = true
	for id := range ids {
		for _, p := range []string{s.web.path(id), s.web.blobPath(id)} {
			if os.Remove(p) == nil {
				n++
			}
		}
	}
	return n
}

func (s *Server) cmdPlayers() CommandResult {
	type row struct{ name, where, addr string }
	var rows []row
	for _, st := range s.online.states() {
		if st.Persona.Name == "" || isDSPersonaName(st.Persona.Name) || isDedicatedServerAccount(st.Email) {
			continue
		}
		where := "menus"
		s.gm.mu.Lock()
		if g := st.game; g != nil && s.gm.games[g.id] == g {
			where = fmt.Sprintf("game %d (%s)", g.id, gameStateName(g.gstate))
		} else if st.mm != nil && !st.mm.done {
			where = "matchmaking"
		}
		s.gm.mu.Unlock()
		if id, _, ok := s.pgStatus(st.Persona.BlazeID); ok {
			where += fmt.Sprintf(", group %d", id)
		}
		rows = append(rows, row{st.Persona.Name, where, remoteIP(st.peer)})
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name) })
	if len(rows) == 0 {
		return CommandResult{Lines: []string{"nobody online (players all: everyone in the database)"}}
	}
	out := []string{fmt.Sprintf("%d online:", len(rows))}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("  %-16s %-34s %s", r.name, r.where, r.addr))
	}
	return CommandResult{Lines: out}
}

func (s *Server) cmdAllPlayers(db adminStore) CommandResult {
	list, err := db.ListPersonaInfo(500)
	if err != nil {
		return CommandResult{Lines: []string{err.Error()}}
	}
	online := map[string]bool{}
	for _, st := range s.online.states() {
		online[strings.ToLower(st.Persona.Name)] = true
	}
	out := []string{fmt.Sprintf("%-16s %-10s %-19s %-6s %s", "name", "blazeId", "last login", "photos", "launcher key")}
	n := 0
	for _, p := range list {
		if isDSPersonaName(p.Name) {
			continue
		}
		n++
		last := p.LastLogin
		if last == "" {
			last = "never"
		}
		key := "no"
		if p.Bound {
			key = "yes"
		}
		if online[strings.ToLower(p.Name)] {
			key += "  [online]"
		}
		out = append(out, fmt.Sprintf("%-16s %-10d %-19s %-6d %s", p.Name, p.BlazeID, last, p.Photos, key))
	}
	out = append(out, fmt.Sprintf("%d player(s)", n))
	return CommandResult{Lines: out}
}

func (s *Server) cmdGames() CommandResult {
	var out []string
	s.gm.mu.Lock()
	ids := make([]uint32, 0, len(s.gm.games))
	for id := range s.gm.games {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		g := s.gm.games[id]
		kind, host, addr := "peer", "-", "-"
		if g.ds != nil {
			kind, host = "race server", g.ds.Persona.Name
			if g.dsNet.ExtIP != 0 {
				addr = fmt.Sprintf("%s:%d", ipString(g.dsNet.ExtIP), g.dsNet.ExtPort)
			}
		} else if g.host != nil {
			host = g.host.state.Persona.Name
			if n := g.host.state.Net; n.ExtIP != 0 {
				addr = fmt.Sprintf("%s:%d", ipString(n.ExtIP), n.ExtPort)
			}
		}
		var names []string
		for _, p := range g.players {
			names = append(names, p.state.Persona.Name)
		}
		out = append(out, fmt.Sprintf("game %d  %s  %s %s  address %s  playlist %s", g.id, gameStateName(g.gstate), kind, host, addr, orDash(g.attr("playListId"))))
		out = append(out, "    players: "+PlayerList(names, 16))
	}
	s.gm.mu.Unlock()
	if len(out) == 0 {
		out = append(out, "no games")
	}
	s.ds.mu.Lock()
	for _, i := range s.ds.insts {
		up := ""
		if i.state == "running" && !i.started.IsZero() {
			up = ", up " + time.Since(i.started).Round(time.Second).String()
		}
		out = append(out, fmt.Sprintf("race server #%d  UDP %d  %s  pid %d%s  restarts %d", i.n, i.port, i.state, i.pid, up, i.restarts))
	}
	s.ds.mu.Unlock()
	return CommandResult{Lines: out}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
