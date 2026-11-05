package lsx

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCryptoRoundTrip(t *testing.T) {
	c, err := encryptHex(challengeKey, []byte("cacf897a20b6d612ad0c05e011df52bb"))
	if err != nil || len(c) != 96 || strings.ToLower(c) != c {
		t.Fatalf("c=%q err=%v", c, err)
	}
	if !checkChallengeResponse(c, "cacf897a20b6d612ad0c05e011df52bb") {
		t.Fatal("challenge round trip")
	}
	k := seededKey(seedFromResponse(c))
	if len(k) != 16 || bytes.Equal(k, seededKey(seedFromResponse("00"))) {
		t.Fatal("seeded key")
	}
	if _, err := decryptHex(challengeKey, "zz"); err == nil {
		t.Fatal("garbage must not decrypt")
	}
}

// CRandom reference: seed 7 -> first Rand() = ((7*214013+2531011)>>16)&0xFFFF.
func TestCRandom(t *testing.T) {
	var r cRandom
	r.Seed(7)
	if got, want := r.Rand(), uint32((7*214013+2531011)>>16&0xFFFF); got != want {
		t.Fatalf("got %d want %d", got, want)
	}
}

// fakeSDK plays the game side of the protocol.
func fakeSDK(t *testing.T, addr, version string, plaintext bool) (send func(string) string, closeFn func()) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	ch, err := readMsg(r)
	if err != nil {
		t.Fatal(err)
	}
	ourKey := regexp.MustCompile(`key="([^"]+)"`).FindStringSubmatch(ch)[1]
	clientKey := "0123456789abcdef0123456789abcdef"
	resp, _ := encryptHex(challengeKey, []byte(ourKey))
	verAttr := ""
	if version != "" {
		verAttr = fmt.Sprintf(` version="%s"`, version)
	}
	writeMsg(c, fmt.Sprintf(`<LSX><Request recipient="EALS" id="1"><ChallengeResponse response="%s" key="%s"%s><ContentId>71067</ContentId><Title>Need for Speed The Run</Title></ChallengeResponse></Request></LSX>`, resp, clientKey, verAttr))
	acc, err := readMsg(r)
	if err != nil {
		t.Fatal(err)
	}
	want := makeChallengeResponse(clientKey)
	if !strings.Contains(acc, `response="`+want+`"`) || !strings.Contains(acc, `id="1"`) {
		t.Fatalf("accept=%s", acc)
	}
	key := challengeKey
	if version != "2" {
		key = seededKey(seedFromResponse(want))
	}
	return func(xml string) string {
			out := xml
			if !plaintext {
				out, _ = encryptHex(key, []byte(xml))
			}
			writeMsg(c, out)
			rep, err := readMsg(r)
			if err != nil {
				t.Fatal(err)
			}
			if plaintext {
				return rep
			}
			p, err := decryptHex(key, rep)
			if err != nil {
				t.Fatalf("reply not decryptable: %v", err)
			}
			return p
		}, func() {
			c.Close()
		}
}

func runModes(t *testing.T, version string, plaintext bool) {
	var logs bytes.Buffer
	s := NewServer(Identity{Persona: "EXAMPLE-CLIENT1.0", UserID: 1000001, PersonaID: 1000001, AuthCode: "nfsrun-v1.abc"}, log.New(&logs, "", 0))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.Serve(l)
	send, done := fakeSDK(t, l.Addr().String(), version, plaintext)
	defer done()

	p := send(`<LSX><Request recipient="EbisuSDK" id="2"><GetProfile index="0" /></Request></LSX>`)
	if !strings.Contains(p, `Persona="EXAMPLE-CLIENT1.0"`) || !strings.Contains(p, `id="2"`) {
		t.Fatalf("profile=%s", p)
	}
	if a := send(`<LSX><Request recipient="Utility" id="3"><GetAuthCode UserId="1000001" ClientId="NFS" /></Request></LSX>`); !strings.Contains(a, `<AuthCode value="nfsrun-v1.abc" />`) {
		t.Fatalf("auth=%s", a)
	}
	if g := send(`<LSX><Request recipient="EbisuSDK" id="4"><GetSetting SettingId="ENVIRONMENT" /></Request></LSX>`); !strings.Contains(g, `Setting="production"`) {
		t.Fatalf("setting=%s", g)
	}
	if u := send(`<LSX><Request recipient="XMPP" id="5"><SetPresence UserId="1" Presence="INGAME" /></Request></LSX>`); !strings.Contains(u, "ErrorSuccess") {
		t.Fatalf("generic=%s", u)
	}
	if !strings.Contains(logs.String(), `contentId="71067"`) {
		t.Fatalf("log:\n%s", logs.String())
	}
}

