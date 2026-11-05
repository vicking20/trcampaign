package server

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
)

// HNET must be a list with element type 3 (the race server's decoder
// drops the list otherwise and crashes at 0x1a146b5).
func TestHNETElementTypeStruct(t *testing.T) {
	ds := &connState{}
	ds.Persona.BlazeID = 1000005
	g := &game{id: 4, ds: ds, dsNet: netInfo{Set: true, ExtIP: 0xB5D6DDB6, ExtPort: 3659, IntIP: 0x0A000005, IntPort: 3659}}
	g.req.NTop, g.req.PCap = 1, 8
	b := g.buildNotifyGameSetup(setupReason{key: reasonDataless, dataless: datalessCreateGame})
	tag := []byte{0xa2, 0xe9, 0x74} // "HNET"
	i := bytes.Index(b, tag)
	// list, element type 3, 1 element, member 2 (IpPair), then EXIP directly (no VALU)
	if i < 0 || b[i+3] != byte(blaze.TDFList) || b[i+4] != byte(blaze.TDFGroup) || b[i+5] != 1 || b[i+6] != 2 || !bytes.Equal(b[i+7:i+10], []byte{0x97, 0x8a, 0x70}) {
		t.Fatalf("HNET header % x", b[i:i+8])
	}
	fs, err := blaze.DecodeAll(b)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := parseNetworkAddressList(fieldGroupOf(fs, "GAME"), "HNET")
	if !ok || n.ExtIP != 0xB5D6DDB6 || n.IntIP != 0x0A000005 || n.IntPort != 3659 {
		t.Fatalf("decoded %+v %v", n, ok)
	}
	if fieldUintAuth(fieldGroupOf(fs, "GAME"), "NTOP") != 1 {
		t.Fatal("fields after HNET lost")
	}
}

// The race server's own createGame: HNET as list<3> with
// member byte + IpPair body. It must decode completely (NTOP 1 at the end).
func TestDSCreateGameRawDecodes(t *testing.T) {
	raw, _ := hex.DecodeString("8B4C2C090000009E3D320104736A63009E5BB400009EE86D011057494E2D384F413343435141453444009F3974008F049F4E700101009F5CAC0116687474703A2F2F66616B6575726C2E65612E636F6D00A2E97404030102978A7003A7000000B6F6B6DD16C2FCB400B0890300A6EA7003A7000000B6F6B6DD16C2FCB400B089030000A67BAF0000BB29730000BB4BF00001C23870040002800100C27A64010100C27CE30200C2D878008001C329730001C638700000CA7A640000CECBF40000D238700000D2993800BFFF07DAFA700001DB3D3201053231303000")
	fs, err := blaze.DecodeAll(raw)
	if err != nil {
		t.Fatal(err)
	}
	req := parseCreateGame(fs)
	if req.NTop != 1 || req.Net.IntIP != 0xB5D6DDB6 || req.Net.IntPort != 25200 {
		t.Fatalf("NTOP %d net %+v", req.NTop, req.Net)
	}
}

func TestCrashLines(t *testing.T) {
	msg := "Nome do aplicativo com falha: NFSTR_DedicatedServer.exe\r\nCódigo de exceção: 0xc0000005\r\nDeslocamento de falha: 0x016146b5\r\n"
	out := strings.Join(crashLines(msg), "|")
	if !strings.Contains(out, "0x1a146b5") || !strings.Contains(out, "0xc0000005") {
		t.Fatal(out)
	}
}

func TestParseNetstatUDP(t *testing.T) {
	out := "Active Connections\r\n\r\n  Proto  Local Address          Foreign Address        State           PID\r\n  UDP    0.0.0.0:6000           *:*                                    6684\r\n  UDP    0.0.0.0:62637          *:*                                    6684\r\n  UDP    0.0.0.0:9999           *:*                                    6684\r\n  UDP    0.0.0.0:500            *:*                                    123\r\n  UDP    [::]:6000              *:*                                    6684\r\n"
	got := parseNetstatUDP(out, 6684)
	if len(got) != 3 || got[0] != 6000 || got[1] != 62637 || got[2] != 9999 {
		t.Fatal(got)
	}
}
