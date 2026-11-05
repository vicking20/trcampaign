package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"nfsrun-server/internal/database"
	"nfsrun-server/internal/playlistpatch"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nfsrun-server/internal/blaze"
)

// GameManager (component 0x0004).
//
// Online races run on dedicated race servers: the client asks for NTOP=1
// (CLIENT_SERVER_DEDICATED) and connects once the server sets the game
// attribute OkToConnect="true".
//
// Flow:
//   race server  createGame -> CreateGameResponse + NotifyGameSetup; the server
//                is the topology host and never in the roster. The game is
//                pooled as RESETABLE.
//   player       startMatchmaking -> join an open game of the same playlist,
//                or reset a free one with the player's attributes. The race
//                server gets NotifyPlayerJoining, players get NotifyGameSetup.
//   race server  setGameAttributes -> NotifyGameAttribChange to everyone.
//   both         updateMeshConnection -> ACTIVE_CONNECTED + NotifyPlayerJoinCompleted.
//   race server  returnDedicatedServerToPool -> players removed, RESETABLE again.
//
// Without a race server, MM_MODE=peer creates peer-hosted games (tests only).

const (
	ComponentGameManager uint16 = 0x0004

	GMCreateGame                  uint16 = 1
	GMDestroyGame                 uint16 = 2
	GMAdvanceGameState            uint16 = 3
	GMSetGameSettings             uint16 = 4
	GMSetPlayerCapacity           uint16 = 5
	GMSetGameAttributes           uint16 = 7
	GMSetPlayerAttributes         uint16 = 8
	GMJoinGame                    uint16 = 9
	GMRemovePlayer                uint16 = 11
	GMStartMatchmaking            uint16 = 13
	GMCancelMatchmaking           uint16 = 14
	GMFinalizeGameCreation        uint16 = 15
	GMReturnDedicatedServerToPool uint16 = 0x14
	GMResetDedicatedServer        uint16 = 0x19
	GMReplayGame                  uint16 = 0x13
	GMUpdateGameSession           uint16 = 26
	GMUpdateMeshConnection        uint16 = 29

	GMNotifyMatchmakingFailed     uint16 = 10
	GMNotifyGameCreated           uint16 = 15
	GMNotifyGameRemoved           uint16 = 16
	GMNotifyGameSetup             uint16 = 20
	GMNotifyPlayerJoining         uint16 = 21
	GMNotifyPlayerJoinCompleted   uint16 = 30
	GMNotifyPlayerRemoved         uint16 = 40
	GMNotifyGameAttribChange      uint16 = 80
	GMNotifyPlayerAttribChange    uint16 = 90
	GMNotifyGameStateChange       uint16 = 100
	GMNotifyGameSettingsChange    uint16 = 110
	GMNotifyGameCapacityChange    uint16 = 111
	GMNotifyGameReset             uint16 = 112
	GMNotifyGamePlayerStateChange uint16 = 116

	mmResultCreatedGame        uint64 = 0
	mmResultJoinedNewGame      uint64 = 1
	mmResultJoinedExistingGame uint64 = 2
	mmResultTimedOut           uint64 = 3
	mmResultCanceled           uint64 = 4

	// GameSetupReason union members
	reasonDataless       byte = 0
	reasonResetDedicated byte = 1
	reasonIndirectJoin   byte = 2 // a playgroup member follows its leader into a game
	reasonMatchmaking    byte = 3
	reasonIndirectMM     byte = 4 // a playgroup member of a leader's matchmaking

	datalessCreateGame uint64 = 0
	datalessJoinGame   uint64 = 1

	topologyDedicated uint64 = 1

	gameStateInitializing uint64 = 1
	gameStatePreGame      uint64 = 130
	gameStateInGame       uint64 = 131
	gameStatePostGame     uint64 = 4
	gameStateResetable    uint64 = 7
	gameStateReplaySetup  uint64 = 8

	gameSettingOpenToMatchmaking uint64 = 0x2

	playerActiveConnecting uint64 = 2
	playerActiveConnected  uint64 = 4
	presenceStandard       uint64 = 1
	unsetTeamIndex         uint64 = 0xFFFF

	netConnected uint64 = 2 // PlayerNetConnectionStatus CONNECTED

	defaultMatchmakingDurationMs = 20000
	maxMatchmakingDurationMs     = 120000
)

var (
	matchmakingSessionSeq uint32
	gameIDSeq             uint32
)

type kv struct{ K, V string }

// mmRequest is what we keep from StartMatchmakingRequest / CreateGameRequest.
type mmRequest struct {
	GVer, GName, GType string
	Mode, GSet         uint64
	NTop, Voip         uint64
	PMin, PCap         uint64
	DurMs              int
	Attr, PAttr        []kv
	Net                netInfo
}

func (r mmRequest) attr(k string) string {
	for _, a := range r.Attr {
		if strings.EqualFold(a.K, k) {
			return a.V
		}
	}
	return ""
}

type matchmakingSession struct {
	// group: the leader's playgroup (BTPL) and its other online members, who
	// join the same game (IndirectMatchmakingSetupContext).
	group   objectID
	members []*connState

	msid   uint32
	timer  *time.Timer
	done   bool
	req    mmRequest
	state  *connState
	waitMs int // how long the server keeps it (MM_TIMEOUT for race servers)
}

type gamePlayer struct {
	state    *connState
	slot     uint64
	status   uint64
	attrs    []kv
	net      netInfo
	msid     uint32
	joinedAt time.Time
	relay    *udpRelay // see relay.go
	// In a race-server game being set up the player is held until the race
	// server reaches PRE_GAME, then gets NotifyGameSetup with this result.
	held       bool
	heldResult uint64
	// group: the player's playgroup (UGID); indirect: joined because its leader
	// did (group matchmaking / group join).
	group    objectID
	indirect bool
	leader   uint64 // indirect: the leader's Blaze id (USID)
	// reason: the setup reason when it is not matchmaking (joinGame)
	reason *setupReason
}

type game struct {
	id    uint32
	ds    *connState // dedicated server = topology host; nil for peer-hosted games
	dsNet netInfo
	// relay: set only while building one recipient's notification
	hnetOverride netInfo     // HNET as seen by one player
	pnetForDS    bool        // PNET of relayed players as seen by the race server
	host         *gamePlayer // peer-hosted games only
	players      []*gamePlayer
	gstate       uint64
	attrs        []kv
	req          mmRequest
	seed         uint32
	uuid         string
	// set while the race server prepares a reset (see flushHeldLocked)
	setupSince time.Time
	setupTimer *time.Timer
	resetAttrs []kv // the game attributes as reset, before the race server added its own
}

func (g *game) dedicated() bool { return g.ds != nil }

func (g *game) attr(k string) string {
	for _, a := range g.attrs {
		if strings.EqualFold(a.K, k) {
			return a.V
		}
	}
	return ""
}

// hostInfo: who is the topology/platform host and where it is.
func (g *game) hostInfo() (id, slot uint64, n netInfo) {
	if g.ds != nil {
		if g.hnetOverride.Set {
			o := g.hnetOverride
			o.DBPS, o.NATT, o.UBPS = g.dsNet.DBPS, g.dsNet.NATT, g.dsNet.UBPS
			return g.ds.Persona.BlazeID, dsHostSlot, o
		}
		return g.ds.Persona.BlazeID, dsHostSlot, g.dsNet
	}
	if g.host != nil {
		return g.host.state.Persona.BlazeID, g.host.slot, g.host.net
	}
	return 0, 0, netInfo{}
}

// dsHostSlot is the race server's slot id (THST/PHST HSLT). Players never
// get it: in a dedicated game ConnApiAdapter::setupMembers (PC 0x01A14870)
// indexes clients by slot id and writes the host at HSLT, so a player in the
// same slot is overwritten.
const dsHostSlot = 0

func (g *game) freeSlot() uint64 {
	used := map[uint64]bool{}
	for _, p := range g.players {
		used[p.slot] = true
	}
	first := uint64(0)
	if g.ds != nil {
		first = dsHostSlot + 1 // slot 0 belongs to the race server
	}
	for i := first; ; i++ {
		if !used[i] {
			return i
		}
	}
}

type gameManager struct {
	mu    sync.Mutex
	pool  []*matchmakingSession
	games map[uint32]*game
	out   []outMsg // notifications collected under mu, sent after unlock, in order
	// playlists seen in players' requests; Quick Match picks among these and
	// MM_DEFAULT_PLAYLIST.
	seenPlaylists map[string]bool
}

type outMsg struct {
	to  *connState
	pkt *blaze.Packet
}

func newGameManager() *gameManager { return &gameManager{games: map[uint32]*game{}} }

// ------------------------------------------------------------------ parsing

func mapKV(fs []blaze.Field, tag string) []kv {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		m, _ := f.Value.(map[string]any)
		items, _ := m["items"].([][2]any)
		var out []kv
		for _, it := range items {
			k, _ := it[0].(string)
			v, _ := it[1].(string)
			out = append(out, kv{k, v})
		}
		return out
	}
	return nil
}

func setKV(list []kv, add []kv) []kv {
	for _, a := range add {
		found := false
		for i := range list {
			if list[i].K == a.K {
				list[i].V, found = a.V, true
			}
		}
		if !found {
			list = append(list, a)
		}
	}
	return list
}