func TestHandshakeFixedKey(t *testing.T)  { runModes(t, "2", false) }
func TestHandshakeSeededKey(t *testing.T) { runModes(t, "3", false) }
func TestHandshakePlaintext(t *testing.T) { runModes(t, "2", true) }

func TestParseRequest(t *testing.T) {
	r, ok := parseRequest(`<LSX><Request recipient="EbisuSDK" id="17"><GetGameInfo GameInfoId="UPTODATE" version="1"/></Request></LSX>`)
	if !ok || r.ID != "17" || r.Name != "GetGameInfo" || r.Attrs["GameInfoId"] != "UPTODATE" {
		t.Fatalf("%+v", r)
	}
}

func TestQueryFriendsFromServerList(t *testing.T) {
	var logs bytes.Buffer
	s := NewServer(Identity{Persona: "PlayerOne", UserID: 7, PersonaID: 7, Friends: func() []Friend {
		return []Friend{{Persona: "PlayerOne", UserID: 7}, {Persona: "Rival<1>", UserID: 1234567890, Online: true}, {Persona: "Old", UserID: 99}}
	}}, log.New(&logs, "", 0))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.Serve(l)
	send, done := fakeSDK(t, l.Addr().String(), "2", false)
	defer done()
	r := send(`<LSX><Request recipient="XMPP" id="5"><QueryFriends version="2" UserId="7"/></Request></LSX>`)
	if strings.Contains(r, `Persona="PlayerOne"`) || !strings.Contains(r, `Persona="Rival&lt;1&gt;" PersonaId="1234567890" State="MUTUAL"`) ||
		!strings.Contains(r, `Presence="INGAME"`) || !strings.Contains(r, `UserId="99"`) || !strings.Contains(r, `Presence="OFFLINE"`) {
		t.Fatalf("friends=%s", r)
	}
}

