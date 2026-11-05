package database

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Persona struct {
	ID         uint64
	AccountID  uint64
	BlazeID    uint64
	Name       string
	Email      string
	SessionKey string
}

func token() string { var b [24]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

// launcherToken makes a launcher session ticket; fails if the OS RNG fails.
func launcherToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("launcher session entropy: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func parsePersonaRow(row string) (Persona, error) {
	p := strings.Split(row, "\t")
	if len(p) == 4 { // tolerate a stripped empty email column
		p = append(p, "")
	}
	if len(p) < 5 {
		return Persona{}, fmt.Errorf("unexpected persona row %q", row)
	}
	id, _ := strconv.ParseUint(p[0], 10, 64)
	aid, _ := strconv.ParseUint(p[1], 10, 64)
	bid, _ := strconv.ParseUint(p[2], 10, 64)
	return Persona{ID: id, AccountID: aid, BlazeID: bid, Name: p[3], Email: p[4]}, nil
}

func (d *DB) issueSession(per Persona) (Persona, error) {
	sk := token()
	// Other valid tickets of the persona stay: the launcher and the Blaze
	// connection may hold different keys at the same time.
	_, e := d.query(fmt.Sprintf("UPDATE personas SET last_login_at=NOW() WHERE id=%d; DELETE FROM sessions WHERE persona_id=%d AND expires_at<=NOW(); INSERT INTO sessions(persona_id,session_key,created_at,expires_at) VALUES(%d,'%s',NOW(),DATE_ADD(NOW(),INTERVAL 7 DAY));", per.ID, per.ID, per.ID, sk))
	if e != nil {
		return Persona{}, e
	}
	per.SessionKey = sk
	return per, nil
}

// IssueGameSession creates the Blaze session key of a game connection
// (kind "blaze"), separate from the launcher ticket.
func (d *DB) IssueGameSession(per Persona, ttl time.Duration) (string, error) {
	sk, err := launcherToken()
	if err != nil {
		return "", err
	}
	seconds := int(ttl / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	_, err = d.query(fmt.Sprintf(`DELETE FROM sessions WHERE persona_id=%d AND expires_at<=NOW();
INSERT INTO sessions(persona_id,session_key,session_kind,created_at,expires_at)
VALUES(%d,'%s','blaze',NOW(),DATE_ADD(NOW(),INTERVAL %d SECOND));`, per.ID, per.ID, esc(sk), seconds))
	if err != nil {
		return "", err
	}
	return sk, nil
}

func validPublicKeyHex(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) != 64 {
		return false
	}
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32
}

func claimCodeHash(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func normalizeClaimCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	if len(code) == 10 {
		return code[:5] + "-" + code[5:]
	}
	return code
}

func randomClaimCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Base32 without punctuation, cut to 10 characters (50 random bits) so the
	// code is easy to type.
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	raw = raw[:10]
	return raw[:5] + "-" + raw[5:], nil
}

