package database

// Server-owner commands (server window / command line): delete, rename,
// pending launcher codes.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// CheckPersonaName: 1-16 characters, no control characters, no tab/line
// breaks (the same rule the launcher uses).
func CheckPersonaName(name string) error {
	n := utf8.RuneCountInString(name)
	if n == 0 || n > 16 {
		return fmt.Errorf("name %q has %d characters; use 1..16", name, n)
	}
	if strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("name %q contains control characters", name)
	}
	return nil
}

// PersonaInfo is one persona as the server owner sees it.
type PersonaInfo struct {
	ID, AccountID, BlazeID uint64
	Name                   string
	Created, LastLogin     string // "YYYY-MM-DD HH:MM:SS", LastLogin may be ""
	Bound                  bool   // a launcher key owns it
	Photos                 int
}

// FindPersona looks a persona up by name (case-insensitive, like the
// database's unique key).
func (d *DB) FindPersona(name string) (PersonaInfo, bool, error) {
	list, err := d.listPersonaInfo(fmt.Sprintf("WHERE p.name='%s'", esc(strings.TrimSpace(name))), 1)
	if err != nil || len(list) == 0 {
		return PersonaInfo{}, false, err
	}
	return list[0], true, nil
}

// ListPersonaInfo: every persona, most recently seen first.
func (d *DB) ListPersonaInfo(limit int) ([]PersonaInfo, error) {
	if limit <= 0 {
		limit = 500
	}
	return d.listPersonaInfo("", limit)
}

func (d *DB) listPersonaInfo(where string, limit int) ([]PersonaInfo, error) {
	rows, err := d.query(fmt.Sprintf(`SELECT p.id, p.account_id, p.blaze_id, REPLACE(REPLACE(p.name,char(9),' '),char(10),' '),
p.created_at, COALESCE(p.last_login_at,'-'),
(SELECT COUNT(*) FROM launcher_credentials lc WHERE lc.persona_id=p.id),
(SELECT COUNT(*) FROM locker_contents c WHERE c.persona_id=p.id)
FROM personas p %s ORDER BY COALESCE(p.last_login_at, p.created_at) DESC LIMIT %d;`, where, limit))
	if err != nil {
		return nil, err
	}
	var out []PersonaInfo
	for _, r := range rows {
		f := strings.Split(r, "\t")
		if len(f) < 8 {
			continue
		}
		var pi PersonaInfo
		pi.ID, _ = strconv.ParseUint(f[0], 10, 64)
		pi.AccountID, _ = strconv.ParseUint(f[1], 10, 64)
		pi.BlazeID, _ = strconv.ParseUint(f[2], 10, 64)
		pi.Name, pi.Created, pi.LastLogin = f[3], f[4], f[5]
		if pi.LastLogin == "-" {
			pi.LastLogin = ""
		}
		bound, _ := strconv.Atoi(f[6])
		pi.Bound = bound > 0
		pi.Photos, _ = strconv.Atoi(f[7])
		out = append(out, pi)
	}
	return out, nil
}

// DeletePersona removes a persona and everything that belongs to it (the
// foreign keys cascade: sessions, launcher key, friends, stats, records,
// Autolog stories, photos and likes; game reports keep a NULL owner). The
// account goes too when it has no other persona. Returns the deleted persona
// and the ids of its photos (their files live in data\locker\<id>).
func (d *DB) DeletePersona(name string) (PersonaInfo, []uint64, error) {
	pi, ok, err := d.FindPersona(name)
	if err != nil {
		return PersonaInfo{}, nil, err
	}
	if !ok {
		return PersonaInfo{}, nil, fmt.Errorf("player %q not found", name)
	}
	var photos []uint64
	rows, err := d.query(fmt.Sprintf("SELECT id FROM locker_contents WHERE persona_id=%d;", pi.ID))
	if err != nil {
		return PersonaInfo{}, nil, err
	}
	for _, r := range rows {
		if id, e := strconv.ParseUint(strings.TrimSpace(r), 10, 64); e == nil {
			photos = append(photos, id)
		}
	}
	_, err = d.query(fmt.Sprintf(`DELETE FROM launcher_claims WHERE persona_name='%s';
DELETE FROM personas WHERE id=%d;
DELETE FROM accounts WHERE id=%d AND NOT EXISTS (SELECT 1 FROM personas WHERE account_id=%d);`, esc(pi.Name), pi.ID, pi.AccountID, pi.AccountID))
	if err != nil {
		return PersonaInfo{}, nil, err
	}
	return pi, photos, nil
}

