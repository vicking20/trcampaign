// nfstr-origin is the local stand-in for the Origin client's LSX service
// (127.0.0.1:3216), a Go port of helper/lsx.py. The game's EbisuSDK talks
// NUL-terminated XML to it; without an answer the game says you must sign in
// to Origin. It also turns the ini's name and password into the one-time game
// code the server accepts (POST /nfsrun/auth/password).
package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var replyGap = 20 * time.Millisecond

var port = 3216 // NFSTR_LSX_PORT overrides (tests)

var lsxKey = func() []byte {
	k := make([]byte, 16)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}()

// ---- AES-128-ECB, key 00..0f, PKCS padding, hex ----

func encryptHex(text string) string {
	data := []byte(text)
	pad := 16 - len(data)%16
	data = append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
	blk, _ := aes.NewCipher(lsxKey)
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += 16 {
		blk.Encrypt(out[i:], data[i:i+16])
	}
	return hex.EncodeToString(out)
}

func decryptHex(h string) (string, error) {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw)%16 != 0 {
		return "", fmt.Errorf("bad encrypted message")
	}
	blk, _ := aes.NewCipher(lsxKey)
	out := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 16 {
		blk.Decrypt(out[i:], raw[i:i+16])
	}
	if n := len(out); n > 0 {
		if p := int(out[n-1]); p >= 1 && p <= 16 && p <= n {
			out = out[:n-p]
		}
	}
	return string(out), nil
}

// ---- ini ----

type iniFile map[string]string // "section.key" lower case

func readINI(paths ...string) iniFile {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		m := iniFile{}
		sec := ""
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\xef\xbb\xbf"))
			switch {
			case l == "" || l[0] == ';' || l[0] == '#':
			case l[0] == '[' && strings.HasSuffix(l, "]"):
				sec = strings.ToLower(l[1 : len(l)-1])
			default:
				if k, v, ok := strings.Cut(l, "="); ok {
					m[sec+"."+strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
				}
			}
		}
		return m
	}
	return iniFile{}
}

var (
	ini                            iniFile
	persona                        string
	password                       string
	loginHost, loginPort, loginPin string
	userID                         string
	logf                           func(string, ...any)
)

var badName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func playerName(dir string) string {
	n := os.Getenv("NFSTR_NAME")
	if n == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "player_name.txt")); err == nil {
			n = strings.TrimSpace(string(b))
		}
	}
	if n == "" {
		n = ini["player.name"]
	}
	n = badName.ReplaceAllString(n, "")
	if len(n) > 16 {
		n = n[:16]
	}
	if n == "" {
		n = "TRPlayer"
	}
	return n
}

// fetchGameCode: name + password -> one-time game code (HTTPS, pinned).
func fetchGameCode() (string, error) {
	pin := strings.ToLower(strings.ReplaceAll(loginPin, ":", ""))
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // self-signed; pinned below
	tr.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if pin == "" || len(cs.PeerCertificates) == 0 {
			return nil
		}
		got := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if hex.EncodeToString(got[:]) != pin {
			return fmt.Errorf("server certificate does not match Pin (got %x)", got)
		}
		return nil
	}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"name": persona, "password": password})
	req, _ := http.NewRequest("POST", "https://"+net.JoinHostPort(loginHost, loginPort)+"/nfsrun/auth/password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "nfsrun-launcher/ini-1")
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code, Message, Error string
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode == 200 && out.Code != "" {
		return out.Code, nil
	}
	m := out.Message
	if m == "" {
		m = out.Error
	}
	return "", fmt.Errorf("%d %s", resp.StatusCode, m)
}

// ---- LSX ----

func wrap(kind, sender, rid, inner string, enc bool) []byte {
	idattr := ""
	if rid != "" {
		idattr = ` id="` + rid + `"`
	}
	x := fmt.Sprintf(`<LSX><%s sender="%s"%s>%s</%s></LSX>`, kind, sender, idattr, inner, kind)
	if enc {
		x = encryptHex(x)
	}
	return append([]byte(x), 0)
}

type lsxChild struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
}

func (c lsxChild) attr(n string) string {
	for _, a := range c.Attrs {
		if a.Name.Local == n {
			return a.Value
		}
	}
	return ""
}

type lsxReq struct {
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []lsxChild `xml:",any"`
}

type lsxRoot struct {
	Reqs []lsxReq `xml:",any"`
}