// issueLauncherSession creates the short-lived ticket used by HTTP, LSX and
// Blaze after the launcher proved its Ed25519 key (credentialKey: hex public key).
func (d *DB) issueLauncherSession(per Persona, credentialKey string, ttl time.Duration) (Persona, int64, error) {
	credentialKey = strings.ToLower(strings.TrimSpace(credentialKey))
	if !validPublicKeyHex(credentialKey) {
		return Persona{}, 0, fmt.Errorf("invalid launcher public key")
	}
	seconds := int64(ttl / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	sk, err := launcherToken()
	if err != nil {
		return Persona{}, 0, err
	}
	// A new signed login revokes every older ticket of the persona.
	rows, err := d.query(fmt.Sprintf(`UPDATE personas SET last_login_at=NOW() WHERE id=%d;
DELETE FROM sessions WHERE persona_id=%d;
DELETE FROM sessions WHERE expires_at<=NOW();
INSERT INTO sessions(persona_id,session_key,session_kind,credential_key,created_at,expires_at)
VALUES(%d,'%s','launcher-v3','%s',NOW(),DATE_ADD(NOW(),INTERVAL %d SECOND));
SELECT UNIX_TIMESTAMP(expires_at) FROM sessions WHERE session_key='%s' LIMIT 1;`,
		per.ID, per.ID, per.ID, esc(sk), esc(credentialKey), seconds, esc(sk)))
	if err != nil {
		return Persona{}, 0, err
	}
	var expires int64
	if len(rows) > 0 {
		expires, _ = strconv.ParseInt(strings.TrimSpace(rows[len(rows)-1]), 10, 64)
	}
	per.SessionKey = sk
	return per, expires, nil
}

// RenewLauncherSession rotates the ticket owned by publicKey. The old ticket
// stays valid until its own expiry, so an auth code the game cached keeps
// working; new requests use the returned ticket.
func (d *DB) RenewLauncherSession(publicKey, sessionKey string, ttl time.Duration) (Persona, int64, bool, error) {
	publicKey = strings.ToLower(strings.TrimSpace(publicKey))
	sessionKey = strings.ToLower(strings.TrimSpace(sessionKey))
	if !validPublicKeyHex(publicKey) || sessionKey == "" {
		return Persona{}, 0, false, nil
	}
	seconds := int64(ttl / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	rows, err := d.query(fmt.Sprintf(`SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,'') FROM sessions s
JOIN personas p ON p.id=s.persona_id JOIN accounts a ON a.id=p.account_id
JOIN launcher_credentials lc ON lc.persona_id=p.id
WHERE s.session_key='%s' AND s.session_kind='launcher-v3' AND s.credential_key='%s'
  AND lc.public_key='%s' AND s.expires_at>DATE_SUB(NOW(), INTERVAL 5 MINUTE) LIMIT 1;`,
		esc(sessionKey), esc(publicKey), esc(publicKey)))
	if err != nil || len(rows) == 0 {
		return Persona{}, 0, false, err
	}
	per, err := parsePersonaRow(rows[0])
	if err != nil {
		return Persona{}, 0, false, err
	}

	fresh, err := launcherToken()
	if err != nil {
		return Persona{}, 0, false, err
	}
	rows, err = d.query(fmt.Sprintf(`UPDATE personas SET last_login_at=NOW() WHERE id=%d;
DELETE FROM sessions WHERE expires_at<=NOW();
INSERT INTO sessions(persona_id,session_key,session_kind,credential_key,created_at,expires_at)
VALUES(%d,'%s','launcher-v3','%s',NOW(),DATE_ADD(NOW(),INTERVAL %d SECOND));
UPDATE launcher_credentials SET last_seen_at=NOW() WHERE public_key='%s';
SELECT UNIX_TIMESTAMP(expires_at) FROM sessions WHERE session_key='%s' LIMIT 1;`,
		per.ID, per.ID, esc(fresh), esc(publicKey), seconds, esc(publicKey), esc(fresh)))
	if err != nil {
		return Persona{}, 0, false, err
	}
	var expires int64
	if len(rows) > 0 {
		expires, _ = strconv.ParseInt(strings.TrimSpace(rows[len(rows)-1]), 10, 64)
	}
	per.SessionKey = fresh
	return per, expires, true, nil
}

// GetOrCreateIdentity stores the identity the game presents: MAIL is the
// account key, PNAM (or the derived name) its persona. No password is stored.
func (d *DB) GetOrCreateIdentity(email, name string) (Persona, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "LocalPlayer"
	}

	selectByName := fmt.Sprintf("SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,'') FROM personas p JOIN accounts a ON a.id=p.account_id WHERE p.name='%s' LIMIT 1;", esc(name))
	rows, e := d.query(selectByName)
	if e != nil {
		return Persona{}, e
	}
	if len(rows) > 0 {
		per, e := parsePersonaRow(rows[0])
		if e != nil {
			return Persona{}, e
		}
		if email != "" {
			// link the email to rows created without it
			if per.Email == "" {
				if _, e = d.query(fmt.Sprintf("UPDATE accounts SET email='%s' WHERE id=%d AND email IS NULL;", esc(email), per.AccountID)); e != nil {
					return Persona{}, e
				}
				per.Email = email
			} else if !strings.EqualFold(strings.TrimSpace(per.Email), email) {
				// a name owned by another account never logs in as that persona
				return Persona{}, fmt.Errorf("persona name %q already belongs to another account", name)
			}
		}
		return d.issueSession(per)
	}

	var accountID uint64
	if email != "" {
		aRows, e := d.query(fmt.Sprintf("SELECT id FROM accounts WHERE email='%s' LIMIT 1;", esc(email)))
		if e != nil {
			return Persona{}, e
		}
		if len(aRows) > 0 {
			accountID, _ = strconv.ParseUint(strings.TrimSpace(aRows[0]), 10, 64)
		}
	}
	if accountID == 0 {
		if email != "" {
			rows, e = d.query(fmt.Sprintf("INSERT INTO accounts(email,created_at) VALUES('%s',NOW()); SELECT LAST_INSERT_ID();", esc(email)))
		} else {
			rows, e = d.query("INSERT INTO accounts(created_at) VALUES(NOW()); SELECT LAST_INSERT_ID();")
		}
		if e != nil {
			return Persona{}, e
		}
		if len(rows) == 0 {
			return Persona{}, fmt.Errorf("account insert returned no id")
		}
		accountID, _ = strconv.ParseUint(strings.TrimSpace(rows[len(rows)-1]), 10, 64)
	}

	// The persona's blaze_id is 1000000+account id; further personas of the
	// same account (race servers share one account) take the next free id.
	_, e = d.query(fmt.Sprintf("INSERT INTO personas(account_id,blaze_id,name,created_at,last_login_at) SELECT %d, IF(COALESCE(SUM(blaze_id=%d),0)>0, MAX(blaze_id)+1, %d), '%s', NOW(), NOW() FROM personas;", accountID, 1000000+accountID, 1000000+accountID, esc(name)))
	if e != nil {
		return Persona{}, e
	}
	rows, e = d.query(selectByName)
	if e != nil || len(rows) == 0 {
		if e == nil {
			e = fmt.Errorf("persona insert could not be read back")
		}
		return Persona{}, e
	}
	per, e := parsePersonaRow(rows[0])
	if e != nil {
		return Persona{}, e
	}
	return d.issueSession(per)
}

// LauncherPublicKeyKnown reports whether a public key is paired, so
// AUTH_MODE=invite asks for an invite code only on first pairing.
func (d *DB) LauncherPublicKeyKnown(publicKey string) (bool, error) {
	publicKey = strings.ToLower(strings.TrimSpace(publicKey))
	if !validPublicKeyHex(publicKey) {
		return false, nil
	}
	rows, err := d.query(fmt.Sprintf("SELECT 1 FROM launcher_credentials WHERE public_key='%s' LIMIT 1;", esc(publicKey)))
	return err == nil && len(rows) > 0, err
}

// AuthenticateLauncherPublicKey resolves or claims the persona of an Ed25519
// public key and issues a session. The caller has already verified the
// challenge signature; private keys never reach the server.
func (d *DB) AuthenticateLauncherPublicKey(email, name, publicKey, claimCode, authMode string, ttl time.Duration) (Persona, int64, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	publicKey = strings.ToLower(strings.TrimSpace(publicKey))
	authMode = strings.ToLower(strings.TrimSpace(authMode))
	if name == "" {
		return Persona{}, 0, fmt.Errorf("empty persona name")
	}
	if !validPublicKeyHex(publicKey) {
		return Persona{}, 0, fmt.Errorf("invalid launcher public key")
	}

	// a known key always resolves to its persona, whatever PERSONA says
	rows, err := d.query(fmt.Sprintf(`SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,'') FROM launcher_credentials lc
JOIN personas p ON p.id=lc.persona_id JOIN accounts a ON a.id=p.account_id
WHERE lc.public_key='%s' LIMIT 1;`, esc(publicKey)))
	if err != nil {
		return Persona{}, 0, err
	}
	if len(rows) > 0 {
		per, err := parsePersonaRow(rows[0])
		if err != nil {
			return Persona{}, 0, err
		}
		_, _ = d.query(fmt.Sprintf("UPDATE launcher_credentials SET last_seen_at=NOW() WHERE public_key='%s';", esc(publicKey)))
		return d.issueLauncherSession(per, publicKey, ttl)
	}

	// a persona owned by a public key never moves to another key
	rows, err = d.query(fmt.Sprintf(`SELECT lc.public_key FROM launcher_credentials lc
JOIN personas p ON p.id=lc.persona_id WHERE p.name='%s' LIMIT 1;`, esc(name)))
	if err != nil {
		return Persona{}, 0, err
	}
	if len(rows) > 0 {
		return Persona{}, 0, fmt.Errorf("persona %q is already claimed by another launcher installation", name)
	}

	if authMode == "invite" {
		code := normalizeClaimCode(claimCode)
		if code == "" {
			return Persona{}, 0, fmt.Errorf("this server requires a one-time invite code (INVITE_CODE)")
		}
		hash := claimCodeHash(code)
		rows, err = d.query(fmt.Sprintf(`SELECT 1 FROM launcher_claims
WHERE claim_hash='%s' AND persona_name='%s' AND expires_at>NOW() LIMIT 1;`, esc(hash), esc(name)))
		if err != nil {
			return Persona{}, 0, err
		}
		if len(rows) == 0 {
			return Persona{}, 0, fmt.Errorf("invalid or expired invite code for %q", name)
		}
	} else if authMode == "open" {
		// first come, first served - except a name with a pending code
		// (invite / reset-key): it waits for the player with that code.
		rows, err = d.query(fmt.Sprintf(`SELECT claim_hash FROM launcher_claims
WHERE persona_name='%s' AND expires_at>NOW();`, esc(name)))
		if err != nil {
			return Persona{}, 0, err
		}
		if len(rows) > 0 {
			code := normalizeClaimCode(claimCode)
			if code == "" {
				return Persona{}, 0, fmt.Errorf("persona %q is reserved: it needs a one-time invite code (INVITE_CODE) from the server owner", name)
			}
			match := false
			for _, h := range rows {
				if strings.TrimSpace(h) == claimCodeHash(code) {
					match = true
				}
			}
			if !match {
				return Persona{}, 0, fmt.Errorf("invalid or expired invite code for %q", name)
			}
		}
	} else {
		return Persona{}, 0, fmt.Errorf("unsupported auth mode %q", authMode)
	}

	// UNIQUE(public_key) and UNIQUE(persona_id) make a concurrent double claim fail.
	per, err := d.GetOrCreateIdentity(email, name)
	if err != nil {
		return Persona{}, 0, err
	}
	if _, err := d.query(fmt.Sprintf(`INSERT INTO launcher_credentials(public_key,persona_id,created_at,last_seen_at)
VALUES('%s',%d,NOW(),NOW());`, esc(publicKey), per.ID)); err != nil {
		return Persona{}, 0, fmt.Errorf("claim persona %q: %w", name, err)
	}
	// the persona has its owner now: no code of this name stays usable
	_, _ = d.query(fmt.Sprintf("DELETE FROM launcher_claims WHERE persona_name='%s';", esc(name)))
	return d.issueLauncherSession(per, publicKey, ttl)
}

// CreateLauncherClaim makes a one-time pairing code for AUTH_MODE=invite.
// The persona is created when a launcher uses the code.
func (d *DB) CreateLauncherClaim(name string, ttl time.Duration) (string, time.Time, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", time.Time{}, fmt.Errorf("empty persona name")
	}
	claimed, err := d.LauncherIdentityClaimed(name)
	if err != nil {
		return "", time.Time{}, err
	}
	if claimed {
		return "", time.Time{}, fmt.Errorf("persona %q already has launcher ownership; use reset-key for a lost key", name)
	}
	if ttl < time.Minute {
		ttl = 24 * time.Hour
	}
	code, err := randomClaimCode()
	if err != nil {
		return "", time.Time{}, err
	}
	hash := claimCodeHash(code)
	seconds := int64(ttl / time.Second)
	_, err = d.query(fmt.Sprintf(`DELETE FROM launcher_claims WHERE persona_name='%s' OR expires_at<=NOW();
INSERT INTO launcher_claims(claim_hash,persona_name,created_at,expires_at)
VALUES('%s','%s',NOW(),DATE_ADD(NOW(),INTERVAL %d SECOND));`, esc(name), esc(hash), esc(name), seconds))
	if err != nil {
		return "", time.Time{}, err
	}
	return code, time.Now().Add(ttl), nil
}

