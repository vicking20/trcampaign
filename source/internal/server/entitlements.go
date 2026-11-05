package server

import (
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Authentication.listUserEntitlements2 (0x001D).
// Request: BUID, GNLS ["NFS12PC"], EPSN 1, EPSZ 50. Every player gets one
// ACTIVE ONLINE_ACCESS entitlement per requested group, so online-pass checks
// pass (client_config NFS12_THERUN.cfg also sets OnlinePassKillSwitch=true).
//
// Entitlements { NLST list<Entitlement{DEVI,GDAY,GNAM,ID,ISCO,PID,PJID,PRCA,PRID,STAT,STRC,TAG,TDAY,TYPE,UCNT,VER}> }

const (
	AuthListUserEntitlements2 uint16 = 0x001D
	entitlementActive         uint64 = 1
	entitlementOnlineAccess   uint64 = 1
)

func buildEntitlements(groups []string, per database.Persona, now time.Time) []byte {
	if len(groups) == 0 {
		groups = []string{"NFS12PC"}
	}
	day := now.UTC().Format("2006-01-02T15:04Z")
	var w blaze.Writer
	writeStructListHeader(&w, "NLST", len(groups))
	for i, g := range groups {
		w.TagString("DEVI", "")
		w.TagString("GDAY", day)
		w.TagString("GNAM", g)
		w.TagVar("ID", uint64(1000+i))
		w.TagBool("ISCO", false)
		w.TagVar("PID", per.ID)
		w.TagString("PJID", "")
		w.TagVar("PRCA", 0)
		w.TagString("PRID", "")
		w.TagVar("STAT", entitlementActive)
		w.TagVar("STRC", 0)
		w.TagString("TAG", "ONLINE_ACCESS")
		w.TagString("TDAY", "")
		w.TagVar("TYPE", entitlementOnlineAccess)
		w.TagVar("UCNT", 0)
		w.TagVar("VER", 0)
		w.GroupEnd()
	}
	return w.Bytes()
}

func stringList(fs []blaze.Field, tag string) []string {
	for _, f := range fs {
		if f.Tag != tag {
			continue
		}
		m, _ := f.Value.(map[string]any)
		items, _ := m["items"].([]any)
		var out []string
		for _, it := range items {
			if v, ok := it.(string); ok && v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

func personaFromRef(r database.PersonaRef) database.Persona {
	return database.Persona{ID: r.ID, AccountID: r.AccountID, BlazeID: r.BlazeID, Name: r.Name}
}
