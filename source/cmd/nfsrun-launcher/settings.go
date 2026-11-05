package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	iniName = "nfsrun-launcher.ini"
	keyName = "nfsrun-launcher.key"
)

// defaultINI: the settings a player may want to change. Server settings are
// built into the launcher; the rest is documented in README.txt.
const defaultINI = `# NFS The Run launcher. Keep this file next to nfsrun-launcher.exe.
# Changes take effect the next time the launcher starts. All options: README.txt

# Your name on the server, 1-16 characters. Empty = <COMPUTERNAME>.0
PLAYER_NAME=

# One-time invite code from the server owner (first login only; cleared after use).
INVITE_CODE=

# Language of the game and the launcher:
#   en_US fr_FR de_DE es_ES es_MX it_IT ja_JP ru_RU pl_PL nl_NL cs_CZ zh_TW
#   pt_BR pt_PT ko_KR = launcher only (the game has no such text)
#   auto = the Windows language
LANGUAGE=auto

# Game executable (relative to this folder or a full path) and extra arguments.
GAME_EXE=Need For Speed The Run.exe
GAME_ARGS=

# Local Origin port the game connects to. Change it only when another
# program already uses it.
LSX_LISTEN=127.0.0.1:3216

# 1 = keep playing a playlist alone, 0 = leave the session when alone.
SOLO_ONLINE=1

# Invites from friends: ask (Yes/No box), auto (accept at once), off (ignore).
INVITES=ask

# 1 = start the game as soon as the launcher is ready.
AUTO_START=0

# 1 = close the launcher when the game exits. Closing the launcher always
# closes the game.
CLOSE_WITH_GAME=0
`

const keyFileHeader = `# NFS The Run launcher: your private login key and what you allowed the
# launcher to do. Back this file up and never share it.
`

type config struct {
	GameExe, GameArgs, Persona, InviteCode, LSXListen string
	Language                                          string // as written (auto, en_US, ...)
	Locale                                            string // the game's Locale, resolved at start
	ServerHTTP, ServerCertPin, Domain                 string
	SoloOnline, ExtraPlaylists, NoOriginStart         bool
	AutoStart, CloseWithGame                          bool
	Invites                                           string // ask, auto, off

	ClientKey string   // from nfsrun-launcher.key
	Allowed   []string // consent given on the first start
}

func defaultConfig() config {
	p := builtIn()
	return config{GameExe: "Need For Speed The Run.exe", LSXListen: "127.0.0.1:3216", Language: "auto",
		ServerHTTP: p.ServerHTTP, ServerCertPin: p.Pin, Domain: p.Domain,
		SoloOnline: true, NoOriginStart: true, Invites: "ask"}
}

func isOn(v string) bool {
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "on") || strings.EqualFold(v, "yes")
}

func readKV(path string) (map[string]string, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	kv := map[string]string{}
	var order []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToUpper(strings.TrimSpace(k))
		if _, seen := kv[k]; !seen {
			order = append(order, k)
		}
		kv[k] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return kv, order, sc.Err()
}

// loadConfig reads nfsrun-launcher.ini (created on first start) and
// nfsrun-launcher.key. An ini of an older launcher is rewritten in the
// current format; its CLIENT_KEY moves to the key file.
func loadConfig(dir string) (config, error) {
	c := defaultConfig()
	path := filepath.Join(dir, iniName)
	kv, _, err := readKV(path)
	if os.IsNotExist(err) {
		_ = os.WriteFile(path, crlf(defaultINI), 0644)
		kv, err = map[string]string{}, nil
	}
	if err != nil {
		return c, err
	}
	key, allowed, kerr := readKeyFile(dir)
	if kerr != nil && !os.IsNotExist(kerr) {
		return c, kerr
	}
	c.ClientKey, c.Allowed = key, allowed
	applyINI(&c, kv)
	if old := kv["CLIENT_KEY"]; old != "" || hasOldKeys(kv) {
		if c.ClientKey == "" {
			c.ClientKey = strings.ToLower(old)
		}
		if err := migrateINI(dir, c, kv); err != nil {
			return c, fmt.Errorf("updating %s: %w", iniName, err)
		}
	}
	return c, nil
}