// LauncherIdentityClaimed reports whether a launcher key owns a persona.
func (d *DB) LauncherIdentityClaimed(name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, nil
	}
	rows, err := d.query(fmt.Sprintf(`SELECT 1 FROM personas p
JOIN launcher_credentials lc ON lc.persona_id=p.id
WHERE p.name='%s' LIMIT 1;`, esc(name)))
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// ResetLauncherDevice revokes the login material of one persona: its launcher
// key, remembered external aliases and session tickets.
// Account/persona ids, stats, friends and other game data are kept.
func (d *DB) ResetLauncherDevice(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty persona name")
	}
	rows, err := d.query(fmt.Sprintf(`SELECT p.id FROM personas p WHERE p.name='%s' LIMIT 1;`, esc(name)))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("persona %q not found", name)
	}
	id, _ := strconv.ParseUint(strings.TrimSpace(rows[0]), 10, 64)
	_, err = d.query(fmt.Sprintf(`DELETE FROM identity_aliases WHERE persona_id=%d;
DELETE FROM sessions WHERE persona_id=%d;
DELETE FROM launcher_credentials WHERE persona_id=%d;
DELETE FROM launcher_claims WHERE persona_name='%s';`, id, id, id, esc(name)))
	return err
}

// PersonaByIdentityAlias resolves an external identity fingerprint (EA/Origin
// auth token) to its persona.
func (d *DB) PersonaByIdentityAlias(alias string) (Persona, bool, error) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return Persona{}, false, nil
	}
	rows, err := d.query(fmt.Sprintf(`SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,'') FROM identity_aliases ia
JOIN personas p ON p.id=ia.persona_id JOIN accounts a ON a.id=p.account_id
WHERE ia.alias_key='%s' LIMIT 1;`, esc(alias)))
	if err != nil || len(rows) == 0 {
		return Persona{}, false, err
	}
	per, err := parsePersonaRow(rows[0])
	if err != nil {
		return Persona{}, false, err
	}
	_, _ = d.query(fmt.Sprintf("UPDATE identity_aliases SET last_seen_at=NOW() WHERE alias_key='%s';", esc(alias)))
	return per, true, nil
}

