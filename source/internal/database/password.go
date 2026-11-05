package database

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Password login for players who do not use the launcher: the server owner
// creates the player (adduser / passwd) and hands out the generated password;
// the Origin stand-in turns name + password into a one-time game code.

const pwIterations = 120000

// pbkdf2SHA256 is PBKDF2-HMAC-SHA256 (RFC 8018) for a 32-byte key.
func pbkdf2SHA256(password, salt []byte, iter int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write(binary.BigEndian.AppendUint32(nil, 1))
	u := mac.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < iter; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}

// GeneratePassword makes a password that is easy to type and read out:
// four groups of five characters without look-alikes (no 0/O, 1/I/l).
func GeneratePassword() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%5 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return sb.String(), nil
}

func normalizePassword(p string) string { return strings.ToLower(strings.TrimSpace(p)) }

// SetPassword gives the player a new password, creating the player when the
// name is free. It returns whether the player was created.
func (d *DB) SetPassword(name, password string) (created bool, err error) {
	name = strings.TrimSpace(name)
	if err := CheckPersonaName(name); err != nil {
		return false, err
	}
	password = normalizePassword(password)
	if len(password) < 8 {
		return false, fmt.Errorf("password too short")
	}
	_, exists, err := d.FindPersona(name)
	if err != nil {
		return false, err
	}
	per, err := d.GetOrCreateIdentity("", name)
	if err != nil {
		return false, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return false, err
	}
	h := pbkdf2SHA256([]byte(password), salt, pwIterations)
	_, err = d.query(fmt.Sprintf(`INSERT INTO password_logins(persona_id,pw_salt,pw_hash,iterations) VALUES(%d,'%s','%s',%d)
ON CONFLICT(persona_id) DO UPDATE SET pw_salt=excluded.pw_salt, pw_hash=excluded.pw_hash, iterations=excluded.iterations, created_at=datetime('now');
DELETE FROM sessions WHERE persona_id=%d AND credential_key IS NULL;`,
		per.ID, hex.EncodeToString(salt), hex.EncodeToString(h), pwIterations, per.ID))
	return !exists, err
}

// CheckPassword verifies name + password. A wrong name costs the same time as
// a wrong password.
func (d *DB) CheckPassword(name, password string) (Persona, bool, error) {
	name = strings.TrimSpace(name)
	password = normalizePassword(password)
	rows, err := d.query(fmt.Sprintf(`SELECT p.id,p.account_id,p.blaze_id,p.name,COALESCE(a.email,''),w.pw_salt,w.pw_hash,w.iterations
FROM personas p JOIN accounts a ON a.id=p.account_id JOIN password_logins w ON w.persona_id=p.id WHERE p.name='%s' LIMIT 1;`, esc(name)))
	if err != nil {
		return Persona{}, false, err
	}
	salt, want, iter := make([]byte, 16), make([]byte, 32), pwIterations
	var per Persona
	if len(rows) > 0 {
		f := strings.Split(rows[0], "\t")
		if len(f) == 8 {
			if per, err = parsePersonaRow(strings.Join(f[:5], "\t")); err == nil {
				if s, e := hex.DecodeString(f[5]); e == nil {
					salt = s
				}
				if h, e := hex.DecodeString(f[6]); e == nil && len(h) == 32 {
					want = h
				}
				if n, e := strconv.Atoi(f[7]); e == nil && n > 0 {
					iter = n
				}
			} else {
				per = Persona{}
			}
		}
	}
	got := pbkdf2SHA256([]byte(password), salt, iter)
	ok := per.ID != 0 && subtle.ConstantTimeCompare(got, want) == 1
	if !ok {
		return Persona{}, false, nil
	}
	return per, true, nil
}

// IssuePasswordSession makes a launcher-style ticket for a persona that
// logged in with a password. Other tickets of the persona stay valid.
func (d *DB) IssuePasswordSession(per Persona, ttl time.Duration) (Persona, error) {
	seconds := int64(ttl / time.Second)
	if seconds < 60 {
		seconds = 60
	}
	sk, err := launcherToken()
	if err != nil {
		return Persona{}, err
	}
	_, err = d.query(fmt.Sprintf(`UPDATE personas SET last_login_at=NOW() WHERE id=%d;
DELETE FROM sessions WHERE expires_at<=NOW();
INSERT INTO sessions(persona_id,session_key,session_kind,credential_key,created_at,expires_at)
VALUES(%d,'%s','launcher-v3',NULL,NOW(),DATE_ADD(NOW(),INTERVAL %d SECOND));`, per.ID, per.ID, esc(sk), seconds))
	if err != nil {
		return Persona{}, err
	}
	per.SessionKey = sk
	return per, nil
}