// old names: PERSONA, CLAIM_CODE, LOCALE, REGISTRY.
func hasOldKeys(kv map[string]string) bool {
	for _, k := range []string{"PERSONA", "CLAIM_CODE", "LOCALE", "REGISTRY"} {
		if _, ok := kv[k]; ok {
			return true
		}
	}
	return false
}

func applyINI(c *config, kv map[string]string) {
	b := builtIn()
	pick := func(names ...string) (string, bool) {
		for _, n := range names {
			if v, ok := kv[n]; ok {
				return v, true
			}
		}
		return "", false
	}
	if v, ok := pick("GAME_EXE"); ok && v != "" {
		c.GameExe = v
	}
	if v, ok := pick("GAME_ARGS"); ok {
		c.GameArgs = v
	}
	if v, ok := pick("PLAYER_NAME", "PERSONA"); ok {
		c.Persona = v
	}
	if v, ok := pick("INVITE_CODE", "CLAIM_CODE"); ok {
		c.InviteCode = strings.ToUpper(strings.TrimSpace(v))
	}
	if v, ok := pick("LANGUAGE", "LOCALE"); ok && v != "" {
		c.Language = v
	}
	if v, ok := pick("LSX_LISTEN"); ok && v != "" {
		c.LSXListen = v
	}
	if v, ok := pick("SOLO_ONLINE"); ok {
		c.SoloOnline = v == "" || isOn(v)
	}
	if v, ok := pick("NO_ORIGIN_START"); ok {
		c.NoOriginStart = v == "" || isOn(v)
	}
	if v, ok := pick("EXTRA_PLAYLISTS"); ok {
		c.ExtraPlaylists = isOn(v)
	}
	if v, ok := pick("AUTO_START"); ok {
		c.AutoStart = isOn(v)
	}
	if v, ok := pick("CLOSE_WITH_GAME"); ok {
		c.CloseWithGame = isOn(v)
	}
	if v, ok := pick("INVITES"); ok {
		switch strings.ToLower(v) {
		case "auto", "1", "on", "yes":
			c.Invites = "auto"
		case "off", "0", "no", "false":
			c.Invites = "off"
		default:
			c.Invites = "ask"
		}
	}
	// another server than the built-in one: its own pin (or none)
	if v, ok := pick("SERVER_HTTP"); ok && v != "" {
		v = strings.TrimRight(v, "/")
		if strings.EqualFold(v, "off") {
			c.ServerHTTP = ""
		} else {
			c.ServerHTTP = v
		}
		if !strings.EqualFold(v, b.ServerHTTP) {
			c.ServerCertPin, c.Domain = "", ""
		}
	}
	if v, ok := pick("SERVER_CERT_PIN"); ok && v != "" {
		if strings.EqualFold(v, "off") || strings.EqualFold(v, "none") {
			c.ServerCertPin = ""
		} else {
			c.ServerCertPin = v
		}
	}
	if v, ok := pick("BASE_DOMAIN"); ok && v != "" {
		c.Domain = strings.ToLower(v)
	}
}