// BindIdentityAlias ties an alias to a persona. An alias never moves to
// another persona (players behind one NAT would otherwise swap accounts).
func (d *DB) BindIdentityAlias(alias string, personaID uint64) error {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" || personaID == 0 {
		return nil
	}
	rows, err := d.query(fmt.Sprintf("SELECT persona_id FROM identity_aliases WHERE alias_key='%s' LIMIT 1;", esc(alias)))
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		old, _ := strconv.ParseUint(strings.TrimSpace(rows[0]), 10, 64)
		if old != personaID {
			return fmt.Errorf("identity alias %q is already bound to persona %d", alias, old)
		}
		_, err = d.query(fmt.Sprintf("UPDATE identity_aliases SET last_seen_at=NOW() WHERE alias_key='%s';", esc(alias)))
		return err
	}
	_, err = d.query(fmt.Sprintf("INSERT INTO identity_aliases(alias_key,persona_id,created_at,last_seen_at) VALUES('%s',%d,NOW(),NOW());", esc(alias), personaID))
	return err
}

func (d *DB) ValidateSession(key string) bool {
	if key == "" {
		return false
	}
	rows, e := d.query(fmt.Sprintf("SELECT 1 FROM sessions WHERE session_key='%s' AND expires_at>NOW() LIMIT 1;", esc(key)))
	return e == nil && len(rows) > 0
}
func (d *DB) Touch() {
	_, _ = d.query(fmt.Sprintf("INSERT INTO server_state(state_key,state_value,updated_at) VALUES('last_start','%s',NOW()) ON CONFLICT(state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=datetime('now');", time.Now().UTC().Format(time.RFC3339)))
}

