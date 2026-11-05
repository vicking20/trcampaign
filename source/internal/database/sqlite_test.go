package database

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(); err != nil { // idempotent
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestIdentitySessionsAndNames(t *testing.T) {
	d := testDB(t)
	a, err := d.GetOrCreateIdentity("a@ea.com", `Rac'er\t NOW()`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.GetOrCreateIdentity("a@ea.com", a.Name)
	if err != nil || b.ID != a.ID {
		t.Fatalf("same email must be the same persona: %v %v %v", b, a, err)
	}
	got, ok, err := d.FindPersona(strings.ToLower(a.Name)) // case-insensitive, like MariaDB
	if err != nil || !ok || got.Name != a.Name {
		t.Fatalf("find: %v %v %v", got, ok, err)
	}
	key, err := d.IssueGameSession(a, time.Minute)
	if err != nil || !d.ValidateSession(key) {
		t.Fatalf("session %q: %v", key, err)
	}
	if p, ok, _ := d.PersonaBySessionKey(key); !ok || p.ID != a.ID {
		t.Fatal("persona by session")
	}
	if _, err := d.IssueGameSession(a, -time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestLauncherClaimAndKey(t *testing.T) {
	d := testDB(t)
	code, _, err := d.CreateLauncherClaim("Racer", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := d.LauncherNameReserved("racer"); !r {
		t.Fatal("name should be reserved by its pending code")
	}
	pub := strings.Repeat("ab", 32)
	per, exp, err := d.AuthenticateLauncherPublicKey("", "Racer", pub, code, "invite", time.Hour)
	if err != nil || per.Name != "Racer" || exp <= time.Now().Unix() {
		t.Fatalf("auth: %v %v %v", per, exp, err)
	}
	if known, _ := d.LauncherPublicKeyKnown(pub); !known {
		t.Fatal("key should be known")
	}
	if _, _, err := d.AuthenticateLauncherPublicKey("", "Racer", pub, "", "invite", time.Hour); err != nil {
		t.Fatalf("second login with the same key: %v", err)
	}
}

func TestLeaderboardReportsAndLocker(t *testing.T) {
	d := testDB(t)
	a, _ := d.GetOrCreateIdentity("l@ea.com", "Lap")
	if better, err := d.UpsertPersonalBest(a.ID, "run", 7, 5000, `{"a":1}`); err != nil || !better {
		t.Fatalf("first: %v %v", better, err)
	}
	if better, _ := d.UpsertPersonalBest(a.ID, "run", 7, 6000, `{}`); better {
		t.Fatal("slower must not be better")
	}
	if better, _ := d.UpsertPersonalBest(a.ID, "run", 7, 4000, `{"b":2}`); !better {
		t.Fatal("faster must be better")
	}
	es, err := d.LoadBoard("run")
	if err != nil || len(es) != 1 || es[0].Score != 4000 {
		t.Fatalf("board: %v %v", es, err)
	}
	if n, err := d.SaveGameReport(a.ID, "run_single", "h1", "00", "{}"); err != nil || !n {
		t.Fatalf("report: %v %v", n, err)
	}
	if n, _ := d.SaveGameReport(a.ID, "run_single", "h1", "00", "{}"); n {
		t.Fatal("duplicate report must not be new")
	}
	c, err := d.CreateLockerContent(a.ID, "SNAPSHOTS", "n", "", "{}", "client", 0)
	if err != nil || c.ID == 0 {
		t.Fatalf("locker: %v %v", c, err)
	}
	if err := d.RateLockerContent(c.ID, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.RateLockerContent(c.ID, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.LinkAllFriends(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ListPersonaInfo(10); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RenamePersona("Lap", "Lap2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.DeletePersona("Lap2"); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordLogin(t *testing.T) {
	d := testDB(t)
	pw, err := GeneratePassword()
	if err != nil || len(pw) != 23 {
		t.Fatalf("generated %q: %v", pw, err)
	}
	created, err := d.SetPassword("Racer", pw)
	if err != nil || !created {
		t.Fatalf("set: %v %v", created, err)
	}
	if per, ok, err := d.CheckPassword("racer", strings.ToUpper(pw)); err != nil || !ok || per.Name != "Racer" {
		t.Fatalf("right password: %v %v %v", per, ok, err)
	}
	for _, c := range [][2]string{{"Racer", "wrong-wrong"}, {"Nobody", pw}, {"Racer", ""}, {"Racer' OR '1'='1", pw}} {
		if _, ok, _ := d.CheckPassword(c[0], c[1]); ok {
			t.Fatalf("%q must not log in", c)
		}
	}
	per, _, _ := d.CheckPassword("Racer", pw)
	ps, err := d.IssuePasswordSession(per, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := d.PersonaByLauncherSessionKey(ps.SessionKey); !ok || got.ID != per.ID {
		t.Fatal("password session must work as a launcher session")
	}
	pw2, _ := GeneratePassword()
	if created, err := d.SetPassword("Racer", pw2); err != nil || created {
		t.Fatalf("reset: %v %v", created, err)
	}
	if _, ok, _ := d.CheckPassword("Racer", pw); ok {
		t.Fatal("old password must stop working")
	}
	if _, ok, _ := d.PersonaByLauncherSessionKey(ps.SessionKey); ok {
		t.Fatal("a reset must revoke old password sessions")
	}
	if _, ok, _ := d.CheckPassword("Racer", pw2); !ok {
		t.Fatal("new password must work")
	}
}