func TestPushEventAndPresence(t *testing.T) {
	var logs bytes.Buffer
	s := NewServer(Identity{Persona: "Ze2", UserID: 2, PersonaID: 2, Friends: func() []Friend {
		return []Friend{{Persona: "Ze1", UserID: 1, Online: true}, {Persona: "Ze3", UserID: 3}}
	}}, log.New(&logs, "", 0))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.Serve(l)
	send, done := fakeSDK(t, l.Addr().String(), "2", false)
	defer done()
	if p := send(`<LSX><Request recipient="XMPP" id="2"><GetPresence version="2" UserId="1"/></Request></LSX>`); !strings.Contains(p, `UserId="1"`) || !strings.Contains(p, `Presence="INGAME"`) {
		t.Fatalf("presence=%s", p)
	}
	if p := send(`<LSX><Request recipient="XMPP" id="3"><QueryPresence version="2" UserId="2"/></Request></LSX>`); !strings.Contains(p, `<Friend UserId="3" Presence="OFFLINE"`) {
		t.Fatalf("query=%s", p)
	}
	if n := s.PushPresence(Friend{Persona: "Ze1", UserID: 1, Online: true, Presence: "JOINABLE", SessionID: "1"}); n != 1 {
		t.Fatalf("pushed to %d", n)
	}
	// the next message the game reads is the (encrypted) event: a
	// GetPresenceResponse, the only presence element this SDK has a handler for
	if ev := send(`<LSX><Request recipient="Utility" id="4"><GetInternetConnectedState version="2"/></Request></LSX>`); !strings.HasPrefix(ev, `<LSX><Event sender="XMPP"><GetPresenceResponse `) || !strings.Contains(ev, `UserId="1"`) || !strings.Contains(ev, `Presence="JOINABLE"`) || !strings.Contains(ev, `SessionId="1"`) {
		t.Fatalf("event=%s", ev)
	}
	s.PushFriends()
	// the reply to id 4 may still be ahead of the pushed event (timing): skip
	// plain responses until an event arrives
	ev := ""
	for i := 5; i < 8; i++ {
		ev = send(fmt.Sprintf(`<LSX><Request recipient="Utility" id="%d"><GetInternetConnectedState version="2"/></Request></LSX>`, i))
		if strings.HasPrefix(ev, `<LSX><Event `) {
			break
		}
	}
	if !strings.HasPrefix(ev, `<LSX><Event sender="XMPP"><QueryFriendsResponse><Friend `) {
		t.Fatalf("friends event=%s", ev)
	}
}

func TestInvitesAndPresenceRelay(t *testing.T) {
	got := make(chan string, 4)
	s := NewServer(Identity{Persona: "A", UserID: 1000001,
		OnSetPresence: func(p, sess, rich string) { got <- p + "/" + sess },
		OnSendInvite: func(to []uint64, sess string) {
			got <- fmt.Sprint(to) + "/" + sess
		},
		Friends: func() []Friend {
			return []Friend{{Persona: "B", UserID: 1000002, Online: true, Presence: "JOINABLE", SessionID: "1000002"}}
		},
	}, nil)
	s.Respond(`<LSX><Request recipient="XMPP" id="1"><SetPresence version="2" UserId="1000001" Presence="JOINABLE" RichPresence="x" GamePresence="" SessionId="1000001"/></Request></LSX>`)
	if v := <-got; v != "JOINABLE/1000001" {
		t.Fatal(v)
	}
	s.Respond(`<LSX><Request recipient="XMPP" id="2"><SendInvite version="2" UserId="1000001" Invitation=""><Invitees>1000002</Invitees></SendInvite></Request></LSX>`)
	if v := <-got; v != "[1000002]/1000001" {
		t.Fatal(v)
	}
	s.Respond(`<LSX><Request recipient="XMPP" id="3"><SendInvite version="2" UserId="1000001" Invitation=""><Invitees UserId="1000003"/></SendInvite></Request></LSX>`)
	if v := <-got; v != "[1000003]/1000001" {
		t.Fatal(v)
	}
	if r := s.Respond(`<LSX><Request recipient="XMPP" id="4"><QueryFriends version="2" UserId="1000001"/></Request></LSX>`); !strings.Contains(r, `Presence="JOINABLE" SessionId="1000002"`) {
		t.Fatal(r)
	}
	if r := s.Respond(`<LSX><Request recipient="EbisuSDK" id="6"><GetUserProfileByEmailorEAID version="2" KeyWord="b"/></Request></LSX>`); !strings.Contains(r, `<GetUserProfileByEmailorEAIDResponse Return="Success"><User EAID="B" MAIL="" UserId="1000002" /></GetUserProfileByEmailorEAIDResponse>`) {
		t.Fatal(r)
	}
	if r := s.Respond(`<LSX><Request recipient="XMPP" id="5"><GetPresence version="2" UserId="1000001"/></Request></LSX>`); !strings.Contains(r, `Presence="JOINABLE"`) || !strings.Contains(r, `SessionId="1000001"`) {
		t.Fatal(r)
	}
}