// SaveGameReport stores one raw GameReporting payload. Identical payloads are
// stored once (the client may resend queued offline reports). It returns
// whether the report was new.
func (d *DB) SaveGameReport(personaID uint64, gameType, payloadSHA256, payloadHex, resultsJSON string) (bool, error) {
	pid := "NULL"
	if personaID != 0 {
		pid = strconv.FormatUint(personaID, 10)
	}
	rows, e := d.query(fmt.Sprintf("INSERT IGNORE INTO game_reports(persona_id,game_type,payload_sha256,payload_hex,results_json) VALUES(%s,'%s','%s','%s','%s'); SELECT ROW_COUNT();",
		pid, esc(gameType), esc(payloadSHA256), esc(payloadHex), esc(resultsJSON)))
	if e != nil {
		return false, e
	}
	return len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "1", nil
}

// UpsertPersonalBest keeps the lowest time per (board, event, persona).
// metadata is replaced only when the new time is better.
func (d *DB) UpsertPersonalBest(personaID uint64, board string, eventID, timeMs uint64, metadataJSON string) (bool, error) {
	rows, e := d.query(fmt.Sprintf(`SELECT score FROM leaderboards WHERE board_key='%s' AND event_id=%d AND persona_id=%d;
INSERT INTO leaderboards(board_key,event_id,persona_id,score,metadata_json) VALUES('%s',%d,%d,%d,'%s')
ON CONFLICT(board_key,event_id,persona_id) DO UPDATE SET metadata_json=IIF(excluded.score<score,excluded.metadata_json,metadata_json), updated_at=IIF(excluded.score<score,datetime('now'),updated_at), score=MIN(score,excluded.score);`,
		esc(board), eventID, personaID, esc(board), eventID, personaID, timeMs, esc(metadataJSON)))
	if e != nil {
		return false, e
	}
	if len(rows) == 0 {
		return true, nil // first time on this board
	}
	prev, _ := strconv.ParseUint(strings.TrimSpace(rows[0]), 10, 64)
	return timeMs < prev, nil
}

// BoardEntry is one persona's best on one leaderboard event.
type BoardEntry struct {
	Board        string
	EventID      uint64
	PersonaID    uint64
	BlazeID      uint64
	Score        uint64
	PersonaName  string
	MetadataJSON string
	UpdatedUnix  int64
}

// LoadBoardEntries returns every best for the given boards/events, fastest
// first, in one query (the client asks for ~75 events at once).
func (d *DB) LoadBoardEntries(boards []string, events []uint64) ([]BoardEntry, error) {
	if len(events) == 0 {
		return nil, nil
	}
	boardFilter := "" // no boards = any board (Stats leaderboards key on the event only)
	if len(boards) > 0 {
		bq := make([]string, len(boards))
		for i, b := range boards {
			bq[i] = "'" + esc(b) + "'"
		}
		boardFilter = fmt.Sprintf("l.board_key IN (%s) AND ", strings.Join(bq, ","))
	}
	eq := make([]string, len(events))
	for i, e := range events {
		eq[i] = strconv.FormatUint(e, 10)
	}
	rows, err := d.query(fmt.Sprintf(`SELECT l.board_key, l.event_id, l.persona_id, p.blaze_id, l.score, REPLACE(REPLACE(p.name,char(9),' '),char(10),' '), COALESCE(l.metadata_json,'{}')
FROM leaderboards l JOIN personas p ON p.id=l.persona_id
WHERE %sl.event_id IN (%s)
ORDER BY l.board_key, l.event_id, l.score, l.updated_at;`, boardFilter, strings.Join(eq, ",")))
	if err != nil {
		return nil, err
	}
	out := make([]BoardEntry, 0, len(rows))
	for _, r := range rows {
		c := strings.SplitN(r, "\t", 7)
		if len(c) < 7 {
			continue
		}
		e := BoardEntry{Board: c[0], PersonaName: c[5], MetadataJSON: c[6]}
		e.EventID, _ = strconv.ParseUint(c[1], 10, 64)
		e.PersonaID, _ = strconv.ParseUint(c[2], 10, 64)
		e.BlazeID, _ = strconv.ParseUint(c[3], 10, 64)
		e.Score, _ = strconv.ParseUint(c[4], 10, 64)
		out = append(out, e)
	}
	return out, nil
}

