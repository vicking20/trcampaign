package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"nfsrun-server/internal/blaze"
)

// Legal documents (Terms of Service / Privacy Policy).
//
// The Policy screens send getTosInfo (0x2A), then getTermsOfServiceContent
// (0xF6) or getPrivacyPolicyContent (0x2F). With an empty TosHost/URI in
// GetTosInfoResponse the client asks for the text over Blaze (otherwise it
// downloads it over HTTP). TEXT 0 = plain text. The client copies TCOL bytes
// of TCOT, so TCOL must be the byte length of TCOT.
//
// Texts: data\legal\terms_of_service.txt and privacy_policy.txt (defaults are
// written on first use). A language file (terms_of_service.<LANG>.txt, e.g.
// .ptBR) is used when the game sends that LANG.

const (
	legalTOS     = "terms_of_service"
	legalPrivacy = "privacy_policy"
)

func (s *Server) legalDir() string {
	return filepath.Join(filepath.Dir(s.cfg.LockerDir), "legal")
}

// legalDoc returns the text and a version string for a document.
func (s *Server) legalDoc(kind, lang string) (text, version string) {
	dir := s.legalDir()
	names := []string{}
	if l := legalLangSuffix(lang); l != "" {
		names = append(names, kind+"."+l+".txt")
	}
	names = append(names, kind+".txt")
	var raw []byte
	for _, n := range names {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			raw = b
			break
		}
	}
	if raw == nil {
		def := defaultTOS
		if kind == legalPrivacy {
			def = defaultPrivacy
		}
		raw = []byte(def)
		if err := os.MkdirAll(dir, 0755); err == nil {
			p := filepath.Join(dir, kind+".txt")
			if os.WriteFile(p, []byte(strings.ReplaceAll(def, "\n", "\r\n")), 0644) == nil {
				s.log.Printf("[AUTH] created %s (shown in the game's Terms/Privacy screen; edit it freely)", p)
			}
		}
	}
	text = normalizeLegalText(raw)
	sum := sha256.Sum256([]byte(text))
	return text, "nfsrun-" + hex.EncodeToString(sum[:4])
}

// legalLangSuffix keeps only letters and digits of the game's LANG ("ptBR").
func legalLangSuffix(lang string) string {
	var b strings.Builder
	for _, r := range lang {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeLegalText: no BOM, "\n" line breaks, no NUL bytes (the client
// treats the text as a C string).
func normalizeLegalText(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	t := strings.ReplaceAll(string(b), "\r\n", "\n")
	t = strings.ReplaceAll(t, "\r", "\n")
	t = strings.ReplaceAll(t, "\x00", "")
	return strings.TrimRight(t, "\n") + "\n"
}

// GetTosInfoResponse{EAMC, PMC, PRIV, THST, TURI}: no download host, so the
// client asks for the content over Blaze.
func buildTosInfo() []byte {
	var w blaze.Writer
	w.TagVar("EAMC", 0)
	w.TagVar("PMC", 0)
	w.TagString("PRIV", "")
	w.TagString("THST", "")
	w.TagString("TURI", "")
	return w.Bytes()
}

// GetLegalDocContentResponse{LDVC, TCOL, TCOT}.
func buildLegalDocContent(text, version string) []byte {
	var w blaze.Writer
	w.TagString("LDVC", version)
	w.TagVar("TCOL", uint64(len(text)))
	w.TagString("TCOT", text)
	return w.Bytes()
}

// handleLegal answers Authentication getTosInfo / getTermsOfServiceContent /
// getPrivacyPolicyContent.
func (s *Server) handleLegal(p *blaze.Packet, fields []blaze.Field) (*blaze.Packet, bool) {
	switch p.Command {
	case 0x002A: // getTosInfo
		return s.response(p, buildTosInfo()), true
	case 0x00F6, 0x002F: // getTermsOfServiceContent, getPrivacyPolicyContent
		kind, label := legalTOS, "Terms of Service"
		if p.Command == 0x002F {
			kind, label = legalPrivacy, "Privacy Policy"
		}
		lang, ctype := "", fieldUint(fields, "TEXT")
		for _, f := range fields {
			if v, ok := f.Value.(string); ok && f.Tag == "LANG" {
				lang = v
			}
		}
		text, ver := s.legalDoc(kind, lang)
		if ctype != 0 {
			s.log.Printf("[AUTH] %s asked as content type %d (1 = HTML); sending the plain text", label, ctype)
		}
		s.log.Printf("[AUTH] %s: %d bytes (version %s, LANG=%q)", label, len(text), ver, lang)
		return s.response(p, buildLegalDocContent(text, ver)), true
	}
	return nil, false
}

const defaultTOS = `NFS THE RUN - PRIVATE SERVER
TERMS OF USE

This is a private, community-run server that brings back the online
features of Need for Speed: The Run (Autolog, Speedwalls, playlists and
online races) after the official servers were shut down.

It is NOT operated by, endorsed by or affiliated with Electronic Arts.
Need for Speed and The Run belong to their owners. You need your own
copy of the game.

By playing here you agree to:

1. Play fair. No cheats, trainers, modified game data or exploits that
   give an advantage or spoil races for other players.
2. Be respectful. No harassment, hate speech or offensive persona
   names. The server owner may rename or remove such personas.
3. Keep your launcher key to yourself. Your account is tied to the key
   your launcher created; anyone with it can play as you.
4. Accept that this is a hobby project. The server is provided "as is",
   without any warranty. It may be offline, reset or shut down at any
   time, and progress, times and Speedwall entries may be lost.

The server owner may suspend players who break these rules.

Questions: contact the owner of this server.
`

const defaultPrivacy = `NFS THE RUN - PRIVATE SERVER
PRIVACY POLICY

What this server stores:

- Your persona name and account number.
- The public key of your launcher (used to log you in; your private
  key never leaves your computer).
- Your game progress as the game reports it: Autolog profile, badges,
  race times, split times, Speedwall entries and race results.
- Your friends list and the players you recently raced with.
- Technical logs: IP address, connection times and the game's network
  requests, used to run the server and fix problems. Logs are rotated
  and old logs are deleted.

What it does NOT do:

- It does not ask for your real name, e-mail, payment details or
  password.
- It does not sell or share your data. It is not connected to EA or
  Origin accounts.

Other players can see your persona name, online status, profile,
times and Speedwall entries, as in the original Autolog.

To have your data removed, contact the owner of this server.
`
