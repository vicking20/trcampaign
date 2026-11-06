package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	RedirectorListen        string
	BlazeListen             string
	RedirectorFrontListen   string
	BlazeFrontListen        string
	RedirectorGatewayTarget string
	HTTPListen              string
	// Admin web page (ADMIN_WEB_LISTEN, e.g. ":8090"; empty = off) and its login.
	AdminWebListen   string
	AdminWebUser     string
	AdminWebPassword string
	// BansFile (BANS_FILE): banned player names, one per line.
	BansFile string
	// RaceSettingsFile (RACE_SETTINGS_FILE): settings the admin page edits (traffic ...), written into the race servers' plugin ini.
	RaceSettingsFile string
	// Launcher API over HTTPS. LauncherHTTPSListen "" = off (the API then
	// moves to HTTP_LISTEN). Without LAUNCHER_TLS_CERT on disk a self-signed
	// certificate is created there.
	LauncherHTTPSListen string
	LauncherTLSCert     string
	LauncherTLSKey      string
	// LauncherURL: the address players use for the launcher API (player
	// package). "" = https://<PUBLIC_HOST>:<LAUNCHER_HTTPS_LISTEN port>.
	LauncherURL string
	// TrustedProxies: peers whose CF-Connecting-IP / X-Forwarded-For header
	// gives the client address ("cloudflare", IPs, CIDRs; comma separated).
	TrustedProxies string
	PublicIP       string
	PublicHost     string
	BlazePort      int
	CertFile       string
	BaseDomain     string // BASE_DOMAIN: domain of the Redirector hosts (first-run setup)
	KeyFile        string
	DatabaseEnv    string // DATABASE_ENV: path of the SQLite database file (name kept for old configs)
	LogTDF         bool
	// SchemaCheck (SCHEMA_CHECK): check every packet sent against the game's TDF
	// schema and log [SCHEMA] issues (development).
	SchemaCheck bool
	// ClientConfigDir holds Util::fetchClientConfig sections as <SECTION>.cfg.
	ClientConfigDir string
	// FriendsAll: every player is everyone's friend.
	FriendsAll bool
	// LockerDir stores Locker uploads (photo-mode snapshots).
	LockerDir string
	// WebAutologHost is sent as V8ServerName when client_config does not set it.
	// "" = PUBLIC_HOST:18443, "off" = not sent.
	WebAutologHost string
	// LockerBaseURL: public base URL for Locker files.
	// "" = http://<PUBLIC_HOST>:<HTTP_LISTEN port>.
	LockerBaseURL string

	// AuthMode: "invite" (a new player needs a one-time invite code) or
	// "open" (the first launcher key that uses a free name owns it).
	AuthMode string
	// AuthSessionHours: lifetime of a launcher session ticket; the launcher
	// renews it before it expires.
	AuthSessionHours int

	// ClientGuard: only the game may use the public ports (protocol checks,
	// login required, HTTP User-Agent check).
	ClientGuard bool
	// GuardAllowIPs: optional allowlist (IPs or CIDRs, comma separated).
	GuardAllowIPs string
	// GuardMaxConnPerIP: connections per remote IP (0 = no limit).
	GuardMaxConnPerIP int
	// MMMode: auto = race servers when registered, else peer; dedicated = always
	// wait for a race server; peer = never use race servers.
	MMMode string
	// MMDefaultPlaylist: playlist GUID(s), comma separated, for Quick Match games.
	MMDefaultPlaylist string

	// Dedicated race servers.
	DSEnabled bool   // DS_ENABLED
	DSGameDir string // DS_GAME_DIR: game folder
	// DSAllowSolo (DS_ALLOW_SOLO): -NfsOnline.AllowSinglePlayerOnline true, so a
	// playlist keeps running with one player.
	DSAllowSolo bool
	// DSExtraPlaylists (DS_EXTRA_PLAYLISTS): add the 10 unused playlists to the
	// race servers' PlaylistSet (experimental). Players need EXTRA_PLAYLISTS=1.
	DSExtraPlaylists bool
	// MMQuickExtra (MM_QUICK_EXTRA): extra playlists may enter the Quick Match
	// pool.
	MMQuickExtra   bool
	ConsoleMode    string // CONSOLE_MODE: dashboard or log
	LogMaxMB       int    // LOG_MAX_MB: rotate logs\server.log above this size (0 = never)
	ConsoleRefresh int    // CONSOLE_REFRESH: seconds between dashboard redraws
	DSSourceExe    string // DS_SOURCE_EXE: game exe the server exe is made from
	DSExe          string // DS_EXE: server exe name (created/updated automatically)
	DSCount        int    // DS_COUNT: instances = races at the same time
	DSPortBase     int    // DS_PORT_BASE: instance i uses UDP DSPortBase+i
	DSListenIP     string // DS_LISTEN_IP
	// DSListenOffset (DS_LISTEN_OFFSET): the Frostbite -listen port is
	// port+offset, apart from the mesh port.
	DSListenOffset int
	// DSMeshPort (DS_MESH_PORT): UDP port players are told for a race server;
	// 0 = what it reports (its -GamePort).
	DSMeshPort int
	// UDP relay between players and race servers (RELAY_*).
	RelayEnabled   bool
	RelayPortBase  int
	RelayPortCount int
	RelayDSHost    string
	DSPublicIP     string // DS_PUBLIC_IP: address players connect to (empty = auto)
	DSExtraArgs    string // DS_EXTRA_ARGS
	DSLauncher     string // DS_LAUNCHER: program that starts the race-server exe (gets the exe and its arguments), e.g. a Proton wrapper on Linux
	DSKeepHosts    bool   // DS_KEEP_HOSTS: leave the exe's Redirector host names alone (a plugin redirects them)
	DSInstanceDir  string // DS_INSTANCE_DIR: per-instance -serverInstancePath root
	DSRestartSec   int    // DS_RESTART_DELAY seconds
	DSKillStale    bool   // DS_KILL_STALE: end leftover server processes at start
	// On-demand race servers: DS_COUNT is the most that run at once, DS_IDLE are
	// kept running while nobody plays, idle ones stop after DS_IDLE_EXIT seconds.
	DSIdle        int
	DSIdleExitSec int
	// MMTimeoutSec (MM_TIMEOUT): how long Quick Match waits for a race server;
	// the client only stops when the server says so.
	MMTimeoutSec int
	// DSSetupWaitSec (DS_SETUP_WAIT): after a reset, players get their game once
	// the race server reaches PRE_GAME or after this many seconds.
	DSSetupWaitSec int
}

