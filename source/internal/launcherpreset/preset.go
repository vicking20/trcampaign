// Package launcherpreset: the server address and certificate pin built into
// a launcher exe, so players need no settings.
//
// The launcher keeps a fixed-size string (Block) in its data. The server
// owner's own nfsrun-server.exe rewrites it in place (--stamp-launcher), so
// no Go toolchain is needed; the length never changes, the exe stays valid.
// Source builders can use -ldflags "-X main.bakedServerHTTP=... -X
// main.bakedServerPin=..." instead.
package launcherpreset

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

const (
	// Header starts the block. It must occur exactly once in a launcher exe.
	Header = "@@NFSRUN-LAUNCHER-PRESET-V1@@"
	pad64  = "################################################################"
	// Empty is the unstamped payload (Size bytes of '#').
	Empty = pad64 + pad64 + pad64 + pad64 + pad64 + pad64 + pad64 + pad64
	// Block is the whole unstamped block; the launcher keeps it in a var.
	Block = Header + Empty
	// Size is the payload length after Header.
	Size = len(Empty)
)

// Preset is what a player's launcher needs to know about the server.
type Preset struct {
	ServerHTTP string // launcher API address
	Pin        string // SPKI SHA-256 of a self-signed certificate ("" = CA)
	Domain     string // base domain of the Redirector hosts (patched into the game copy)
}

// Parse reads a (possibly stamped) block.
func Parse(block string) Preset {
	var p Preset
	if len(block) <= len(Header) {
		return p
	}
	payload := strings.TrimRight(block[len(Header):], "#\x00")
	for _, line := range strings.Split(payload, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "SERVER_HTTP":
			p.ServerHTTP = strings.TrimRight(v, "/")
		case "SERVER_CERT_PIN":
			p.Pin = v
		case "BASE_DOMAIN":
			p.Domain = v
		}
	}
	return p
}

// Stamp returns a copy of exe with the preset written into its block.
// An already stamped exe can be stamped again.
func Stamp(exe []byte, p Preset) ([]byte, error) {
	p.ServerHTTP = strings.TrimRight(strings.TrimSpace(p.ServerHTTP), "/")
	p.Pin, p.Domain = strings.TrimSpace(p.Pin), strings.TrimSpace(p.Domain)
	if strings.ContainsAny(p.ServerHTTP+p.Pin+p.Domain, "\r\n#") {
		return nil, errors.New("the preset must not contain line breaks or '#'")
	}
	payload := "SERVER_HTTP=" + p.ServerHTTP + "\nSERVER_CERT_PIN=" + p.Pin + "\nBASE_DOMAIN=" + p.Domain + "\n"
	if len(payload) > Size {
		return nil, fmt.Errorf("preset is %d bytes, the launcher has room for %d", len(payload), Size)
	}
	h := []byte(Header)
	at := bytes.Index(exe, h)
	if at < 0 {
		return nil, errors.New("this launcher exe has no preset block")
	}
	if bytes.Index(exe[at+len(h):], h) >= 0 {
		return nil, errors.New("the preset block occurs more than once in this exe")
	}
	if at+len(h)+Size > len(exe) {
		return nil, errors.New("truncated preset block")
	}
	out := append([]byte(nil), exe...)
	body := out[at+len(h) : at+len(h)+Size]
	copy(body, payload)
	for i := len(payload); i < Size; i++ {
		body[i] = '#'
	}
	return out, nil
}