// writeKVMap writes an attribute map. The client keeps attribute maps as
// sorted vectors and looks keys up by binary search with Blaze's
// case-insensitive compare (0x019B2DD0), without re-sorting what it decodes.
// A case-sensitive order ("RouteIndex" before "playListId") makes lookups of
// lower-case keys fail: a NULL playListId at join (0x007F5290) crashed every
// player who joined a game that already had RouteIndex/SessionIndex set.
func writeKVMap(w *blaze.Writer, tag string, list []kv) {
	sorted := append([]kv(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool { return lessFold(sorted[i].K, sorted[j].K) })
	keys := make([]string, len(sorted))
	vals := map[string]string{}
	for i, p := range sorted {
		keys[i] = p.K
		vals[p.K] = p.V
	}
	writeStringMap(w, tag, keys, vals)
}

// lessFold orders keys like Blaze's CaseInsensitiveStringLessThan: ASCII
// upper case folded to lower case, then byte order.
func lessFold(a, b string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return x < y
		}
	}
	return len(a) < len(b)
}

func kvString(list []kv) string {
	var b strings.Builder
	for i, a := range list {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(a.K + "=" + a.V)
	}
	return b.String()
}

// netFromUnion reads a NetworkAddress union value holding an IpPairAddress.
func netFromUnion(v any) (netInfo, bool) {
	var n netInfo
	m, ok := v.(map[string]any)
	if !ok {
		return n, false
	}
	fv, ok := m["field"].(blaze.Field)
	if !ok {
		return n, false
	}
	g, _ := fv.Value.([]blaze.Field)
	for _, part := range g {
		pg, _ := part.Value.([]blaze.Field)
		switch part.Tag {
		case "EXIP":
			n.ExtIP, n.ExtPort = fieldUintAuth(pg, "IP"), fieldUintAuth(pg, "PORT")
		case "INIP":
			n.IntIP, n.IntPort = fieldUintAuth(pg, "IP"), fieldUintAuth(pg, "PORT")
		}
	}
	n.Set = n.IntIP != 0 || n.ExtIP != 0
	return n, true
}

// parseNetworkAddress reads a NetworkAddress union field holding an IpPairAddress.
func parseNetworkAddress(fs []blaze.Field, tag string) (netInfo, bool) {
	for _, f := range fs {
		if f.Tag == tag {
			return netFromUnion(f.Value)
		}
	}
	return netInfo{}, false
}

// parseNetworkAddressList reads the first usable address of list<NetworkAddress>.
func parseNetworkAddressList(fs []blaze.Field, tag string) (netInfo, bool) {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		m, _ := f.Value.(map[string]any)
		items, _ := m["items"].([]any)
		for _, it := range items {
			if n, ok := netFromUnion(it); ok && n.Set {
				return n, true
			}
		}
	}
	return netInfo{}, false
}