func Default() Config {
	return Config{
		RedirectorListen:        "127.0.0.1:42128",
		MMMode:                  "auto",
		DSSourceExe:             "Need For Speed The Run.exe",
		DSExe:                   "NFSTR_DedicatedServer.exe",
		DSCount:                 1,
		DSPortBase:              25200,
		DSListenIP:              "0.0.0.0",
		DSListenOffset:          100,
		DSMeshPort:              0,
		RelayEnabled:            true,
		RelayPortBase:           6100,
		RelayPortCount:          64,
		DSInstanceDir:           "data/ds",
		DSRestartSec:            10,
		DSIdle:                  0,
		DSIdleExitSec:           60,
		MMTimeoutSec:            45,
		DSSetupWaitSec:          10,
		DSKillStale:             true,
		DSAllowSolo:             true,
		DSExtraPlaylists:        false,
		ConsoleMode:             "dashboard",
		ConsoleRefresh:          15,
		LogMaxMB:                200,
		MMDefaultPlaylist:       "A2BD2E19-116D-43A6-A338-D47F6D1EA729",
		BlazeListen:             "127.0.0.1:10042",
		RedirectorFrontListen:   ":42127",
		BlazeFrontListen:        ":10041",
		RedirectorGatewayTarget: "127.0.0.1:42129",
		HTTPListen:              ":18080",
		BansFile:                "config/bans.txt",
		RaceSettingsFile:        "config/race_settings.json",
		LauncherHTTPSListen:     ":8443",
		LauncherTLSCert:         "certs/launcher.crt",
		LauncherTLSKey:          "certs/launcher.key",
		PublicIP:                "127.0.0.1",
		PublicHost:              "127.0.0.1",
		BlazePort:               10041,
		CertFile:                "certs/protossl_redirector.crt",
		KeyFile:                 "certs/protossl_redirector.key",
		DatabaseEnv:             "config/nfsrun.db",
		ClientConfigDir:         "client_config",
		FriendsAll:              true,
		LockerDir:               "data/locker",
		AuthMode:                "invite",
		AuthSessionHours:        8,
		ClientGuard:             true,
		GuardMaxConnPerIP:       8,
	}
}

