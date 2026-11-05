package server

import (
	"time"

	"nfsrun-server/internal/blaze"
)

// Small endpoints answered before the per-component handlers.

const (
	USUpdateHardwareFlags uint16 = 0x0008
	USUpdateNetworkInfo   uint16 = 0x0014
	USResumeSession       uint16 = 0x0023
)

func (s *Server) handleMisc(p *blaze.Packet, fields []blaze.Field, state *connState) (*blaze.Packet, bool) {
	switch p.Component {
	case ComponentUserSessions:
		switch p.Command {
		case USUpdateNetworkInfo:
			n := state.Net
			n.Set = true
			for _, f := range fields {
				if f.Tag == "ADDR" {
					if m, ok := f.Value.(map[string]any); ok {
						if fv, ok := m["field"].(blaze.Field); ok {
							if g, ok := fv.Value.([]blaze.Field); ok {
								for _, part := range g {
									pg, _ := part.Value.([]blaze.Field)
									switch part.Tag {
									case "EXIP":
										n.ExtIP, n.ExtPort = fieldUintAuth(pg, "IP"), fieldUintAuth(pg, "PORT")
									case "INIP":
										n.IntIP, n.IntPort = fieldUintAuth(pg, "IP"), fieldUintAuth(pg, "PORT")
									}
								}
							}
						}
					}
				}
				if f.Tag == "NQOS" {
					if g, ok := f.Value.([]blaze.Field); ok {
						n.DBPS, n.NATT, n.UBPS = fieldUintAuth(g, "DBPS"), fieldUintAuth(g, "NATT"), fieldUintAuth(g, "UBPS")
					}
				}
			}
			if n.ExtIP == 0 && state.peer != "" { // the client could not see its public IP: use the socket address
				n.ExtIP = ipUint(remoteIP(state.peer))
				if n.ExtPort == 0 {
					n.ExtPort = n.IntPort
				}
			}
			state.Net = n
			s.log.Printf("[USESS] updateNetworkInfo: int %s:%d ext %s:%d NAT=%d", ipString(n.IntIP), n.IntPort, ipString(n.ExtIP), n.ExtPort, n.NATT)
			s.queueExtendedDataUpdate(state)
			return s.response(p, nil), true
		case USUpdateHardwareFlags:
			state.Net.HWFlags = fieldUintAuth(fields, "HWFG")
			s.queueExtendedDataUpdate(state)
			return s.response(p, nil), true
		case USResumeSession:
			key := blaze.FindString(fields, "SKEY")
			per, ok, err := s.db.PersonaBySessionKey(key)
			if err != nil || !ok {
				s.log.Printf("[USESS] resumeSession: unknown/expired key (%v)", err)
				return s.errorResp(p, 1), true
			}
			state.Persona, state.Authenticated = per, true
			s.online.set(state, per.Name)
			state.Email = per.Email
			if !state.UserAnnounced {
				state.Pending = append(state.Pending, loginNotifications(per)...)
				state.UserAnnounced = true
				s.announceOnline(state)
			}
			s.log.Printf("[USESS] resumeSession: %q (blazeId=%d) resumed", per.Name, per.BlazeID)
			return s.response(p, nil), true
		}
		if resp, ok := s.handleUserLookup(p, fields, state); ok {
			return resp, true
		}
	case 0x0009: // Util
		switch p.Command {
		case 0x0005: // getTelemetryServer
			var w blaze.Writer
			writeTelemetryFields(&w, s.cfg.PublicHost)
			return s.response(p, w.Bytes()), true
		case 0x0016: // setClientMetrics: accepted, not stored
			return s.response(p, nil), true
		}
	case 0x0001: // Authentication
		switch p.Command {
		case 0x001E: // getAccount -> AccountInfo
			gopt, tpot := s.accountOptins(state)
			return s.response(p, buildAccountInfo(state, time.Now(), gopt, tpot)), true
		case 0x0014: // updateAccount -> UpdateAccountResponse{PCTK}
			return s.handleUpdateAccount(p, fields, state), true
		case 0x002A, 0x00F6, 0x002F: // getTosInfo, getTermsOfServiceContent, getPrivacyPolicyContent (legal.go)
			return s.handleLegal(p, fields)
		case 0x00E6: // createWalUserSession -> SessionInfo
			// same session as loginPersona
			if state.Persona.BlazeID == 0 {
				return s.errorResp(p, 0x4004), true // ERR_AUTHENTICATION_REQUIRED (0x40040000)
			}
			return s.response(p, buildLoginPersonaResponse(state.Persona, state.Email)), true
		}
	case 0x000F: // Messaging.fetchMessages -> FetchMessageResponse{MCNT}
		if p.Command == 0x0002 {
			var w blaze.Writer
			w.TagVar("MCNT", 0)
			return s.response(p, w.Bytes()), true
		}
	case ComponentAssociationLists:
		if p.Command == 0x0004 { // setUsersToList -> UpdateListMembersResponse{LMID, REM}
			var w blaze.Writer
			writeStructListHeader(&w, "LMID", 0)
			writeStructListHeader(&w, "REM", 0)
			return s.response(p, w.Bytes()), true
		}
	case ComponentLocker:
		return s.handleLocker(p, fields, state), true
	}
	return nil, false
}

// queueExtendedDataUpdate sends NotifyUserSessionExtendedDataUpdate (0x7802/1)
// with the current network data, as a Blaze server does after updates.
func (s *Server) queueExtendedDataUpdate(state *connState) {
	if state.Persona.BlazeID == 0 {
		return
	}
	var w blaze.Writer
	writeUserSessionExtendedDataNet(&w, "DATA", state.Net, s.memberships(state, state.Persona.BlazeID)...)
	w.TagVar("USID", state.Persona.BlazeID)
	state.Pending = append(state.Pending, notifyPacket(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, w.Bytes()))
}

// AccountInfo { AMU, ASRC, CO, DOB, DTCR, GOPT, LATH, LN, MAIL, PML, RC, STAS, STAT, TOSV, TPOT, UDU, UID }
func buildAccountInfo(state *connState, now time.Time, gopt, tpot uint64) []byte {
	var w blaze.Writer
	w.TagBool("AMU", false)
	w.TagString("ASRC", "nfsrun-private")
	w.TagString("CO", "US")
	w.TagString("DOB", "1990-01-01")
	w.TagString("DTCR", now.UTC().Format("2006-01-02"))
	w.TagVar("GOPT", gopt)
	w.TagString("LATH", now.UTC().Format("2006-01-02T15:04Z"))
	w.TagString("LN", "en")
	w.TagString("MAIL", state.Email)
	w.TagString("PML", "")
	w.TagVar("RC", 0)
	w.TagVar("STAS", 0)
	w.TagVar("STAT", 1) // ACTIVE
	w.TagString("TOSV", "")
	w.TagVar("TPOT", tpot)
	w.TagBool("UDU", false)
	w.TagVar("UID", state.Persona.AccountID)
	return w.Bytes()
}

func remoteIP(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}

func ipString(v uint64) string {
	return itoa(v>>24&255) + "." + itoa(v>>16&255) + "." + itoa(v>>8&255) + "." + itoa(v&255)
}

// serverName: SVER in Util::preAuth.
const serverName = "NFSRunEmu"