func uintList(fs []blaze.Field, tag string) []uint64 {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		m, _ := f.Value.(map[string]any)
		items, _ := m["items"].([]any)
		var out []uint64
		for _, it := range items {
			if v, ok := it.(uint64); ok {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

func parseMatchmaking(fs []blaze.Field) mmRequest {
	r := mmRequest{
		GVer: blaze.FindString(fs, "GVER"), GName: blaze.FindString(fs, "GNAM"),
		Mode: fieldUintAuth(fs, "MODE"), GSet: fieldUintAuth(fs, "GSET"),
		NTop: fieldUintAuth(fs, "NTOP"), Voip: fieldUintAuth(fs, "VOIP"),
		PMin: groupUint(fs, "CRIT", "SIZE", "PMIN"), PCap: groupUint(fs, "CRIT", "SIZE", "PCAP"),
		DurMs: int(fieldUintAuth(fs, "DUR")), Attr: mapKV(fs, "ATTR"), PAttr: mapKV(fs, "PATT"),
	}
	r.Net, _ = parseNetworkAddress(fs, "PNET")
	if r.DurMs <= 0 {
		r.DurMs = defaultMatchmakingDurationMs
	}
	if r.DurMs > maxMatchmakingDurationMs {
		r.DurMs = maxMatchmakingDurationMs
	}
	if r.PCap == 0 || r.PCap > 8 {
		r.PCap = 8
	}
	if r.PMin == 0 {
		r.PMin = 2
	}
	return r
}

// parseCreateGame reads a Blaze 3 CreateGameRequest (also used by resetDedicatedServer).
func parseCreateGame(fs []blaze.Field) mmRequest {
	r := mmRequest{
		GVer: blaze.FindString(fs, "VSTR"), GName: blaze.FindString(fs, "GNAM"), GType: blaze.FindString(fs, "GTYP"),
		GSet: fieldUintAuth(fs, "GSET"), NTop: fieldUintAuth(fs, "NTOP"), Voip: fieldUintAuth(fs, "VOIP"),
		Attr: mapKV(fs, "ATTR"), PAttr: mapKV(fs, "PATT"),
	}
	if caps := uintList(fs, "PCAP"); len(caps) > 0 {
		for _, c := range caps {
			r.PCap += c
		}
	}
	if r.PCap == 0 {
		r.PCap = fieldUintAuth(fs, "PMAX")
	}
	if r.PCap == 0 || r.PCap > 16 {
		r.PCap = 8
	}
	r.PMin = 1
	r.Net, _ = parseNetworkAddressList(fs, "HNET")
	return r
}

// ------------------------------------------------------------------ writers

func writeNetworkAddress(w *blaze.Writer, tag string, n netInfo) {
	if !n.Set {
		w.UnionStart(tag, 0x7f)
		return
	}
	w.UnionStart(tag, 2) // IpPairAddress
	writeIPPairValue(w, n)
}

// writeListNetworkAddress: one NetworkAddress as a list element (member byte
// + IpPairAddress body + terminator, no VALU header).
func writeListNetworkAddress(w *blaze.Writer, n netInfo) {
	if !n.Set {
		w.WriteByte(0x7f)
		return
	}
	w.WriteByte(2)
	w.GroupStart("EXIP")
	w.TagVar("IP", n.ExtIP)
	w.TagVar("PORT", n.ExtPort)
	w.GroupEnd()
	w.GroupStart("INIP")
	w.TagVar("IP", n.IntIP)
	w.TagVar("PORT", n.IntPort)
	w.GroupEnd()
	w.WriteByte(0)
}

func writeIPPairValue(w *blaze.Writer, n netInfo) {
	w.GroupStart("VALU")
	w.GroupStart("EXIP")
	w.TagVar("IP", n.ExtIP)
	w.TagVar("PORT", n.ExtPort)
	w.GroupEnd()
	w.GroupStart("INIP")
	w.TagVar("IP", n.IntIP)
	w.TagVar("PORT", n.IntPort)
	w.GroupEnd()
	w.GroupEnd()
}

func writeHostInfo(w *blaze.Writer, tag string, hostID, slot uint64) {
	w.GroupStart(tag)
	w.TagVar("HPID", hostID)
	w.TagVar("HSLT", slot)
	w.GroupEnd()
}

// writeDataBody writes ReplicatedGameData members (no struct header).
func (g *game) writeDataBody(w *blaze.Writer) {
	hostID, hostSlot, hostNet := g.hostInfo()
	admins := []uint64{hostID}
	if g.dedicated() {
		admins = nil
	}
	writeVarList(w, "ADMN", admins)
	writeKVMap(w, "ATTR", g.attrs)
	writeVarList(w, "CAP", []uint64{g.req.PCap, 0})
	w.EmptyMap("CRIT", blaze.TDFString, blaze.TDFString)
	w.TagVar("GID", uint64(g.id))
	w.TagString("GNAM", g.req.GName)
	w.TagVar("GPVH", 0)
	w.TagVar("GSET", g.req.GSet)
	w.TagVar("GSID", uint64(g.id))
	w.TagVar("GSTA", g.gstate)
	w.TagString("GTYP", g.req.GType)
	// HNET is list<NetworkAddress>. The element type must be 3 (STRUCT): the
	// decoder skips a list of any other type, and the race server then crashes
	// on an empty address list. List elements carry no VALU header: member
	// byte, IpPairAddress body, terminator.
	w.Tag("HNET", blaze.TDFList)
	w.WriteByte(byte(blaze.TDFGroup))
	w.Var(1)
	writeListNetworkAddress(w, hostNet)
	w.TagVar("HSES", hostID)
	w.TagBool("IGNO", false)
	w.EmptyMap("MATR", blaze.TDFString, blaze.TDFString)
	w.TagVar("MCAP", g.req.PCap)
	w.GroupStart("NQOS")
	w.TagVar("DBPS", hostNet.DBPS)
	w.TagVar("NATT", hostNet.NATT)
	w.TagVar("UBPS", hostNet.UBPS)
	w.GroupEnd()
	w.TagBool("NRES", false)
	w.TagVar("NTOP", g.req.NTop)
	w.TagString("PGID", "")
	w.TagBlob("PGSR", nil)
	writeHostInfo(w, "PHST", hostID, hostSlot)
	w.TagVar("PRES", presenceStandard)
	psas := ""
	if g.dedicated() {
		psas = dsPingSite // the race server's own QoS ping site
	}
	w.TagString("PSAS", psas)
	w.TagVar("QCAP", 0)
	w.TagVar("SEED", uint64(g.seed))
	w.TagVar("TCAP", 0)
	writeHostInfo(w, "THST", hostID, hostSlot)
	writeVarList(w, "TIDS", nil)
	w.TagString("UUID", g.uuid)
	w.TagVar("VOIP", g.req.Voip)
	w.TagString("VSTR", g.req.GVer)
	w.TagBlob("XNNC", nil)
	w.TagBlob("XSES", nil)
}

func (g *game) writeData(w *blaze.Writer, tag string) {
	w.GroupStart(tag)
	g.writeDataBody(w)
	w.GroupEnd()
}

// writePlayerBody writes ReplicatedGamePlayer members (no struct header).
func (g *game) writePlayerBody(w *blaze.Writer, p *gamePlayer) {
	per := p.state.Persona
	w.TagBlob("BLOB", nil)
	w.TagVar("EXID", per.BlazeID)
	w.TagVar("GID", uint64(g.id))
	w.TagVar("LOC", localeEnUS)
	w.TagString("NAME", per.Name)
	writeKVMap(w, "PATT", p.attrs)
	w.TagVar("PID", per.BlazeID)
	pnet := p.net
	if g.pnetForDS && p.relay != nil {
		pnet = p.relay.raceServerNet()
	}
	writeNetworkAddress(w, "PNET", pnet)
	w.TagVar("SID", p.slot)
	w.TagVar("SLOT", 0) // SLOT_PUBLIC
	w.TagVar("STAT", p.status)
	w.TagVar("TIDX", unsetTeamIndex)
	w.TagVar("TIME", uint64(p.joinedAt.Unix()))
	w.TagTriple("UGID", p.group[0], p.group[1], p.group[2])
	w.TagVar("UID", per.BlazeID)
}

// setupReason writes the REAS union of NotifyGameSetup.
type setupReason struct {
	key      byte
	msid     uint32
	result   uint64
	usid     uint64
	dataless uint64
	grid     objectID
}

func (g *game) buildNotifyGameSetup(r setupReason) []byte {
	var w blaze.Writer
	g.writeData(&w, "GAME")
	writeStructListHeader(&w, "PROS", len(g.players))
	for _, p := range g.players {
		g.writePlayerBody(&w, p)
		w.GroupEnd()
	}
	writeStructListHeader(&w, "QUEU", 0)
	w.UnionStart("REAS", r.key)
	w.GroupStart("VALU")
	switch r.key {
	case reasonMatchmaking:
		w.TagVar("FIT", 0)
		w.TagVar("MAXF", 0)
		w.TagVar("MSID", uint64(r.msid))
		w.TagVar("RSLT", r.result)
		w.TagVar("USID", r.usid)
	case reasonIndirectMM:
		w.TagVar("FIT", 0)
		w.TagTriple("GRID", r.grid[0], r.grid[1], r.grid[2])
		w.TagVar("MAXF", 0)
		w.TagVar("MSID", uint64(r.msid))
		w.TagVar("RPVC", 0)
		w.TagVar("RSLT", r.result)
		w.TagVar("USID", r.usid)
	case reasonIndirectJoin:
		w.TagTriple("GRID", r.grid[0], r.grid[1], r.grid[2])
		w.TagVar("RPVC", 0)
	case reasonResetDedicated:
		w.TagVar("ERR", 0)
	default:
		w.TagVar("DCTX", r.dataless)
	}
	w.GroupEnd()
	return w.Bytes()
}

// mmReason: MatchmakingSetupContext, or IndirectMatchmakingSetupContext for a
// playgroup member brought in by its leader's matchmaking.
func mmReason(p *gamePlayer, result uint64) setupReason {
	if p.indirect {
		return setupReason{key: reasonIndirectMM, msid: p.msid, result: result, usid: p.leader, grid: p.group}
	}
	return setupReason{key: reasonMatchmaking, msid: p.msid, result: result, usid: p.state.Persona.BlazeID}
}

func (g *game) buildNotifyPlayerJoining(p *gamePlayer) []byte {
	var w blaze.Writer
	w.TagVar("GID", uint64(g.id))
	w.GroupStart("PDAT")
	g.writePlayerBody(&w, p)
	w.GroupEnd()
	return w.Bytes()
}

func buildStartMatchmakingResponse(msid uint32) []byte {
	var w blaze.Writer
	w.TagVar("MSID", uint64(msid))
	return w.Bytes()
}

func buildNotifyMatchmakingFailed(msid uint32, userSessionID uint64, result uint64) []byte {
	var w blaze.Writer
	w.TagVar("MAXF", 0)
	w.TagVar("MSID", uint64(msid))
	w.TagVar("RSLT", result)
	w.TagVar("USID", userSessionID)
	return w.Bytes()
}

func gidResponse(gid uint32) []byte {
	var w blaze.Writer
	w.TagVar("GID", uint64(gid))
	return w.Bytes()
}

// joinGameResponse: JoinGameResponse{GID, JGS JOINED_GAME=0}.
func joinGameResponse(gid uint32) []byte {
	var w blaze.Writer
	w.TagVar("GID", uint64(gid))
	w.TagVar("JGS", 0)
	return w.Bytes()
}

// ------------------------------------------------------------------ handler

func isDedicatedServerAccount(email string) bool {
	e := strings.ToLower(email)
	return strings.HasPrefix(e, "c4") && strings.Contains(e, "serv") && strings.HasSuffix(e, "@ea.com")
}

func (s *Server) handleGameManager(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	name := blaze.CommandName(p.Component, p.Command)
	gid := uint32(fieldUintAuth(fields, "GID"))
	switch p.Command {
	case GMStartMatchmaking:
		req := parseMatchmaking(fields)
		if !req.Net.Set && state.Net.Set {
			req.Net = state.Net
		}
		if req.Net.Set && req.Net.ExtIP == 0 && state.Net.ExtIP != 0 {
			req.Net.ExtIP, req.Net.ExtPort = state.Net.ExtIP, req.Net.IntPort
		}
		msid := atomic.AddUint32(&matchmakingSessionSeq, 1)
		s.endMatchmaking(state, 0)
		s.leaveStaleGame(state, "started matchmaking again")
		mm := &matchmakingSession{msid: msid, req: req, state: state, waitMs: req.DurMs}
		mm.group, mm.members = s.groupOf(state, fieldTriple(fields, "BTPL"))
		if s.cfg.MMMode != "peer" && req.NTop == topologyDedicated && s.cfg.MMTimeoutSec > 0 {
			// Race-server matchmaking waits MM_TIMEOUT whatever the client asks;
			// the client only stops on our notification.
			mm.waitMs = s.cfg.MMTimeoutSec * 1000
		}
		mm.timer = time.AfterFunc(time.Duration(mm.waitMs)*time.Millisecond, func() { s.matchmakingTimeout(mm) })
		state.wmu.Lock()
		state.mm = mm
		state.wmu.Unlock()
		s.log.Printf("[MM] %s: session %d for %q mode=%d gver=%q size=%d..%d topology=%d attrs={%s} net=%s:%d/%s:%d, ends in %d ms%s",
			name, msid, state.Persona.Name, req.Mode, req.GVer, req.PMin, req.PCap, req.NTop, kvString(req.Attr),
			ipString(req.Net.IntIP), req.Net.IntPort, ipString(req.Net.ExtIP), req.Net.ExtPort, mm.waitMs, groupNote(mm.group, mm.members))
		resp := s.response(p, buildStartMatchmakingResponse(msid))
		s.tryMatch(mm, state) // our own NotifyGameSetup is queued after this reply
		return resp

	case GMCancelMatchmaking:
		s.log.Printf("[MM] %s: session %d", name, fieldUintAuth(fields, "MSID"))
		s.endMatchmaking(state, mmResultCanceled)
		return s.response(p, nil)

	case GMCreateGame:
		return s.createGame(p, fields, state)

	case GMResetDedicatedServer:
		return s.resetDedicatedServer(p, fields, state)

	case GMJoinGame:
		return s.joinGame(p, fields, state)

	case GMReturnDedicatedServerToPool:
		s.withGame(gid, state, func(g *game) {
			for _, gp := range append([]*gamePlayer(nil), g.players...) {
				s.removePlayerLocked(g, gp, 0)
			}
			s.setGameStateLocked(g, gameStateResetable)
		})
		s.log.Printf("[DS] game %d returned to the pool (RESETABLE)", gid)
		return s.response(p, nil)

	case GMAdvanceGameState:
		st := fieldUintAuth(fields, "GSTA")
		s.withGame(gid, state, func(g *game) {
			s.setGameStateLocked(g, st)
			if g.ds == state && st == gameStatePreGame {
				s.flushHeldLocked(g, "the race server is in PRE_GAME")
			}
		})
		s.log.Printf("[GAME] %d state -> %d (%s) by %q", gid, st, gameStateName(st), state.Persona.Name)
		if st == gameStateResetable {
			s.offerWaitingSessions()
		}
		return s.response(p, nil)

	case GMSetGameSettings:
		gset := fieldUintAuth(fields, "GSET")
		s.withGame(gid, state, func(g *game) {
			g.req.GSet = gset
			s.broadcast(g, nil, GMNotifyGameSettingsChange, func(w *blaze.Writer) {
				w.TagVar("ATTR", gset)
				w.TagVar("GID", uint64(g.id))
			})
		})
		s.log.Printf("[GAME] %d settings -> 0x%X", gid, gset)
		return s.response(p, nil)

	case GMSetPlayerCapacity:
		caps := uintList(fields, "PCAP")
		s.withGame(gid, state, func(g *game) {
			var total uint64
			for _, c := range caps {
				total += c
			}
			if total > 0 {
				g.req.PCap = total
			}
			s.broadcast(g, nil, GMNotifyGameCapacityChange, func(w *blaze.Writer) {
				writeVarList(w, "CAP", []uint64{g.req.PCap, 0})
				w.TagVar("GID", uint64(g.id))
				w.TagVar("TCAP", 0)
			})
		})
		return s.response(p, nil)

	case GMSetGameAttributes:
		attrs := mapKV(fields, "ATTR")
		s.withGame(gid, state, func(g *game) {
			g.attrs = setKV(g.attrs, attrs)
			s.broadcast(g, nil, GMNotifyGameAttribChange, func(w *blaze.Writer) {
				writeKVMap(w, "ATTR", attrs)
				w.TagVar("GID", uint64(g.id))
			})
		})
		s.log.Printf("[GAME] %d attributes by %q: %s", gid, state.Persona.Name, kvString(attrs))
		if kvGet(attrs, "OkToConnect") == "true" {
			s.watchRaceConnect(gid)
		}
		return s.response(p, nil)

	case GMSetPlayerAttributes:
		attrs := mapKV(fields, "ATTR")
		pid := fieldUintAuth(fields, "PID")
		s.withGame(gid, state, func(g *game) {
			for _, gp := range g.players {
				if gp.state.Persona.BlazeID == pid {
					gp.attrs = setKV(gp.attrs, attrs)
				}
			}
			s.broadcast(g, nil, GMNotifyPlayerAttribChange, func(w *blaze.Writer) {
				writeKVMap(w, "ATTR", attrs)
				w.TagVar("GID", uint64(g.id))
				w.TagVar("PID", pid)
			})
		})
		return s.response(p, nil)

	case GMUpdateMeshConnection:
		targets := fieldStructList(fields, "TARG")
		s.withGame(gid, state, func(g *game) {
			for _, t := range targets {
				s.log.Printf("[GAME] %d: %q reports connection to %d = %d", g.id, state.Persona.Name, fieldUintAuth(t, "PID"), fieldUintAuth(t, "STAT"))
			}
			if g.ds == state {
				// The dedicated server reports the players it has (or lost).
				for _, t := range targets {
					if fieldUintAuth(t, "STAT") != netConnected {
						continue
					}
					for _, gp := range g.players {
						if gp.state.Persona.BlazeID == fieldUintAuth(t, "PID") {
							s.markConnectedLocked(g, gp)
						}
					}
				}
				return
			}
			me := g.player(state)
			if me == nil {
				return
			}
			connected := len(targets) == 0
			for _, t := range targets {
				if fieldUintAuth(t, "STAT") == netConnected {
					connected = true
				}
			}
			if connected {
				s.markConnectedLocked(g, me)
			}
		})
		return s.response(p, nil)

	case GMRemovePlayer:
		pid := fieldUintAuth(fields, "PID")
		reason := fieldUintAuth(fields, "REAS")
		s.withGame(gid, state, func(g *game) {
			for _, gp := range append([]*gamePlayer(nil), g.players...) {
				if gp.state.Persona.BlazeID == pid {
					s.removePlayerLocked(g, gp, reason)
				}
			}
		})
		return s.response(p, nil)

	case GMReplayGame:
		// The race server asks for the next race of the playlist: back to PRE_GAME.
		// Its state loop (PC 0x013D1FE0) only knows PRE_GAME, IN_GAME and POST_GAME.
		s.withGame(gid, state, func(g *game) { s.setGameStateLocked(g, gameStatePreGame) })
		s.log.Printf("[GAME] %d replayGame by %q: state -> 130 (PRE_GAME), next race RouteIndex=%s", gid, state.Persona.Name, s.gameAttr(gid, "RouteIndex"))
		return s.response(p, nil)

	case GMDestroyGame:
		s.withGame(gid, state, func(g *game) { s.destroyGameLocked(g, fieldUintAuth(fields, "REAS")) })
		// DestroyGameResponse{GID}
		var w blaze.Writer
		w.TagVar("GID", uint64(gid))
		return s.response(p, w.Bytes())

	case GMFinalizeGameCreation, GMUpdateGameSession:
		s.log.Printf("[GAME] %d %s by %q", gid, name, state.Persona.Name)
		// A race server never moves its new game out of INITIALIZING itself; the
		// game goes to the pool (RESETABLE) when its creation is finalized.
		pooled := false
		if p.Command == GMFinalizeGameCreation {
			s.withGame(gid, state, func(g *game) {
				if g.dedicated() && g.ds == state && g.gstate == gameStateInitializing && len(g.players) == 0 {
					s.setGameStateLocked(g, gameStateResetable)
					pooled = true
				}
			})
		}
		if pooled {
			s.log.Printf("[DS] game %d of %q is ready: state -> 7 (RESETABLE), waiting for players", gid, state.Persona.Name)
			s.dsMarkReady(int(state.Net.IntPort))
			s.offerWaitingSessionsAfter(200 * time.Millisecond)
		}
		return s.response(p, nil)
	}
	s.log.Printf("[BLAZE] %s -- default (empty) reply", name)
	return s.response(p, nil)
}

func gameStateName(st uint64) string {
	switch st {
	case 0:
		return "NEW_STATE"
	case gameStateInitializing:
		return "INITIALIZING"
	case 2:
		return "VIRTUAL"
	case gameStatePreGame:
		return "PRE_GAME"
	case gameStateInGame:
		return "IN_GAME"
	case gameStatePostGame:
		return "POST_GAME"
	case 5:
		return "MIGRATING"
	case 6:
		return "DESTRUCTING"
	case gameStateResetable:
		return "RESETABLE"
	case 8:
		return "REPLAY_SETUP"
	}
	return "?"
}

func (s *Server) setGameStateLocked(g *game, st uint64) {
	g.gstate = st
	s.broadcast(g, nil, GMNotifyGameStateChange, func(w *blaze.Writer) {
		w.TagVar("GID", uint64(g.id))
		w.TagVar("GSTA", st)
	})
}

func (s *Server) markConnectedLocked(g *game, gp *gamePlayer) {
	if gp.status == playerActiveConnected {
		return
	}
	gp.status = playerActiveConnected
	pid := gp.state.Persona.BlazeID
	s.broadcast(g, nil, GMNotifyGamePlayerStateChange, func(w *blaze.Writer) {
		w.TagVar("GID", uint64(g.id))
		w.TagVar("PID", pid)
		w.TagVar("STAT", playerActiveConnected)
	})
	s.broadcast(g, nil, GMNotifyPlayerJoinCompleted, func(w *blaze.Writer) {
		w.TagVar("GID", uint64(g.id))
		w.TagVar("PID", pid)
	})
	s.log.Printf("[GAME] %d: %q connected", g.id, gp.state.Persona.Name)
}

// createGame: a dedicated server registering itself (NTOP=1 or the DS account),
// or a player hosting (peer) (unused by the retail client).
func (s *Server) createGame(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	req := parseCreateGame(fields)
	isDS := req.NTop == topologyDedicated || isDedicatedServerAccount(state.Email)
	if !req.Net.Set && state.Net.Set {
		req.Net = state.Net
	}
	if req.Net.Set && req.Net.ExtIP == 0 && state.Net.ExtIP != 0 {
		req.Net.ExtIP = state.Net.ExtIP
		if req.Net.ExtPort == 0 {
			req.Net.ExtPort = req.Net.IntPort
		}
	}
	req.Net.DBPS, req.Net.NATT, req.Net.UBPS = state.Net.DBPS, state.Net.NATT, state.Net.UBPS
	if isDS {
		// Race servers send NTOP 0 but run as the dedicated host; echoing peer
		// topology leaves the game in INITIALIZING.
		if req.NTop != topologyDedicated {
			s.log.Printf("[DS] createGame asked for topology %d: using CLIENT_SERVER_DEDICATED (1)", req.NTop)
		}
		req.NTop = topologyDedicated
		// Players reach the race server at the server machine's public address:
		// DS_PUBLIC_IP / PUBLIC_IP, else the race server's own address when it is
		// public (a VPS), else PUBLIC_HOST's DNS answer.
		pub := s.dsPublicIP()
		if !s.publicIPConfigured() && isPublicIPv4(req.Net.IntIP) && pub != req.Net.IntIP {
			if pub != 0 {
				s.log.Printf("[DS] race server address %s is public: using it instead of %s (DNS of %s)", ipString(req.Net.IntIP), ipString(pub), s.cfg.PublicHost)
			}
			pub = req.Net.IntIP
		}
		if pub != 0 {
			req.Net.ExtIP = pub
			req.Net.Set = true
		}
		if req.Net.ExtPort == 0 {
			req.Net.ExtPort = req.Net.IntPort
		}
		// The race server reports -gamePort, but its socket listens elsewhere
		// (UDP 6000 by default). Players are sent to DS_MESH_PORT (0 = as reported).
		if mp := uint64(s.cfg.DSMeshPort); mp != 0 {
			if req.Net.IntPort != mp || req.Net.ExtPort != mp {
				s.log.Printf("[DS] race server reports port %d; players are sent to UDP %d (DS_MESH_PORT)", req.Net.IntPort, mp)
			}
			req.Net.IntPort, req.Net.ExtPort = mp, mp
		}
	}
	g := &game{id: atomic.AddUint32(&gameIDSeq, 1), gstate: gameStateInitializing, req: req, attrs: req.Attr, uuid: randomHex(16), seed: uint32(time.Now().UnixNano())}
	reason := setupReason{key: reasonDataless, dataless: datalessCreateGame}
	s.gm.mu.Lock()
	if isDS {
		g.ds, g.dsNet = state, req.Net
		state.dsGames = append(state.dsGames, g)
	} else {
		gp := &gamePlayer{state: state, slot: 0, status: playerActiveConnecting, attrs: req.PAttr, net: req.Net, joinedAt: time.Now()}
		g.players = []*gamePlayer{gp}
		g.host = gp
		state.game = g
	}
	s.gm.games[g.id] = g
	setup := g.buildNotifyGameSetup(reason)
	s.gm.mu.Unlock()
	kind := "peer-hosted (player)"
	if isDS {
		kind = "DEDICATED SERVER"
	}
	s.log.Printf("[DS] %s: game %d by %q (%s) topology=%d cap=%d gset=0x%X gver=%q name=%q net int %s:%d ext %s:%d attrs={%s}",
		blaze.CommandName(p.Component, p.Command), g.id, state.Persona.Name, kind, req.NTop, req.PCap, req.GSet, req.GVer, req.GName,
		ipString(req.Net.IntIP), req.Net.IntPort, ipString(req.Net.ExtIP), req.Net.ExtPort, kvString(req.Attr))
	state.Pending = append(state.Pending, notifyPacket(ComponentGameManager, GMNotifyGameSetup, setup))
	return s.response(p, gidResponse(g.id))
}

// resetDedicatedServer: a client takes an available DS game with its own settings.
func (s *Server) resetDedicatedServer(p *blaze.Packet, fields []blaze.Field, state *connState) *blaze.Packet {
	req := parseCreateGame(fields)
	if !req.Net.Set {
		req.Net = state.Net
	}
	pnet, _ := parseNetworkAddressList(fields, "HNET")
	if !pnet.Set {
		pnet = state.Net
	}
	s.gm.mu.Lock()
	g := s.findResetableLocked(req.GVer)
	if g == nil {
		s.gm.mu.Unlock()
		s.log.Printf("[DS] resetDedicatedServer by %q: no dedicated server available", state.Persona.Name)
		return s.errorResp(p, 0x0004) // GAMEMANAGER_ERR_...: no server
	}
	s.resetGameLocked(g, req)
	gp := s.addPlayerLocked(g, state, req.PAttr, pnet, 0)
	s.gm.out = append(s.gm.out, outMsg{state, notifyPacket(ComponentGameManager, GMNotifyGameSetup, s.setupFor(g, gp, setupReason{key: reasonResetDedicated}))})
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	s.log.Printf("[DS] resetDedicatedServer: %q took game %d (attrs {%s})", gp.state.Persona.Name, g.id, kvString(g.attrs))
	resp := s.response(p, joinGameResponse(g.id))
	s.deliver(out, state)
	return resp
}

// ------------------------------------------------------------------ dedicated servers

func (s *Server) dsRegisteredLocked() bool {
	for _, g := range s.gm.games {
		if g.dedicated() {
			return true
		}
	}
	return false
}

func versionOK(a, b string) bool { return a == "" || b == "" || a == b }

func (s *Server) findResetableLocked(gver string) *game {
	var best *game
	for _, g := range s.gm.games {
		if g.dedicated() && g.gstate == gameStateResetable && len(g.players) == 0 && versionOK(g.req.GVer, gver) {
			if best == nil || g.id < best.id {
				best = g
			}
		}
	}
	return best
}

// findOpenDSGameLocked: a running DS game this request can join.
func (s *Server) findOpenDSGameLocked(r mmRequest, need int) *game {
	var best *game
	for _, g := range s.gm.games {
		if !g.dedicated() || len(g.players) == 0 || uint64(len(g.players)+need) > g.req.PCap {
			continue
		}
		switch g.gstate {
		case gameStateResetable, gameStatePostGame, 5, 6:
			continue
		}
		if g.req.GSet != 0 && g.req.GSet&gameSettingOpenToMatchmaking == 0 {
			continue
		}
		if !versionOK(g.req.GVer, r.GVer) {
			continue
		}
		if pl := r.attr("playListId"); pl != "" && !strings.EqualFold(pl, g.attr("playListId")) {
			continue
		}
		if best == nil || len(g.players) > len(best.players) {
			best = g
		}
	}
	return best
}

// resetGameLocked applies the creator's settings to a RESETABLE DS game and tells the DS.
func (s *Server) resetGameLocked(g *game, r mmRequest) {
	attrs := append([]kv(nil), r.Attr...)
	if pl := r.attr("playListId"); pl != "" {
		s.rememberPlaylistLocked(pl)
	} else if pl := s.quickMatchPlaylistLocked(); pl != "" {
		// Quick Match carries no playlist: it joins any open game; a new game gets a
		// random one. The race server runs the playlist and its end-of-playlist vote
		// itself.
		attrs = setKV(attrs, []kv{{"playListId", pl}})
	}
	g.attrs = setKV(g.attrs, attrs)
	if r.GName != "" {
		g.req.GName = r.GName
	}
	if r.GSet != 0 {
		g.req.GSet = r.GSet
	}
	if r.GVer != "" && g.req.GVer == "" {
		g.req.GVer = r.GVer
	}
	if r.Voip != 0 {
		g.req.Voip = r.Voip
	}
	g.gstate = gameStateInitializing
	g.seed = uint32(time.Now().UnixNano())
	var w blaze.Writer
	g.writeData(&w, "DATA")
	s.gm.out = append(s.gm.out, outMsg{g.ds, notifyPacket(ComponentGameManager, GMNotifyGameReset, w.Bytes())})
	s.log.Printf("[DS] game %d reset for a new session: attrs {%s}", g.id, kvString(g.attrs))
}

// addPlayerLocked puts a player in g and tells everyone already there (DS included).
// Players that join together (silent set) get each other through their own
// NotifyGameSetup roster instead.
func (s *Server) addPlayerLocked(g *game, st *connState, attrs []kv, n netInfo, msid uint32, silent ...*gamePlayer) *gamePlayer {
	gp := &gamePlayer{state: st, slot: g.freeSlot(), status: playerActiveConnecting, attrs: attrs, net: n, msid: msid, joinedAt: time.Now()}
	s.introduceMembersLocked(g, st)
	if g.ds != nil && s.cfg.RelayEnabled {
		if r, err := s.openRelay(g.id, st.Persona.Name, n.ExtIP, s.raceServerAddr(g)); err != nil {
			s.log.Printf("[RELAY] game %d %q: %v -- direct connection", g.id, st.Persona.Name, err)
		} else {
			gp.relay = r
		}
	}
	payload := g.buildNotifyPlayerJoining(gp)
	if g.ds != nil {
		s.gm.out = append(s.gm.out, outMsg{g.ds, notifyPacket(ComponentGameManager, GMNotifyPlayerJoining, g.joiningForRaceServer(gp))})
	}
	for _, o := range g.players {
		skip := o.held // held players get the whole roster with their setup
		for _, q := range silent {
			if q == o {
				skip = true
			}
		}
		if !skip {
			s.gm.out = append(s.gm.out, outMsg{o.state, notifyPacket(ComponentGameManager, GMNotifyPlayerJoining, payload)})
		}
	}
	g.players = append(g.players, gp)
	s.noteRecentLocked(g)
	if st.game != nil && st.game != g {
		if old := st.game.player(st); old != nil {
			s.removePlayerLocked(st.game, old, 0)
		}
	}
	st.game = g
	return gp
}

// offerWaitingSessions retries the pool when a server becomes available.
func (s *Server) offerWaitingSessions() {
	s.gm.mu.Lock()
	var waiting []*matchmakingSession
	for _, o := range s.gm.pool {
		if !o.done {
			waiting = append(waiting, o)
		}
	}
	s.gm.mu.Unlock()
	for _, o := range waiting {
		s.tryMatch(o, nil)
	}
}

// ------------------------------------------------------------------ matchmaking

func compatible(a, b mmRequest) bool {
	return a.GVer == b.GVer && a.Mode == b.Mode && strings.EqualFold(a.attr("playListId"), b.attr("playListId"))
}

func (s *Server) useDedicatedLocked(r mmRequest) bool {
	switch s.cfg.MMMode {
	case "dedicated":
		return true
	case "peer":
		return false
	}
	// The retail client asks for NTOP=1 and cannot host (serverCapable = 0 at
	// 0x0041835D), so such requests wait for a race server and fail when none
	// frees up in time. Peer games only with MM_MODE=peer.
	return r.NTop == 1 || s.dsRegisteredLocked()
}

// tryMatch puts mm in a game if possible: a dedicated server when one is
// registered, otherwise a peer-hosted game once enough players wait.
func (s *Server) tryMatch(mm *matchmakingSession, self *connState) {
	s.gm.mu.Lock()
	if mm.done {
		s.gm.mu.Unlock()
		return
	}
	inPool := false
	for _, o := range s.gm.pool {
		if o == mm {
			inPool = true
		}
	}
	if !inPool {
		s.gm.pool = append(s.gm.pool, mm)
	}
	if s.useDedicatedLocked(mm.req) {
		s.matchDedicatedLocked(mm, self)
		return
	}
	s.matchPeerLocked(mm, self)
}

// takeSessionLocked removes a matchmaking session from the pool (it found a game).
func (s *Server) takeSessionLocked(o *matchmakingSession) {
	o.done = true
	if o.timer != nil {
		o.timer.Stop()
	}
}

func (s *Server) finishSessionsLocked(gps []*gamePlayer) {
	s.gm.removeDoneLocked()
	for _, gp := range gps {
		gp.state.wmu.Lock()
		if gp.state.mm != nil && gp.state.mm.done {
			gp.state.mm = nil
		}
		gp.state.wmu.Unlock()
	}
}

func (s *Server) matchDedicatedLocked(mm *matchmakingSession, self *connState) {
	// 1. an open DS game of the same playlist
	if g := s.findOpenDSGameLocked(mm.req, 1+len(mm.members)); g != nil {
		s.takeSessionLocked(mm)
		gps := s.addSessionLocked(g, mm)
		if !g.setupSince.IsZero() {
			for _, gp := range gps {
				gp.held, gp.heldResult = true, mmResultJoinedNewGame
			}
			s.finishSessionsLocked(gps)
			out := s.gm.out
			s.gm.out = nil
			s.gm.mu.Unlock()
			s.log.Printf("[MM] session %d: %v join dedicated game %d while the race server prepares it (held until PRE_GAME)", mm.msid, playerNames(gps), g.id)
			s.deliver(out, self)
			return
		}
		// Joining a game in progress: the full game, as the race server set it up.
		for _, gp := range gps {
			s.gm.out = append(s.gm.out, outMsg{gp.state, notifyPacket(ComponentGameManager, GMNotifyGameSetup, s.setupFor(g, gp, mmReason(gp, mmResultJoinedExistingGame)))})
		}
		s.finishSessionsLocked(gps)
		out := s.gm.out
		s.gm.out = nil
		s.gm.mu.Unlock()
		s.log.Printf("[MM] session %d: %v join dedicated game %d (%s, %d/%d players, playlist %s)", mm.msid, playerNames(gps), g.id, gameStateName(g.gstate), len(g.players), g.req.PCap, g.attr("playListId"))
		s.deliver(out, self)
		return
	}
	// 2. reset a free DS for this session (and every compatible waiting one)
	g := s.findResetableLocked(mm.req.GVer)
	if g == nil {
		hint := s.dsGamesHintLocked(mm.req.GVer)
		s.gm.mu.Unlock()
		if hint == "" {
			hint = s.dsWaitHint()
		}
		s.log.Printf("[MM] session %d waiting: no free race server (%s); up to %d s", mm.msid, hint, mm.waitMs/1000)
		go s.ensureRaceServer(fmt.Sprintf("session %d (%s)", mm.msid, mm.state.Persona.Name))
		return
	}
	group := []*matchmakingSession{mm}
	seats := 1 + len(mm.members)
	for _, o := range s.gm.pool {
		if o != mm && !o.done && compatible(o.req, mm.req) && uint64(seats+1+len(o.members)) <= g.req.PCap {
			group = append(group, o)
			seats += 1 + len(o.members)
		}
	}
	s.resetGameLocked(g, mm.req)
	var gps []*gamePlayer
	for _, o := range group {
		s.takeSessionLocked(o)
		gps = append(gps, s.addSessionLocked(g, o, gps...)...)
	}
	// Players' NotifyGameSetup waits until the race server has set the game up
	// (RouteIndex, SessionIndex, skillLevel, PRE_GAME); the pre-game screen
	// reads those once at join.
	for i, gp := range gps {
		gp.held, gp.heldResult = true, mmResultJoinedNewGame
		if i == 0 {
			gp.heldResult = mmResultCreatedGame
		}
	}
	names := playerNames(gps)
	g.setupSince = time.Now()
	g.resetAttrs = append([]kv(nil), g.attrs...)
	gid := g.id
	g.setupTimer = time.AfterFunc(time.Duration(s.cfg.DSSetupWaitSec)*time.Second, func() {
		s.withGame(gid, nil, func(g *game) {
			s.flushHeldLocked(g, fmt.Sprintf("the race server did not reach PRE_GAME in %d s", s.cfg.DSSetupWaitSec))
		})
	})
	s.finishSessionsLocked(gps)
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	s.log.Printf("[MM] dedicated game %d (server %q) now hosts %v; they get the game when the race server is in PRE_GAME", g.id, g.ds.Persona.Name, names)
	s.deliver(out, self)
}

// matchPeerLocked: the longest-waiting player hosts (MM_MODE=peer).
func (s *Server) matchPeerLocked(mm *matchmakingSession, self *connState) {
	var group []*matchmakingSession
	for _, o := range s.gm.pool {
		if !o.done && compatible(o.req, mm.req) {
			group = append(group, o)
		}
	}
	need := mm.req.PMin
	for _, o := range group {
		if o.req.PMin > need {
			need = o.req.PMin
		}
	}
	capacity := mm.req.PCap
	if uint64(len(group)) > capacity {
		group = group[:capacity]
	}
	if uint64(len(group)) < need {
		s.gm.mu.Unlock()
		s.log.Printf("[MM] session %d waiting: %d of %d players (no dedicated server registered: peer-hosted)", mm.msid, len(group), need)
		return
	}
	g := &game{id: atomic.AddUint32(&gameIDSeq, 1), gstate: gameStateInitializing, req: group[0].req, attrs: group[0].req.Attr, uuid: randomHex(16)}
	g.req.NTop = 0 // CLIENT_SERVER_PEER_HOSTED
	g.seed = uint32(time.Now().UnixNano())
	var gps []*gamePlayer
	for i, o := range group {
		s.takeSessionLocked(o)
		gp := &gamePlayer{state: o.state, slot: uint64(i), status: playerActiveConnecting, attrs: o.req.PAttr, net: o.req.Net, msid: o.msid, joinedAt: time.Now()}
		g.players = append(g.players, gp)
		o.state.game = g
		gps = append(gps, gp)
	}
	g.host = g.players[0]
	s.gm.games[g.id] = g
	s.noteRecentLocked(g)
	for _, gp := range gps {
		result := mmResultJoinedNewGame
		if gp == g.host {
			result = mmResultCreatedGame
		}
		s.gm.out = append(s.gm.out, outMsg{gp.state, notifyPacket(ComponentGameManager, GMNotifyGameSetup, s.setupFor(g, gp, mmReason(gp, result)))})
	}
	s.finishSessionsLocked(gps)
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	names := []string{}
	for _, gp := range gps {
		names = append(names, gp.state.Persona.Name)
	}
	s.log.Printf("[GAME] %d created: host %q, players %v, mode=%d gver=%q topology=%d", g.id, g.host.state.Persona.Name, names, g.req.Mode, g.req.GVer, g.req.NTop)
	s.deliver(out, self)
}

// deliver sends notifications in order; those for self (the connection whose
// request is being handled) are queued behind its reply.
func (s *Server) deliver(out []outMsg, self *connState) {
	for _, m := range out {
		if self != nil && m.to == self {
			self.Pending = append(self.Pending, m.pkt)
			continue
		}
		s.send(m.to, m.pkt, true)
	}
}

func (gm *gameManager) removeDoneLocked() {
	keep := gm.pool[:0]
	for _, o := range gm.pool {
		if !o.done {
			keep = append(keep, o)
		}
	}
	gm.pool = keep
}

func (s *Server) matchmakingTimeout(mm *matchmakingSession) {
	s.gm.mu.Lock()
	if mm.done {
		s.gm.mu.Unlock()
		return
	}
	mm.done = true
	s.gm.removeDoneLocked()
	s.gm.mu.Unlock()
	st := mm.state
	st.wmu.Lock()
	if st.mm == mm {
		st.mm = nil
	}
	st.wmu.Unlock()
	s.log.Printf("[MM] session %d timed out after %d ms (not enough players / no free server)", mm.msid, mm.waitMs)
	s.send(st, notifyPacket(ComponentGameManager, GMNotifyMatchmakingFailed, buildNotifyMatchmakingFailed(mm.msid, st.Persona.BlazeID, mmResultTimedOut)), true)
}

// endMatchmaking stops the connection's session; result 0 = no notification.
func (s *Server) endMatchmaking(state *connState, result uint64) {
	state.wmu.Lock()
	mm := state.mm
	state.mm = nil
	state.wmu.Unlock()
	if mm == nil {
		return
	}
	s.gm.mu.Lock()
	wasActive := !mm.done
	mm.done = true
	s.gm.removeDoneLocked()
	s.gm.mu.Unlock()
	if mm.timer != nil {
		mm.timer.Stop()
	}
	if wasActive && result != 0 {
		state.Pending = append(state.Pending, notifyPacket(ComponentGameManager, GMNotifyMatchmakingFailed, buildNotifyMatchmakingFailed(mm.msid, state.Persona.BlazeID, result)))
	}
}

// ------------------------------------------------------------------ games

func (g *game) player(st *connState) *gamePlayer {
	for _, p := range g.players {
		if p.state == st {
			return p
		}
	}
	return nil
}

func (s *Server) withGame(gid uint32, self *connState, fn func(g *game)) {
	s.gm.mu.Lock()
	if g := s.gm.games[gid]; g != nil {
		fn(g)
	} else if gid != 0 {
		s.log.Printf("[GAME] request for unknown game %d", gid)
	}
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	s.deliver(out, self)
}

// broadcast sends a notification to every member of g (players and the
// dedicated server) except skip (gm.mu held).
func (s *Server) broadcast(g *game, skip *connState, cmd uint16, body func(w *blaze.Writer)) {
	var w blaze.Writer
	body(&w)
	payload := w.Bytes()
	if g.ds != nil && g.ds != skip {
		s.gm.out = append(s.gm.out, outMsg{g.ds, notifyPacket(ComponentGameManager, cmd, payload)})
	}
	for _, p := range g.players {
		if p.state == skip || p.held {
			continue
		}
		s.gm.out = append(s.gm.out, outMsg{p.state, notifyPacket(ComponentGameManager, cmd, payload)})
	}
}

// flushHeldLocked (gm.mu held) sends the held players their game once the
// race server has set it up.
func (s *Server) flushHeldLocked(g *game, why string) {
	if g.setupSince.IsZero() {
		return
	}
	if g.setupTimer != nil {
		g.setupTimer.Stop()
		g.setupTimer = nil
	}
	took := time.Since(g.setupSince).Round(time.Millisecond)
	g.setupSince = time.Time{}
	var names []string
	for _, gp := range g.players {
		if !gp.held {
			continue
		}
		gp.held = false
		gp.joinedAt = time.Now()
		s.gm.out = append(s.gm.out, s.heldSetupLocked(g, gp)...)
		names = append(names, gp.state.Persona.Name)
	}
	s.log.Printf("[MM] game %d ready after %s (%s): state %s, RouteIndex=%s SessionIndex=%s playListId=%s -> game sent to %v",
		g.id, took, why, gameStateName(g.gstate), g.attr("RouteIndex"), g.attr("SessionIndex"), playlistLabel(g.attr("playListId")), names)
}

// heldSetupLocked builds what a held player gets: the game as reset
// (INITIALIZING, reset attributes), then the state change and every attribute
// the race server set since. Sending RouteIndex/SessionIndex/PRE_GAME inside
// NotifyGameSetup crashes the client at join.
func (s *Server) heldSetupLocked(g *game, gp *gamePlayer) []outMsg {
	st, attrs := g.gstate, g.attrs
	g.gstate, g.attrs = gameStateInitializing, g.resetAttrs
	r := mmReason(gp, gp.heldResult)
	if gp.reason != nil {
		r = *gp.reason
	}
	setup := s.setupFor(g, gp, r)
	g.gstate, g.attrs = st, attrs
	out := []outMsg{{gp.state, notifyPacket(ComponentGameManager, GMNotifyGameSetup, setup)}}
	if st != gameStateInitializing {
		var w blaze.Writer
		w.TagVar("GID", uint64(g.id))
		w.TagVar("GSTA", st)
		out = append(out, outMsg{gp.state, notifyPacket(ComponentGameManager, GMNotifyGameStateChange, w.Bytes())})
	}
	var changed []kv
	for _, a := range attrs {
		if kvGet(g.resetAttrs, a.K) != a.V {
			changed = append(changed, a)
		}
	}
	for _, a := range changed { // one per attribute, as the race server sent them
		var w blaze.Writer
		writeKVMap(&w, "ATTR", []kv{a})
		w.TagVar("GID", uint64(g.id))
		out = append(out, outMsg{gp.state, notifyPacket(ComponentGameManager, GMNotifyGameAttribChange, w.Bytes())})
	}
	return out
}

// gameAttr reads one attribute of a game (for logs).
func (s *Server) gameAttr(gid uint32, k string) string {
	s.gm.mu.Lock()
	defer s.gm.mu.Unlock()
	if g := s.gm.games[gid]; g != nil {
		return g.attr(k)
	}
	return ""
}

func (s *Server) removePlayerLocked(g *game, gp *gamePlayer, reason uint64) {
	pid := gp.state.Persona.BlazeID
	s.broadcast(g, nil, GMNotifyPlayerRemoved, func(w *blaze.Writer) {
		w.TagVar("CNTX", 0)
		w.TagVar("GID", uint64(g.id))
		w.TagVar("PID", pid)
		w.TagVar("REAS", reason)
	})
	s.log.Printf("[GAME] %d: %q left (reason %d)", g.id, gp.state.Persona.Name, reason)
	gp.relay.close(fmt.Sprintf("left, reason %d", reason))
	if !g.dedicated() && (gp == g.host || len(g.players) == 1) {
		// Peer-hosted, no host migration: the game ends for everyone still in it.
		s.destroyGameLocked(g, 0)
		return
	}
	keep := g.players[:0]
	for _, p := range g.players {
		if p != gp {
			keep = append(keep, p)
		}
	}
	g.players = keep
	if gp.state.game == g {
		gp.state.game = nil
	}
}

func (s *Server) destroyGameLocked(g *game, reason uint64) {
	if s.gm.games[g.id] == nil {
		return
	}
	s.broadcast(g, nil, GMNotifyGameRemoved, func(w *blaze.Writer) {
		w.TagVar("GID", uint64(g.id))
		w.TagVar("REAS", reason)
	})
	if g.setupTimer != nil {
		g.setupTimer.Stop()
		g.setupTimer = nil
	}
	for _, p := range g.players {
		if p.state.game == g {
			p.state.game = nil
		}
		p.relay.close("game removed")
		if p.held {
			// still waiting on its side: end its matchmaking instead
			s.gm.out = append(s.gm.out, outMsg{p.state, notifyPacket(ComponentGameManager, GMNotifyMatchmakingFailed, buildNotifyMatchmakingFailed(p.msid, p.state.Persona.BlazeID, mmResultTimedOut))})
		}
	}
	if g.ds != nil {
		keep := g.ds.dsGames[:0]
		for _, o := range g.ds.dsGames {
			if o != g {
				keep = append(keep, o)
			}
		}
		g.ds.dsGames = keep
	}
	delete(s.gm.games, g.id)
	s.log.Printf("[GAME] %d removed", g.id)
}

// leaveAll runs when a connection closes: matchmaking and game cleanup.
func (gm *gameManager) leaveAll(s *Server, state *connState) {
	gm.mu.Lock()
	for _, o := range gm.pool {
		if o.state == state {
			o.done = true
			if o.timer != nil {
				o.timer.Stop()
			}
		}
	}
	gm.removeDoneLocked()
	if g := state.game; g != nil {
		if gp := g.player(state); gp != nil {
			s.removePlayerLocked(g, gp, 0)
		}
	}
	for _, g := range append([]*game(nil), state.dsGames...) {
		s.log.Printf("[DS] dedicated server %q disconnected: game %d ends", state.Persona.Name, g.id)
		s.destroyGameLocked(g, 0)
	}
	out := gm.out
	gm.out = nil
	gm.mu.Unlock()
	var rest []outMsg
	for _, m := range out {
		if m.to != state { // the closing connection itself gets nothing
			rest = append(rest, m)
		}
	}
	s.deliver(rest, nil)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func groupUint(fs []blaze.Field, path ...string) uint64 {
	cur := fs
	for i, tag := range path {
		found := false
		for _, f := range cur {
			if f.Tag != tag {
				continue
			}
			if i == len(path)-1 {
				v, _ := f.Value.(uint64)
				return v
			}
			if g, ok := f.Value.([]blaze.Field); ok {
				cur, found = g, true
			}
			break
		}
		if !found {
			return 0
		}
	}
	return 0
}

// gameSummary for /health.
func (s *Server) gameSummary() []map[string]any {
	s.gm.mu.Lock()
	defer s.gm.mu.Unlock()
	var out []map[string]any
	for _, g := range s.gm.games {
		host := ""
		if g.ds != nil {
			host = g.ds.Persona.Name
		} else if g.host != nil {
			host = g.host.state.Persona.Name
		}
		var names []string
		for _, p := range g.players {
			names = append(names, p.state.Persona.Name)
		}
		out = append(out, map[string]any{"gid": g.id, "host": host, "dedicated": g.dedicated(), "state": gameStateName(g.gstate), "playlist": g.attr("playListId"), "players": names})
	}
	return out
}

// noteRecentLocked updates everyone's recent-players list for game g.
func (s *Server) noteRecentLocked(g *game) {
	var ps []database.Persona
	for _, p := range g.players {
		ps = append(ps, p.state.Persona)
	}
	if len(ps) > 1 {
		s.recent.met(ps, func(p database.Persona) uint64 { return p.BlazeID })
	}
}

// quickMatchPlaylistLocked picks a playlist for a Quick Match game:
// MM_DEFAULT_PLAYLIST (comma separated) plus the playlists players chose.
func (s *Server) quickMatchPlaylistLocked() string {
	set := map[string]bool{}
	for _, p := range strings.Split(s.cfg.MMDefaultPlaylist, ",") {
		if p = strings.ToUpper(strings.TrimSpace(p)); p != "" {
			set[p] = true
		}
	}
	s.loadPlaylistsLocked()
	for p := range s.gm.seenPlaylists {
		set[p] = true
	}
	if len(set) == 0 {
		return ""
	}
	list := make([]string, 0, len(set))
	for p := range set {
		list = append(list, p)
	}
	sort.Strings(list)
	return list[mrand.Intn(len(list))]
}

// dsGamesHintLocked (gm.mu held): when race-server games exist but none is
// free, describes their states for the log. "" when there are none.
func (s *Server) dsGamesHintLocked(gver string) string {
	var parts []string
	for _, g := range s.gm.games {
		if !g.dedicated() {
			continue
		}
		why := gameStateName(g.gstate)
		switch {
		case g.gstate == gameStateResetable && !versionOK(g.req.GVer, gver):
			why += fmt.Sprintf(", version %q vs player %q", g.req.GVer, gver)
		case g.gstate == gameStateInitializing:
			why += ", the race server has not set it RESETABLE yet"
		case len(g.players) > 0:
			why += fmt.Sprintf(", %d player(s)", len(g.players))
		}
		parts = append(parts, fmt.Sprintf("game %d of %q: %s", g.id, g.ds.Persona.Name, why))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// offerWaitingSessionsAfter: offer the pool to waiting matchmaking sessions a
// moment later (after the reply and the state notification went out).
func (s *Server) offerWaitingSessionsAfter(d time.Duration) {
	time.AfterFunc(d, s.offerWaitingSessions)
}

// introduceMembersLocked (gm.mu held): the player and every member must know
// each other (UserAdded + UserUpdated) before the player enters g; the
// client's mesh setup looks up the topology host's user and crashes if it is
// unknown.
func (s *Server) introduceMembersLocked(g *game, st *connState) {
	tell := func(to, about *connState) {
		if to == nil || about == nil || to == about || about.Persona.BlazeID == 0 {
			return
		}
		for _, p := range userOnlinePackets(about) {
			s.gm.out = append(s.gm.out, outMsg{to, p})
		}
	}
	tell(st, g.ds)
	tell(g.ds, st)
	for _, o := range g.players {
		tell(st, o.state)
		tell(o.state, st)
	}
}

// leaveStaleGame removes a player who starts matchmaking (or joins another
// game) while still listed in a game, so it is not added twice.
func (s *Server) leaveStaleGame(st *connState, why string) {
	s.gm.mu.Lock()
	if g := st.game; g != nil {
		if gp := g.player(st); gp != nil {
			s.log.Printf("[GAME] %d: %q is still listed here but %s: removing (join timeout)", g.id, st.Persona.Name, why)
			s.removePlayerLocked(g, gp, 0)
		}
		if st.game == g {
			st.game = nil
		}
	}
	out := s.gm.out
	s.gm.out = nil
	s.gm.mu.Unlock()
	s.deliver(out, st)
}

// raceConnectWait: how long players get to reach the race server after
// OkToConnect=true before it is logged.
var raceConnectWait = 30 * time.Second

// watchRaceConnect logs players that never reported a connection to the race
// server (usually a UDP problem).
func (s *Server) watchRaceConnect(gid uint32) {
	time.AfterFunc(raceConnectWait, func() {
		s.gm.mu.Lock()
		defer s.gm.mu.Unlock()
		g := s.gm.games[gid]
		if g == nil || !g.dedicated() {
			return
		}
		for _, gp := range g.players {
			if gp.status != playerActiveConnected && time.Since(gp.joinedAt) >= raceConnectWait {
				s.log.Printf("[DS] game %d: %q has not connected to the race server %s:%d (UDP) %s after OkToConnect; check that UDP %d is open on the server (Windows firewall: tools\\open_ds_firewall.bat, and the router / hosting firewall) and that the player's network allows UDP out%s",
					g.id, gp.state.Persona.Name, ipString(g.dsNet.ExtIP), g.dsNet.ExtPort, raceConnectWait, g.dsNet.ExtPort, relayNote(gp))
			}
		}
	})
}

func kvGet(list []kv, k string) string {
	for _, a := range list {
		if strings.EqualFold(a.K, k) {
			return a.V
		}
	}
	return ""
}

// setupFor builds NotifyGameSetup for one player; with a relay, HNET points
// at the player's relay port. gm.mu held.
func (s *Server) setupFor(g *game, gp *gamePlayer, r setupReason) []byte {
	if gp != nil && gp.relay != nil {
		ip := g.dsNet.ExtIP
		if ip == 0 {
			ip = g.dsNet.IntIP
		}
		g.hnetOverride = gp.relay.publicNet(ip)
		defer func() { g.hnetOverride = netInfo{} }()
	}
	return g.buildNotifyGameSetup(r)
}

// joiningForRaceServer: NotifyPlayerJoining as sent to the race server
// (relayed players appear at their relay's local address).
func (g *game) joiningForRaceServer(gp *gamePlayer) []byte {
	g.pnetForDS = true
	defer func() { g.pnetForDS = false }()
	return g.buildNotifyPlayerJoining(gp)
}

func relayNote(gp *gamePlayer) string {
	if gp.relay == nil {
		return ""
	}
	return fmt.Sprintf(" (relay UDP %d: %d packets from the player, %d from the race server)", gp.relay.pubPort, gp.relay.in.Load(), gp.relay.out.Load())
}

// playlistsFile is data\playlists.txt: playlists players chose, one GUID per
// line ("#" comments allowed), kept across restarts. Quick Match picks among
// these and MM_DEFAULT_PLAYLIST.
func (s *Server) playlistsFile() string {
	if s.cfg.LockerDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.cfg.LockerDir), "playlists.txt")
}

func (s *Server) loadPlaylistsLocked() {
	if s.gm.seenPlaylists != nil {
		return
	}
	s.gm.seenPlaylists = map[string]bool{}
	path := s.playlistsFile()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.IndexAny(line, "# \t"); i >= 0 {
			line = line[:i]
		}
		if g := strings.ToUpper(strings.TrimSpace(line)); len(g) == 36 {
			s.gm.seenPlaylists[g] = true
		}
	}
	s.log.Printf("[MM] %d playlist(s) for Quick Match from %s", len(s.gm.seenPlaylists), path)
}

func (s *Server) rememberPlaylistLocked(pl string) {
	s.loadPlaylistsLocked()
	g := strings.ToUpper(strings.TrimSpace(pl))
	if g == "" || s.gm.seenPlaylists[g] {
		return
	}
	if playlistpatch.IsExtra(g) && !s.cfg.MMQuickExtra {
		// extra playlists stay out of Quick Match unless MM_QUICK_EXTRA=true
		return
	}
	s.gm.seenPlaylists[g] = true
	path := s.playlistsFile()
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		s.log.Printf("[MM] cannot save playlist %s: %v", g, err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", g)
	s.log.Printf("[MM] new playlist %s saved to %s (Quick Match now picks among %d)", playlistLabel(g), path, len(s.gm.seenPlaylists))
}

// playlistNames: the Playlist assets in NFSGame.sb (_c4/Gameplay/Playlist/
// Nfs12Career/Playlists/*, instance GUID = playListId). Only for logs.
var playlistNames = map[string]string{
	"F3E31DF0-82C0-4F21-A56E-8C6F614D4C29": "Supercar",
	"75286283-5078-420B-8B59-796A01371E2C": "Underground",
	"40808F8D-99F3-44D6-AE7F-0F06B2D84FD3": "Mixed",
	"6530CB16-7317-4945-AA78-8B1EC2480534": "Exotic",
	"A2BD2E19-116D-43A6-A338-D47F6D1EA729": "Muscle",
	"C516C96D-3870-4D57-8B06-CACEDF60E187": "Need For Speed",
	"FE8A591E-47BF-4F81-B278-1E7E238624B5": "European Elite",
	"9E6A682E-BBF5-4376-8DE0-F968478B8343": "All American",
	"BD970881-06F7-4A6C-96A2-0C2050FF02F3": "Japan",
	"4AB933B4-9F7A-4CB4-9B0E-F789DB5E19EA": "Nissan Showdown",
	"0C339FF1-DA88-4548-B90C-5063ECDA78FA": "Porsche Unleashed",
	"AEAC6F79-E98A-44A6-AA69-EFA2614415FB": "Classics",
	"3B46C82B-8F65-476E-8451-9DD5D5905E98": "Insomnia",
	"BC4C430F-8990-498E-8F74-CB068183B544": "AWD",
	"4803206B-D393-4556-A361-109198C620D1": "Ford",
	"8BAE3D69-689F-48E8-AD52-0C3B674F31BE": "Lamborghini Untamed",
}

// playlistLabel is "GUID (Name)" for logs.
func playlistLabel(g string) string {
	if n, ok := playlistNames[strings.ToUpper(g)]; ok {
		return g + " (" + n + ")"
	}
	return g
}
