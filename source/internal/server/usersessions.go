package server

import (
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// UserSessions (component 0x7802) and the Util pre/post-auth replies.
//
// Notifications: 1 UserSessionExtendedDataUpdate, 2 NotifyUserAdded,
// 3 NotifyUserRemoved, 4 UserSessionDisconnectReason, 5 UserStatus.
// Layouts follow the Blaze 3 schema. Tags are written in ascending order, as
// Heat2 encoders emit them.

const (
	ComponentUserSessions uint16 = 0x7802

	NotifyUserSessionExtendedDataUpdate uint16 = 0x0001
	NotifyUserAdded                     uint16 = 0x0002
	NotifyUserRemoved                   uint16 = 0x0003
	NotifyUserSessionDisconnected       uint16 = 0x0004
	NotifyUserUpdated                   uint16 = 0x0005

	// UserDataFlags: bit0 subscribed, bit1 online.
	userStatusOnline uint64 = 0x3

	localeEnUS uint64 = 0x656E5553 // 'enUS'
)

// Component ids advertised in preAuth CIDS (the usual Blaze 3 set);
// unimplemented RPCs get the generic reply from dispatch().
var advertisedComponents = []uint64{
	0x0001, // Authentication
	0x0004, // GameManager
	0x0007, // Stats
	0x0009, // Util
	0x000F, // Messaging
	0x0019, // AssociationLists
	0x001C, // GameReporting
	0x07D0, // (2000) Example/Dynamic
	0x7800, // (30720)
	0x7801, // (30721)
	0x7802, // UserSessions
	0x7803, // (30723)
	0x7805, // (30725)
	0x7806, // (30726)
	0xF802, // (63490)
}

func writeVarList(w *blaze.Writer, tag string, vals []uint64) {
	w.Tag(tag, blaze.TDFList)
	w.WriteByte(byte(blaze.TDFVarInt))
	w.Var(uint64(len(vals)))
	for _, v := range vals {
		w.Var(v)
	}
}

// writeStringMap writes map<string,string>; keys must already be sorted.
func writeStringMap(w *blaze.Writer, tag string, keys []string, vals map[string]string) {
	w.Tag(tag, blaze.TDFMap)
	w.WriteByte(byte(blaze.TDFString))
	w.WriteByte(byte(blaze.TDFString))
	w.Var(uint64(len(keys)))
	for _, k := range keys {
		w.Var(uint64(len(k) + 1))
		w.WriteString(k)
		w.WriteByte(0)
		v := vals[k]
		w.Var(uint64(len(v) + 1))
		w.WriteString(v)
		w.WriteByte(0)
	}
}

// buildPreAuthResponse builds Util::PreAuthResponse (CIDS is list<uint16>).
func buildPreAuthResponse(version string) []byte { return buildPreAuthResponseQoS(version, nil) }

// qosSites: the QoS ping sites sent in PreAuthResponse.QOSS.
type qosSites struct {
	Host  string
	Port  uint64
	Sites []string // aliases, e.g. "sjc"
}

// writeQosPingSite: QosPingSiteInfo {PSA address, PSP port, SNA site name}.
func writeQosPingSite(w *blaze.Writer, host string, port uint64, name string) {
	w.TagString("PSA", host)
	w.TagVar("PSP", port)
	w.TagString("SNA", name)
}

// buildPreAuthResponseQoS also writes QOSS = QosConfigInfo {BWPS, LNP, LTPS,
// SVID}. Race servers need it: they stop when their own ping site
// (NfsOnline.QosPingSite) is missing from the list.
func buildPreAuthResponseQoS(version string, qos *qosSites) []byte {
	var w blaze.Writer
	w.TagVar("ANON", 0)
	w.TagString("ASRC", "nfsrun-private")
	writeVarList(&w, "CIDS", advertisedComponents)
	w.GroupStart("CONF")
	writeStringMap(&w, "CONF", []string{"pingPeriod", "voipHeadsetUpdateRate"},
		map[string]string{"pingPeriod": "15s", "voipHeadsetUpdateRate": "1000"})
	w.GroupEnd()
	w.TagString("INST", "nfs-the-run-pc")
	w.TagString("NASP", "cem_ea_id")
	w.TagString("PILD", "")
	w.TagString("PLAT", "pc")
	if qos != nil && len(qos.Sites) > 0 {
		w.GroupStart("QOSS")
		w.GroupStart("BWPS")
		writeQosPingSite(&w, qos.Host, qos.Port, qos.Sites[0])
		w.GroupEnd()
		w.TagVar("LNP", 10)
		w.Tag("LTPS", blaze.TDFMap)
		w.WriteByte(byte(blaze.TDFString))
		w.WriteByte(byte(blaze.TDFGroup))
		w.Var(uint64(len(qos.Sites)))
		for _, site := range qos.Sites {
			w.Var(uint64(len(site) + 1))
			w.WriteString(site)
			w.WriteByte(0)
			writeQosPingSite(&w, qos.Host, qos.Port, site)
			w.GroupEnd()
		}
		w.TagVar("SVID", 1161889797)
		w.GroupEnd()
	}
	w.TagString("RSRC", "nfsrun-private")
	w.TagString("SVER", version)
	return w.Bytes()
}

// buildPostAuthResponse builds Util::PostAuthResponse (PSS, TELE, TICK, UROP).
// Telemetry is present but disabled (SPCT 0, opt-out).
func buildPostAuthResponse(per database.Persona, host string) []byte {
	var w blaze.Writer

	w.GroupStart("PSS")
	w.TagString("ADRS", host)
	w.TagString("PJID", "")
	w.TagVar("PORT", 0)
	w.TagVar("RPRT", 0)
	w.TagVar("TIID", 0)
	w.GroupEnd()

	w.GroupStart("TELE")
	writeTelemetryFields(&w, host)
	w.GroupEnd()

	w.GroupStart("TICK")
	w.TagString("ADRS", host)
	w.TagVar("PORT", 8999)
	w.TagString("SKEY", "")
	w.GroupEnd()

	w.GroupStart("UROP")
	w.TagVar("TMOP", 0)
	w.TagVar("UID", per.BlazeID)
	w.GroupEnd()

	return w.Bytes()
}

// buildPingResponse — Util::ping (0x0002) returns server time.
func buildPingResponse(now time.Time) []byte {
	var w blaze.Writer
	w.TagVar("STIM", uint64(now.Unix()))
	return w.Bytes()
}

func writeUserIdentification(w *blaze.Writer, tag string, per database.Persona) {
	w.GroupStart(tag)
	w.TagVar("AID", per.AccountID)
	w.TagVar("ALOC", localeEnUS)
	w.TagBlob("EXBB", nil)
	w.TagVar("EXID", per.BlazeID) // = Origin persona id (launcher)
	w.TagVar("ID", per.BlazeID)   // must equal SessionInfo.BUID
	w.TagString("NAME", per.Name)
	w.GroupEnd()
}

// netInfo is what UserSessions.updateNetworkInfo / updateHardwareFlags report.
type netInfo struct {
	Set              bool
	ExtIP, ExtPort   uint64
	IntIP, IntPort   uint64
	DBPS, NATT, UBPS uint64
	HWFlags          uint64
}

func writeUserSessionExtendedData(w *blaze.Writer, tag string) {
	writeUserSessionExtendedDataNet(w, tag, netInfo{})
}

func writeUserSessionExtendedDataNet(w *blaze.Writer, tag string, n netInfo, objs ...objectID) {
	w.GroupStart(tag)
	writeUserSessionExtendedDataFields(w, n, objs...)
	w.GroupEnd()
}

// objectID is a BlazeObjectId: component, entity type, id.
type objectID [3]uint64

func gameObject(gid uint32) objectID { return objectID{uint64(ComponentGameManager), 1, uint64(gid)} }
func playgroupObject(pgid uint32) objectID {
	return objectID{uint64(ComponentPlaygroups), 1, uint64(pgid)}
}

// writeUserSessionExtendedDataFields: UserSessionExtendedData members in tag
// order (ADDR BPS CMAP CTY CVAR DMAP HWFG PSLM QDAT UATT ULST; the unset ones
// keep their defaults). ULST lists the game and playgroup the user is in: an
// accepted invite looks the inviter up and joins what it finds there
// (fb::BlazeInviteAcceptedHandler::onUserLookup).
func writeUserSessionExtendedDataFields(w *blaze.Writer, n netInfo, objs ...objectID) {
	if n.Set {
		w.UnionStart("ADDR", 2) // IpPairAddress
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
	} else {
		w.UnionStart("ADDR", 0x7f) // unset NetworkAddress
	}
	w.TagString("BPS", "")
	w.TagString("CTY", "")
	w.EmptyMap("DMAP", blaze.TDFVarInt, blaze.TDFVarInt)
	w.TagVar("HWFG", n.HWFlags)
	w.EmptyList("PSLM", blaze.TDFVarInt)
	w.GroupStart("QDAT")
	w.TagVar("DBPS", n.DBPS)
	w.TagVar("NATT", n.NATT)
	w.TagVar("UBPS", n.UBPS)
	w.GroupEnd()
	w.TagVar("UATT", 0)
	if len(objs) > 0 {
		w.Tag("ULST", blaze.TDFList)
		w.WriteByte(byte(blaze.TDFTriple))
		w.Var(uint64(len(objs)))
		for _, o := range objs {
			w.Var(o[0])
			w.Var(o[1])
			w.Var(o[2])
		}
	}
}

// memberships: the game and playgroup a user is in, for ULST.
func (s *Server) memberships(st *connState, blazeID uint64) []objectID {
	var out []objectID
	if st != nil {
		s.gm.mu.Lock()
		if g := st.game; g != nil && s.gm.games[g.id] == g {
			out = append(out, gameObject(g.id))
		}
		s.gm.mu.Unlock()
	}
	if id, _, ok := s.pgStatus(blazeID); ok {
		out = append(out, playgroupObject(id))
	}
	return out
}

func buildNotifyUserAdded(per database.Persona) []byte {
	var w blaze.Writer
	writeUserSessionExtendedData(&w, "DATA")
	writeUserIdentification(&w, "USER", per)
	return w.Bytes()
}

func buildUserStatus(per database.Persona, flags uint64) []byte {
	var w blaze.Writer
	w.TagVar("FLGS", flags)
	w.TagVar("ID", per.BlazeID)
	return w.Bytes()
}

func notifyPacket(component, command uint16, payload []byte) *blaze.Packet {
	return &blaze.Packet{Component: component, Command: command, QType: blaze.TypeNotify, ID: 0, Payload: payload}
}

// loginNotifications is what a Blaze server pushes once a persona is logged in:
// UserAdded for the local user, then UserUpdated marking it online.
func loginNotifications(per database.Persona) []*blaze.Packet {
	return []*blaze.Packet{
		notifyPacket(ComponentUserSessions, NotifyUserAdded, buildNotifyUserAdded(per)),
		notifyPacket(ComponentUserSessions, NotifyUserUpdated, buildUserStatus(per, userStatusOnline)),
	}
}

// writeTelemetryFields: GetTelemetryServerResponse members (postAuth TELE and
// Util.getTelemetryServer). Telemetry stays disabled (SPCT 0).
func writeTelemetryFields(w *blaze.Writer, host string) {
	w.TagString("ADRS", host)
	w.TagVar("ANON", 0)
	w.TagString("DISA", "")
	w.TagString("FILT", "")
	w.TagVar("LOC", localeEnUS)
	w.TagString("NOOK", "")
	w.TagVar("PORT", 9988)
	w.TagVar("SDLY", 15000)
	w.TagString("SESS", "nfsrun")
	w.TagString("SKEY", "")
	w.TagVar("SPCT", 0)
	w.TagString("STIM", "")
}