func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	vals := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		p := strings.SplitN(line, "=", 2)
		if len(p) != 2 {
			continue
		}
		vals[strings.ToUpper(strings.TrimSpace(p[0]))] = strings.TrimSpace(p[1])
	}
	if err := s.Err(); err != nil {
		return c, err
	}
	set := func(k string, dst *string) {
		if v, ok := vals[k]; ok && v != "" {
			*dst = v
		}
	}
	set("REDIRECTOR_LISTEN", &c.RedirectorListen)
	set("BLAZE_LISTEN", &c.BlazeListen)
	set("REDIRECTOR_FRONT_LISTEN", &c.RedirectorFrontListen)
	set("BLAZE_FRONT_LISTEN", &c.BlazeFrontListen)
	set("REDIRECTOR_GATEWAY_TARGET", &c.RedirectorGatewayTarget)
	set("HTTP_LISTEN", &c.HTTPListen)
	set("ADMIN_WEB_LISTEN", &c.AdminWebListen)
	set("ADMIN_WEB_USER", &c.AdminWebUser)
	set("ADMIN_WEB_PASSWORD", &c.AdminWebPassword)
	set("BANS_FILE", &c.BansFile)
	set("RACE_SETTINGS_FILE", &c.RaceSettingsFile)
	if v, ok := vals["LAUNCHER_HTTPS_LISTEN"]; ok {
		if strings.EqualFold(v, "off") || v == "0" || strings.EqualFold(v, "false") {
			v = ""
		}
		c.LauncherHTTPSListen = v
	}
	set("LAUNCHER_TLS_CERT", &c.LauncherTLSCert)
	set("LAUNCHER_TLS_KEY", &c.LauncherTLSKey)
	set("LAUNCHER_URL", &c.LauncherURL)
	set("TRUSTED_PROXIES", &c.TrustedProxies)
	set("PUBLIC_IP", &c.PublicIP)
	set("PUBLIC_HOST", &c.PublicHost)
	set("CERT_FILE", &c.CertFile)
	set("BASE_DOMAIN", &c.BaseDomain)
	set("KEY_FILE", &c.KeyFile)
	set("DATABASE_ENV", &c.DatabaseEnv)
	set("CLIENT_CONFIG_DIR", &c.ClientConfigDir)
	set("LOCKER_DIR", &c.LockerDir)
	set("WEB_AUTOLOG_HOST", &c.WebAutologHost)
	set("LOCKER_BASE_URL", &c.LockerBaseURL)
	if v, ok := vals["AUTH_MODE"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "invite", "":
			c.AuthMode = "invite"
		case "open":
			c.AuthMode = "open"
		default:
			return c, fmt.Errorf("invalid AUTH_MODE %q (use invite or open)", v)
		}
	}
	if v, ok := vals["AUTH_SESSION_HOURS"]; ok {
		n, e := strconv.Atoi(strings.TrimSpace(v))
		if e != nil || n < 1 || n > 72 {
			return c, fmt.Errorf("invalid AUTH_SESSION_HOURS %q (use 1..72)", v)
		}
		c.AuthSessionHours = n
	}
	if v, ok := vals["BLAZE_PORT"]; ok {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("invalid BLAZE_PORT")
		}
		c.BlazePort = n
	}
	if v, ok := vals["FRIENDS_ALL"]; ok {
		c.FriendsAll = !(strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "off"))
	}
	if v, ok := vals["CLIENT_GUARD"]; ok {
		c.ClientGuard = !(strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "off"))
	}
	set("GUARD_ALLOW_IPS", &c.GuardAllowIPs)
	if v, ok := vals["GUARD_MAX_CONN_PER_IP"]; ok {
		if n, e := strconv.Atoi(v); e == nil && n >= 0 {
			c.GuardMaxConnPerIP = n
		}
	}
	if v, ok := vals["MM_MODE"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "dedicated", "ds", "server":
			c.MMMode = "dedicated"
		case "peer", "p2p":
			c.MMMode = "peer"
		default:
			c.MMMode = "auto"
		}
	}
	if v, ok := vals["MM_DEFAULT_PLAYLIST"]; ok {
		c.MMDefaultPlaylist = strings.TrimSpace(v)
	}
	boolVal := func(v string) bool {
		v = strings.ToLower(strings.TrimSpace(v))
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	intVal := func(key string, dst *int, lo, hi int) {
		if v, ok := vals[key]; ok {
			if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n >= lo && n <= hi {
				*dst = n
			}
		}
	}
	if v, ok := vals["DS_ENABLED"]; ok {
		c.DSEnabled = boolVal(v)
	}
	if v, ok := vals["CONSOLE_MODE"]; ok {
		c.ConsoleMode = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := vals["LOG_MAX_MB"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			c.LogMaxMB = n
		}
	}
	if v, ok := vals["CONSOLE_REFRESH"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			c.ConsoleRefresh = n
		}
	}
	if v, ok := vals["DS_KEEP_HOSTS"]; ok {
		c.DSKeepHosts = boolVal(v)
	}
	if v, ok := vals["DS_ALLOW_SOLO"]; ok {
		c.DSAllowSolo = boolVal(v)
	}
	if v, ok := vals["DS_EXTRA_PLAYLISTS"]; ok {
		c.DSExtraPlaylists = boolVal(v)
	}
	if v, ok := vals["MM_QUICK_EXTRA"]; ok {
		c.MMQuickExtra = boolVal(v)
	}
	if v, ok := vals["DS_KILL_STALE"]; ok {
		c.DSKillStale = boolVal(v)
	}
	set("DS_GAME_DIR", &c.DSGameDir)
	set("DS_SOURCE_EXE", &c.DSSourceExe)
	// Installed as <game folder>\NFSTR_Server: the game is one folder up.
	if c.DSGameDir == "" {
		if _, err := os.Stat(filepath.Join("..", c.DSSourceExe)); err == nil {
			c.DSGameDir = ".."
			if _, set := vals["DS_ENABLED"]; !set {
				c.DSEnabled = true
			}
		}
	}
	set("DS_EXE", &c.DSExe)
	set("DS_LISTEN_IP", &c.DSListenIP)
	set("DS_PUBLIC_IP", &c.DSPublicIP)
	set("DS_EXTRA_ARGS", &c.DSExtraArgs)
	set("DS_LAUNCHER", &c.DSLauncher)
	set("DS_INSTANCE_DIR", &c.DSInstanceDir)
	intVal("DS_COUNT", &c.DSCount, 0, 64)
	intVal("DS_PORT_BASE", &c.DSPortBase, 1024, 65000)
	intVal("DS_LISTEN_OFFSET", &c.DSListenOffset, 0, 1000)
	intVal("DS_MESH_PORT", &c.DSMeshPort, 0, 65535)
	intVal("RELAY_PORT_BASE", &c.RelayPortBase, 1024, 65000)
	intVal("RELAY_PORT_COUNT", &c.RelayPortCount, 1, 1000)
	set("RELAY_DS_HOST", &c.RelayDSHost)
	if v, ok := vals["RELAY_ENABLED"]; ok {
		c.RelayEnabled = !(strings.EqualFold(v, "0") || strings.EqualFold(v, "false") || strings.EqualFold(v, "no"))
	}
	intVal("DS_RESTART_DELAY", &c.DSRestartSec, 1, 3600)
	intVal("DS_IDLE", &c.DSIdle, 0, 64)
	intVal("DS_IDLE_EXIT", &c.DSIdleExitSec, 5, 86400)
	intVal("MM_TIMEOUT", &c.MMTimeoutSec, 5, 600)
	intVal("DS_SETUP_WAIT", &c.DSSetupWaitSec, 1, 120)
	if v, ok := vals["LOG_TDF"]; ok {
		c.LogTDF = strings.EqualFold(v, "1") || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	if v, ok := vals["SCHEMA_CHECK"]; ok {
		c.SchemaCheck = !(strings.EqualFold(v, "0") || strings.EqualFold(v, "false") || strings.EqualFold(v, "no"))
	}
	return c, nil
}