func (r lsxReq) attr(n string) string {
	for _, a := range r.Attrs {
		if a.Name.Local == n {
			return a.Value
		}
	}
	return ""
}

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func answer(c lsxChild, rid, rcp string) []byte {
	if rcp == "" {
		rcp = "EALS"
	}
	resp := func(inner string) []byte { return wrap("Response", rcp, rid, inner, true) }
	switch c.XMLName.Local {
	case "ChallengeResponse":
		return wrap("Response", rcp, rid, fmt.Sprintf(`<ChallengeAccepted response="%s"/>`, encryptHex(c.attr("key"))), false)
	case "GetConfig":
		return resp(`<GetConfigResponse version="2"/>`)
	case "GetProfile":
		return resp(fmt.Sprintf(`<GetProfileResponse version="2" UserId="%s" Persona="%s" AvatarId="0"/>`, userID, persona))
	case "GetInternetConnectedState":
		return resp(`<InternetConnectedState version="2" connected="1"/>`)
	case "GetAuthToken", "GetAuthCode":
		if password != "" {
			code, err := fetchGameCode()
			if err != nil {
				logf("password login failed: %v", err)
			} else {
				logf("%s -> one-time game code issued for %s", c.XMLName.Local, persona)
			}
			el := "AuthToken"
			if c.XMLName.Local == "GetAuthCode" {
				el = "AuthCode"
			}
			return resp(fmt.Sprintf(`<%s version="2" value="%s"/>`, el, esc(code)))
		}
		if c.XMLName.Local == "GetAuthToken" {
			tok := make([]byte, 16)
			copy(tok, persona)
			return resp(fmt.Sprintf(`<AuthToken version="2" value="%s"/>`, hex.EncodeToString(tok)))
		}
	case "CheckPermission":
		return resp(`<CheckPermissionResponse version="2" Access="GRANTED"/>`)
	case "GetSetting":
		return resp(`<GetSettingResponse version="2" Setting=""/>`)
	case "QueryFriends":
		return resp(`<QueryFriendsResponse version="2"/>`)
	case "GetBlockList":
		return resp(`<GetBlockListResponse version="2"/>`)
	case "GetPresence", "QueryPresence":
		return resp(fmt.Sprintf(`<GetPresenceResponse version="2" UserId="%s" Presence="ONLINE" Title="" TitleId="" RichPresence="" GamePresence="" SessionId=""/>`, userID))
	}
	return resp(`<ErrorSuccess version="2" Code="0" Description=""/>`)
}

func handle(c net.Conn) {
	defer c.Close()
	logf("LSX client %s", c.RemoteAddr())
	// The Python helper this replaces sent with Nagle on and ~ms latency; the game's
	// SDK stalls when replies and events arrive back to back, so keep that pacing.
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(false)
	}
	send := func(b []byte) {
		time.Sleep(replyGap)
		logf(">> %d bytes", len(b)-1)
		c.Write(b)
	}
	kb := make([]byte, 8)
	rand.Read(kb)
	skey := hex.EncodeToString(kb)
	send(wrap("Event", "EALS", "", fmt.Sprintf(`<Challenge version="3" key="%s"/>`, skey), false))
	r := bufio.NewReaderSize(c, 65536)
	for {
		msg, err := r.ReadBytes(0)
		if err != nil {
			return
		}
		txt := string(bytes.TrimSuffix(msg, []byte{0}))
		if txt != "" && !strings.HasPrefix(txt, "<") {
			if txt, err = decryptHex(strings.TrimSpace(txt)); err != nil {
				logf("   decrypt error: %v", err)
				continue
			}
		}
		logf("<< %s", txt)
		var root lsxRoot
		if err := xml.Unmarshal([]byte(txt), &root); err != nil {
			logf("   parse error: %v", err)
			continue
		}
		for _, req := range root.Reqs {
			for _, ch := range req.Children {
				out := answer(ch, req.attr("id"), req.attr("recipient"))
				send(out)
				name := ch.XMLName.Local
				if name == "ChallengeResponse" || name == "GetProfile" {
					send(wrap("Event", "EbisuSDK", "", `<Login version="2" IsLoggedIn="1"/>`, true))
				}
				if name == "ChallengeResponse" {
					send(wrap("Event", "EALS", "", `<Login version="2" IsLoggedIn="1"/>`, true))
				}
			}
		}
	}
}

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	ini = readINI(filepath.Join(dir, "NFSTR_Online.ini"), "NFSTR_Online.ini", filepath.Join("Scripts", "NFSTR_Online.ini"))
	persona = playerName(dir)
	password = os.Getenv("NFSTR_PASSWORD")
	if password == "" {
		password = ini["player.password"]
	}
	loginHost = ini["server.host"]
	if loginHost == "" {
		loginHost = "127.0.0.1"
	}
	loginPort = ini["server.loginport"]
	if n, _ := strconv.Atoi(loginPort); n == 0 {
		loginPort = "8443"
	}
	loginPin = ini["server.pin"]
	userID = "1000000001"
	if persona != "TRPlayer" {
		sum := 0
		for _, b := range []byte(persona) {
			sum += int(b)
		}
		userID = strconv.Itoa(2000000001 + sum%1000)
	}
	if v, _ := strconv.Atoi(os.Getenv("NFSTR_LSX_PORT")); v > 0 {
		port = v
	}
	lf, _ := os.OpenFile("lsx.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	w := io.Writer(os.Stdout)
	if lf != nil {
		w = io.MultiWriter(os.Stdout, lf)
	}
	lg := log.New(w, "", 0)
	logf = func(f string, a ...any) { lg.Printf(time.Now().Format("[15:04:05] ")+f, a...) }
	if len(os.Args) > 1 && os.Args[1] == "--selftest" {
		fmt.Println(encryptHex("<LSX/>"))
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "another sign-in helper is already running on port %d\n", port)
		os.Exit(1)
	}
	logf("LSX stub on 127.0.0.1:%d as %s", port, persona)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go handle(c)
	}
}