// LoadBoard returns every best on one board, fastest first per event.
func (d *DB) LoadBoard(board string) ([]BoardEntry, error) {
	rows, err := d.query(fmt.Sprintf(`SELECT l.board_key, l.event_id, l.persona_id, p.blaze_id, l.score, UNIX_TIMESTAMP(l.updated_at), REPLACE(REPLACE(p.name,char(9),' '),char(10),' ')
FROM leaderboards l JOIN personas p ON p.id=l.persona_id
WHERE l.board_key='%s'
ORDER BY l.event_id, l.score, l.updated_at;`, esc(board)))
	if err != nil {
		return nil, err
	}
	out := make([]BoardEntry, 0, len(rows))
	for _, r := range rows {
		c := strings.SplitN(r, "\t", 7)
		if len(c) < 7 {
			continue
		}
		e := BoardEntry{Board: c[0], PersonaName: c[6], MetadataJSON: "{}"}
		e.EventID, _ = strconv.ParseUint(c[1], 10, 64)
		e.PersonaID, _ = strconv.ParseUint(c[2], 10, 64)
		e.BlazeID, _ = strconv.ParseUint(c[3], 10, 64)
		e.Score, _ = strconv.ParseUint(c[4], 10, 64)
		if f, e2 := strconv.ParseFloat(c[5], 64); e2 == nil {
			e.UpdatedUnix = int64(f)
		}
		out = append(out, e)
	}
	return out, nil
}

// PersonaRef is a minimal persona row for friend lists.
type PersonaRef struct {
	ID, BlazeID uint64
	Name        string
	AccountID   uint64 // UserIdentification.AID
}

// ListPersonas returns personas other than excludeID, most recently active first.
func (d *DB) ListPersonas(excludeID uint64, limit int) ([]PersonaRef, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.query(fmt.Sprintf(`SELECT id, blaze_id, account_id, REPLACE(REPLACE(name,char(9),' '),char(10),' ') FROM personas
WHERE id<>%d ORDER BY COALESCE(last_login_at, created_at) DESC LIMIT %d;`, excludeID, limit))
	if err != nil {
		return nil, err
	}
	return parsePersonaRefs(rows), nil
}

// ListFriends returns the accepted friends of a persona (table friends).
func (d *DB) ListFriends(personaID uint64) ([]PersonaRef, error) {
	rows, err := d.query(fmt.Sprintf(`SELECT p.id, p.blaze_id, p.account_id, REPLACE(REPLACE(p.name,char(9),' '),char(10),' ') FROM friends f
JOIN personas p ON p.id=f.friend_persona_id
WHERE f.persona_id=%d AND f.status='accepted' ORDER BY COALESCE(p.last_login_at, p.created_at) DESC LIMIT 500;`, personaID))
	if err != nil {
		return nil, err
	}
	return parsePersonaRefs(rows), nil
}

// parsePersonaRefs: rows of "id, blaze_id, account_id, name" (tab separated).
func parsePersonaRefs(rows []string) []PersonaRef {
	var out []PersonaRef
	for _, r := range rows {
		c := strings.SplitN(r, "\t", 4)
		if len(c) < 4 {
			continue
		}
		var p PersonaRef
		p.ID, _ = strconv.ParseUint(c[0], 10, 64)
		p.BlazeID, _ = strconv.ParseUint(c[1], 10, 64)
		p.AccountID, _ = strconv.ParseUint(c[2], 10, 64)
		p.Name = c[3]
		out = append(out, p)
	}
	return out
}

// LinkAllFriends makes every player a mutual friend of every other player
// (FRIENDS_ALL); race-server personas (NFS-DS-nn) are left out. Idempotent.
func (d *DB) LinkAllFriends() error {
	_, err := d.query(`INSERT IGNORE INTO friends(persona_id,friend_persona_id,status,created_at)
SELECT a.id, b.id, 'accepted', NOW() FROM personas a JOIN personas b ON a.id<>b.id
WHERE a.name NOT LIKE 'NFS-DS-%' AND b.name NOT LIKE 'NFS-DS-%';`)
	return err
}

// PersonaByLauncherSessionKey resolves launcher tickets only; other session
// keys never count as launcher authority.
func (d *DB) PersonaByLauncherSessionKey(key string) (Persona, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Persona{}, false, nil
	}
	rows, err := d.query(fmt.Sprintf(`SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,'') FROM sessions s
JOIN personas p ON p.id=s.persona_id JOIN accounts a ON a.id=p.account_id
WHERE s.session_key='%s' AND s.session_kind='launcher-v3' AND s.expires_at>NOW() LIMIT 1;`, esc(key)))
	if err != nil || len(rows) == 0 {
		return Persona{}, false, err
	}
	per, err := parsePersonaRow(rows[0])
	if err != nil {
		return Persona{}, false, err
	}
	per.SessionKey = key
	return per, true, nil
}