// migrateINI rewrites an older ini in the current format (keeping its values
// and any advanced settings) after saving its CLIENT_KEY to the key file.
func migrateINI(dir string, c config, kv map[string]string) error {
	if c.ClientKey != "" {
		if err := writeKeyFile(dir, c.ClientKey, c.Allowed); err != nil {
			return err
		}
	}
	path := filepath.Join(dir, iniName)
	if b, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".old", b, 0600)
	}
	vals := map[string]string{
		"PLAYER_NAME": c.Persona, "INVITE_CODE": c.InviteCode, "LANGUAGE": c.Language,
		"GAME_EXE": c.GameExe, "GAME_ARGS": c.GameArgs, "LSX_LISTEN": c.LSXListen,
		"SOLO_ONLINE": onOff(c.SoloOnline), "INVITES": c.Invites,
		"AUTO_START": onOff(c.AutoStart), "CLOSE_WITH_GAME": onOff(c.CloseWithGame),
	}
	var b strings.Builder
	for _, line := range strings.Split(defaultINI, "\n") {
		if k, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			if v, has := vals[k]; has {
				line = k + "=" + v
			}
		}
		b.WriteString(line + "\n")
	}
	// advanced settings the player had set keep working
	var adv []string
	for _, k := range []string{"SERVER_HTTP", "SERVER_CERT_PIN", "BASE_DOMAIN", "NO_ORIGIN_START", "EXTRA_PLAYLISTS"} {
		v, ok := kv[k]
		if !ok || v == "" || (k == "NO_ORIGIN_START" && isOn(v)) || (k == "EXTRA_PLAYLISTS" && !isOn(v)) {
			continue
		}
		adv = append(adv, k+"="+v)
	}
	if len(adv) > 0 {
		b.WriteString("\n# Advanced (README.txt)\n" + strings.Join(adv, "\n") + "\n")
	}
	return os.WriteFile(path, crlf(b.String()), 0644)
}

func onOff(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func crlf(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }

func readKeyFile(dir string) (key string, allowed []string, err error) {
	kv, _, err := readKV(filepath.Join(dir, keyName))
	if err != nil {
		return "", nil, err
	}
	for _, a := range strings.Split(kv["ALLOWED"], ",") {
		if a = strings.TrimSpace(a); a != "" {
			allowed = append(allowed, a)
		}
	}
	return strings.ToLower(kv["CLIENT_KEY"]), allowed, nil
}

func writeKeyFile(dir, key string, allowed []string) error {
	a := append([]string(nil), allowed...)
	sort.Strings(a)
	body := keyFileHeader + "CLIENT_KEY=" + key + "\nALLOWED=" + strings.Join(a, ",") + "\n"
	return os.WriteFile(filepath.Join(dir, keyName), crlf(body), 0600)
}

// ensureClientKey checks the key or creates one on the first start.
func ensureClientKey(dir string, c *config) error {
	if key := c.ClientKey; key != "" {
		if b, err := hex.DecodeString(key); err != nil || len(b) != 32 {
			return fmt.Errorf("CLIENT_KEY in %s is invalid (expected 64 hexadecimal characters); restore your backup of that file", keyName)
		}
		return nil
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	c.ClientKey = hex.EncodeToString(raw[:])
	return writeKeyFile(dir, c.ClientKey, c.Allowed)
}

// clearInviteCode empties INVITE_CODE after a successful first login.
func clearInviteCode(dir string, c *config) error {
	path := filepath.Join(dir, iniName)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		k, _, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "\uFEFF")), "=")
		if ok && (strings.EqualFold(strings.TrimSpace(k), "INVITE_CODE") || strings.EqualFold(strings.TrimSpace(k), "CLAIM_CODE")) {
			lines[i] = strings.TrimSpace(k) + "="
		}
	}
	if err := os.WriteFile(path, crlf(strings.Join(lines, "\n")), 0644); err != nil {
		return err
	}
	c.InviteCode = ""
	return nil
}

// consentItem: something the launcher changes outside its own files.
type consentItem struct {
	ID, Text string
}

// ensureConsent asks once (per item) before the launcher changes anything
// on the PC. false = the player said no.
func ensureConsent(ui launcherUI, dir string, c *config, items []consentItem) bool {
	have := map[string]bool{}
	for _, a := range c.Allowed {
		have[a] = true
	}
	var missing []consentItem
	for _, it := range items {
		if !have[it.ID] {
			missing = append(missing, it)
		}
	}
	if len(missing) == 0 {
		return true
	}
	var list strings.Builder
	for _, it := range items {
		list.WriteString(it.Text + "\n")
	}
	if !ui.Confirm(T("consent_title"), T("consent_text", list.String())) {
		return false
	}
	for _, it := range missing {
		c.Allowed = append(c.Allowed, it.ID)
	}
	_ = writeKeyFile(dir, c.ClientKey, c.Allowed)
	return true
}