// RenamePersona changes a persona's name. Records, photos, friends and the
// launcher key stay: a launcher key always resolves to its persona, whatever
// PERSONA its .ini says.
func (d *DB) RenamePersona(oldName, newName string) (PersonaInfo, error) {
	newName = strings.TrimSpace(newName)
	if err := CheckPersonaName(newName); err != nil {
		return PersonaInfo{}, err
	}
	pi, ok, err := d.FindPersona(oldName)
	if err != nil {
		return PersonaInfo{}, err
	}
	if !ok {
		return PersonaInfo{}, fmt.Errorf("player %q not found", oldName)
	}
	if other, taken, err := d.FindPersona(newName); err != nil {
		return PersonaInfo{}, err
	} else if taken && other.ID != pi.ID {
		return PersonaInfo{}, fmt.Errorf("the name %q is already used", newName)
	}
	// The launcher's account e-mail is derived from the name
	// (eatest.<name>@ea.com): it follows, so a later reset-key works with the
	// new PERSONA.
	if _, err := d.query(fmt.Sprintf(`UPDATE personas SET name='%s' WHERE id=%d;
UPDATE launcher_claims SET persona_name='%s' WHERE persona_name='%s';
UPDATE accounts SET email='%s' WHERE id=%d AND email='%s'
  AND (SELECT COUNT(*) FROM personas WHERE account_id=%d)=1
  AND NOT EXISTS (SELECT 1 FROM (SELECT id FROM accounts WHERE email='%s') x);`,
		esc(newName), pi.ID, esc(newName), esc(pi.Name),
		esc("eatest."+newName+"@ea.com"), pi.AccountID, esc("eatest."+pi.Name+"@ea.com"), pi.AccountID, esc("eatest."+newName+"@ea.com"))); err != nil {
		return PersonaInfo{}, err
	}
	pi.Name = newName
	return pi, nil
}

// LauncherClaim is a pending one-time launcher code (only its hash is
// stored; the code itself was shown once).
type LauncherClaim struct {
	Name    string
	Expires time.Time
}

// ListLauncherClaims: codes not used yet and not expired.
func (d *DB) ListLauncherClaims() ([]LauncherClaim, error) {
	rows, err := d.query(`SELECT REPLACE(REPLACE(persona_name,char(9),' '),char(10),' '), UNIX_TIMESTAMP(expires_at) FROM launcher_claims
WHERE expires_at>NOW() ORDER BY expires_at;`)
	if err != nil {
		return nil, err
	}
	var out []LauncherClaim
	for _, r := range rows {
		f := strings.Split(r, "\t")
		if len(f) < 2 {
			continue
		}
		ts, _ := strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
		out = append(out, LauncherClaim{Name: f[0], Expires: time.Unix(int64(ts), 0)})
	}
	return out, nil
}

// CancelLauncherClaim deletes the pending code of a name. found = there was one.
func (d *DB) CancelLauncherClaim(name string) (bool, error) {
	rows, err := d.query(fmt.Sprintf(`SELECT COUNT(*) FROM launcher_claims WHERE persona_name='%s' AND expires_at>NOW();
DELETE FROM launcher_claims WHERE persona_name='%s';`, esc(strings.TrimSpace(name)), esc(strings.TrimSpace(name))))
	if err != nil {
		return false, err
	}
	return len(rows) > 0 && strings.TrimSpace(rows[0]) != "0", nil
}

// LauncherNameReserved: the name has a pending (unexpired) launcher code, so
// only the player with that code may claim it.
func (d *DB) LauncherNameReserved(name string) (bool, error) {
	rows, err := d.query(fmt.Sprintf("SELECT 1 FROM launcher_claims WHERE persona_name='%s' AND expires_at>NOW() LIMIT 1;", esc(strings.TrimSpace(name))))
	return len(rows) > 0, err
}