// PersonaBySessionKey resolves a live session key (UserSessions.resumeSession).
func (d *DB) PersonaBySessionKey(key string) (Persona, bool, error) {
	if strings.TrimSpace(key) == "" {
		return Persona{}, false, nil
	}
	rows, err := d.query(fmt.Sprintf(`SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,'') FROM sessions s
JOIN personas p ON p.id=s.persona_id JOIN accounts a ON a.id=p.account_id
WHERE s.session_key='%s' AND s.expires_at>NOW() LIMIT 1;`, esc(key)))
	if err != nil || len(rows) == 0 {
		return Persona{}, false, err
	}
	per, err := parsePersonaRow(rows[0])
	if err != nil {
		return Persona{}, false, err
	}
	per.SessionKey = key
	return per, true, nil
}

// LockerContent is one Locker item (e.g. a photo-mode snapshot).
type LockerContent struct {
	ID             uint64
	PersonaID      uint64
	BlazeID        uint64
	OwnerName      string
	Category       string
	Name           string
	Description    string
	AttributesJSON string
	SubNames       string // comma separated
	Permission     uint64
	Status         uint64
	Size           uint64
	UploadToken    string
	Created        int64
	Updated        int64
	UseCount       uint64 // incrementUseCount (views)
	RatingCount    uint64 // players who rated it ("likes")
	RatingSum      int64
}

const lockerCols = `c.id,c.persona_id,p.blaze_id,REPLACE(REPLACE(p.name,char(9),' '),char(10),' '),c.category,REPLACE(REPLACE(c.name,char(9),' '),char(10),' '),REPLACE(REPLACE(c.description,char(9),' '),char(10),' '),c.attributes_json,c.sub_names,c.permission,c.status,c.size_bytes,c.upload_token,UNIX_TIMESTAMP(c.created_at),UNIX_TIMESTAMP(c.updated_at),c.use_count,(SELECT COUNT(*) FROM locker_ratings r WHERE r.content_id=c.id),(SELECT COALESCE(SUM(r.rating),0) FROM locker_ratings r WHERE r.content_id=c.id)`

func parseLocker(row string) (LockerContent, bool) {
	c := strings.Split(row, "\t")
	if len(c) < 15 {
		return LockerContent{}, false
	}
	u := func(s string) uint64 { v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64); return v }
	i := func(s string) int64 { v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64); return v }
	lc := LockerContent{ID: u(c[0]), PersonaID: u(c[1]), BlazeID: u(c[2]), OwnerName: c[3], Category: c[4], Name: c[5],
		Description: c[6], AttributesJSON: c[7], SubNames: c[8], Permission: u(c[9]), Status: u(c[10]), Size: u(c[11]),
		UploadToken: c[12], Created: i(c[13]), Updated: i(c[14])}
	if len(c) >= 18 {
		lc.UseCount, lc.RatingCount, lc.RatingSum = u(c[15]), u(c[16]), i(c[17])
	}
	return lc, true
}

// RateLockerContent stores a player's rating of an item (Locker.updateRating;
// the game's "like" sends 1). Rating again replaces the previous value.
func (d *DB) RateLockerContent(contentID, personaID uint64, rating int64) error {
	_, err := d.query(fmt.Sprintf("INSERT INTO locker_ratings(content_id,persona_id,rating) VALUES(%d,%d,%d) ON CONFLICT(content_id,persona_id) DO UPDATE SET rating=excluded.rating,rated_at=datetime('now');",
		contentID, personaID, rating))
	return err
}

// IncrementLockerUseCount counts one view of an item (Locker.incrementUseCount).
func (d *DB) IncrementLockerUseCount(contentID uint64) error {
	_, err := d.query(fmt.Sprintf("UPDATE locker_contents SET use_count=use_count+1, updated_at=updated_at WHERE id=%d;", contentID))
	return err
}

// LockerMyRatings returns a player's ratings of the given items.
func (d *DB) LockerMyRatings(personaID uint64, ids []uint64) (map[uint64]int64, error) {
	out := map[uint64]int64{}
	if personaID == 0 || len(ids) == 0 {
		return out, nil
	}
	q := make([]string, len(ids))
	for i, v := range ids {
		q[i] = strconv.FormatUint(v, 10)
	}
	rows, err := d.query(fmt.Sprintf("SELECT content_id, rating FROM locker_ratings WHERE persona_id=%d AND content_id IN (%s);", personaID, strings.Join(q, ",")))
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		c := strings.Split(r, "\t")
		if len(c) < 2 {
			continue
		}
		id, _ := strconv.ParseUint(strings.TrimSpace(c[0]), 10, 64)
		v, _ := strconv.ParseInt(strings.TrimSpace(c[1]), 10, 64)
		out[id] = v
	}
	return out, nil
}

// CreateLockerContent inserts a PENDING item and returns it.
func (d *DB) CreateLockerContent(personaID uint64, category, name, desc, attrsJSON, subNames string, perm uint64) (LockerContent, error) {
	tok := token()[:32]
	rows, err := d.query(fmt.Sprintf(`INSERT INTO locker_contents(persona_id,category,name,description,attributes_json,sub_names,permission,status,upload_token)
VALUES(%d,'%s','%s','%s','%s','%s',%d,1,'%s'); SELECT LAST_INSERT_ID();`, personaID, esc(category), esc(name), esc(desc), esc(attrsJSON), esc(subNames), perm, tok))
	if err != nil {
		return LockerContent{}, err
	}
	if len(rows) == 0 {
		return LockerContent{}, fmt.Errorf("no id")
	}
	id, _ := strconv.ParseUint(strings.TrimSpace(rows[len(rows)-1]), 10, 64)
	c, ok, err := d.GetLockerContent(id)
	if err != nil || !ok {
		return LockerContent{}, fmt.Errorf("created content %d not found: %v", id, err)
	}
	return c, nil
}

func (d *DB) GetLockerContent(id uint64) (LockerContent, bool, error) {
	rows, err := d.query(fmt.Sprintf("SELECT %s FROM locker_contents c JOIN personas p ON p.id=c.persona_id WHERE c.id=%d LIMIT 1;", lockerCols, id))
	if err != nil || len(rows) == 0 {
		return LockerContent{}, false, err
	}
	c, ok := parseLocker(rows[0])
	return c, ok, nil
}

// SetLockerStatus updates status (and size when > 0).
func (d *DB) SetLockerStatus(id, status, size uint64) error {
	sz := ""
	if size > 0 {
		sz = fmt.Sprintf(", size_bytes=%d", size)
	}
	_, err := d.query(fmt.Sprintf("UPDATE locker_contents SET status=%d%s WHERE id=%d;", status, sz, id))
	return err
}

func (d *DB) UpdateLockerContent(id, ownerID uint64, name, desc, attrsJSON string) error {
	_, err := d.query(fmt.Sprintf("UPDATE locker_contents SET name='%s', description='%s', attributes_json='%s', updated_at=datetime('now') WHERE id=%d AND persona_id=%d;", esc(name), esc(desc), esc(attrsJSON), id, ownerID))
	return err
}

func (d *DB) DeleteLockerContents(ownerID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	q := make([]string, len(ids))
	for i, v := range ids {
		q[i] = strconv.FormatUint(v, 10)
	}
	_, err := d.query(fmt.Sprintf("DELETE FROM locker_contents WHERE persona_id=%d AND id IN (%s);", ownerID, strings.Join(q, ",")))
	return err
}

// ListLockerContents lists ACTIVE items of a category; ownerID 0 = everyone
// (PUBLIC/FRIENDS_ONLY only), otherwise that owner's items (all permissions when
// includePrivate). Returns the page and the total count.
func (d *DB) ListLockerContents(category string, ownerID uint64, includePrivate bool, offset, limit int) ([]LockerContent, uint64, error) {
	where := fmt.Sprintf("c.category='%s' AND c.status=0", esc(category))
	if ownerID != 0 {
		where += fmt.Sprintf(" AND c.persona_id=%d", ownerID)
	}
	if !includePrivate {
		where += " AND c.permission<>1"
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := d.query(fmt.Sprintf("SELECT COUNT(*) FROM locker_contents c WHERE %s; SELECT %s FROM locker_contents c JOIN personas p ON p.id=c.persona_id WHERE %s ORDER BY c.id DESC LIMIT %d OFFSET %d;",
		where, lockerCols, where, limit, offset))
	if err != nil || len(rows) == 0 {
		return nil, 0, err
	}
	total, _ := strconv.ParseUint(strings.TrimSpace(rows[0]), 10, 64)
	var out []LockerContent
	for _, r := range rows[1:] {
		if c, ok := parseLocker(r); ok {
			out = append(out, c)
		}
	}
	return out, total, nil
}

// OwnedCount is how many leaderboards (board+event) a persona leads.
type OwnedCount struct {
	PersonaID, BlazeID, Count uint64
}

// SpeedwallOwnedCounts returns, for every persona that leads at least one
// event, the number of events where it holds the fastest time.
func (d *DB) SpeedwallOwnedCounts() ([]OwnedCount, error) {
	rows, err := d.query(`SELECT p.id, p.blaze_id, COUNT(*) FROM leaderboards l JOIN personas p ON p.id=l.persona_id
WHERE l.score = (SELECT MIN(l2.score) FROM leaderboards l2 WHERE l2.board_key=l.board_key AND l2.event_id=l.event_id)
GROUP BY p.id, p.blaze_id;`)
	if err != nil {
		return nil, err
	}
	var out []OwnedCount
	for _, r := range rows {
		c := strings.Split(r, "\t")
		if len(c) < 3 {
			continue
		}
		var o OwnedCount
		o.PersonaID, _ = strconv.ParseUint(c[0], 10, 64)
		o.BlazeID, _ = strconv.ParseUint(c[1], 10, 64)
		o.Count, _ = strconv.ParseUint(c[2], 10, 64)
		out = append(out, o)
	}
	return out, nil
}
