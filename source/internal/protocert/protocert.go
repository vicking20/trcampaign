// Package protocert makes and checks the Redirector certificate the game's
// ProtoSSL accepts.
//
// The client requires the "OTG3 Certificate Authority" issuer DN and a CN that
// matches the Redirector host (wildcards allowed). Its signature check is
// skipped when the outer AlgorithmIdentifier is rsaEncryption while the TBS
// one stays md5WithRSAEncryption (see Aim4kill/Bug_OldProtoSSL). The
// certificate is built here directly: issuer OTG3, CN "*.<domain>", X.509 v1,
// RSA 1024, MD5.
package protocert

import (
	"bytes"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
)

var (
	oidMD5WithRSA    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 4}
	oidRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}

	oidCN = asn1.ObjectIdentifier{2, 5, 4, 3}
	oidC  = asn1.ObjectIdentifier{2, 5, 4, 6}
	oidL  = asn1.ObjectIdentifier{2, 5, 4, 7}
	oidST = asn1.ObjectIdentifier{2, 5, 4, 8}
	oidO  = asn1.ObjectIdentifier{2, 5, 4, 10}
	oidOU = asn1.ObjectIdentifier{2, 5, 4, 11}
)

// IssuerCN is what the client compares the issuer with.
const (
	IssuerCN = "OTG3 Certificate Authority"
	IssuerO  = "Electronic Arts, Inc."
)

type attr struct {
	oid asn1.ObjectIdentifier
	val string
}

// OpenSSL -subj order; C is a PrintableString, the rest UTF8String.
var issuerDN = []attr{{oidOU, "Online Technology Group"}, {oidO, IssuerO}, {oidL, "Redwood City"}, {oidST, "California"}, {oidC, "US"}, {oidCN, IssuerCN}}

func subjectDN(cn string) []attr {
	return []attr{{oidCN, cn}, {oidOU, "Global Online Studio"}, {oidO, IssuerO}, {oidST, "California"}, {oidC, "US"}}
}

func mustMarshal(v any, params ...string) []byte {
	var b []byte
	var err error
	if len(params) > 0 {
		b, err = asn1.MarshalWithParams(v, params[0])
	} else {
		b, err = asn1.Marshal(v)
	}
	if err != nil {
		panic(err)
	}
	return b
}

func seq(parts ...[]byte) []byte {
	return mustMarshal(asn1.RawValue{Tag: asn1.TagSequence, Class: asn1.ClassUniversal, IsCompound: true, Bytes: bytes.Join(parts, nil)})
}

func set(parts ...[]byte) []byte {
	return mustMarshal(asn1.RawValue{Tag: asn1.TagSet, Class: asn1.ClassUniversal, IsCompound: true, Bytes: bytes.Join(parts, nil)})
}

func name(attrs []attr) []byte {
	var rdns [][]byte
	for _, a := range attrs {
		tag := asn1.TagUTF8String
		if a.oid.Equal(oidC) {
			tag = asn1.TagPrintableString
		}
		val := mustMarshal(asn1.RawValue{Tag: tag, Class: asn1.ClassUniversal, Bytes: []byte(a.val)})
		rdns = append(rdns, set(seq(mustMarshal(a.oid), val)))
	}
	return seq(rdns...)
}

func algID(oid asn1.ObjectIdentifier) []byte {
	return seq(mustMarshal(oid), mustMarshal(asn1.RawValue{Tag: asn1.TagNull, Class: asn1.ClassUniversal}))
}

func timeDER(t time.Time) []byte {
	t = t.UTC()
	if t.Year() >= 2050 {
		return mustMarshal(t, "generalized")
	}
	return mustMarshal(t, "utc")
}

// Generate returns a leaf certificate (PEM, outer OID patched) for
// "*.<domain>" and its RSA private key (PEM, PKCS#1 like "openssl genrsa").
func Generate(domain string, now time.Time) (certPEM, keyPEM []byte, err error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !validDomain(domain) {
		return nil, nil, fmt.Errorf("invalid base domain %q", domain)
	}
	caKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, nil, err
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, nil, err
	}
	serialBytes := make([]byte, 16)
	if _, err = rand.Read(serialBytes); err != nil {
		return nil, nil, err
	}
	serialBytes[0] &= 0x7f
	serialBytes[0] |= 0x40 // positive, fixed length
	serial := new(big.Int).SetBytes(serialBytes)
	spki, err := x509.MarshalPKIXPublicKey(&leafKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	notBefore := now.Add(-48 * time.Hour).Truncate(time.Second) // tolerate a client clock that is behind
	notAfter := now.Add(10000 * 24 * time.Hour).Truncate(time.Second)
	// TBSCertificate, version 1 (no extensions), like "openssl x509 -req".
	tbs := seq(
		mustMarshal(serial),
		algID(oidMD5WithRSA),
		name(issuerDN),
		seq(timeDER(notBefore), timeDER(notAfter)),
		name(subjectDN("*."+domain)),
		spki,
	)
	sum := md5.Sum(tbs)
	sig, err := rsa.SignPKCS1v15(rand.Reader, caKey, crypto.MD5, sum[:])
	if err != nil {
		return nil, nil, err
	}
	der := seq(tbs, algID(oidRSAEncryption), mustMarshal(asn1.BitString{Bytes: sig, BitLength: len(sig) * 8}))
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	return certPEM, keyPEM, nil
}

func validDomain(d string) bool {
	if len(d) < 3 || !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return false
	}
	for _, c := range d {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// Info is what Check found in a certificate.
type Info struct {
	Issuer, Subject string
	IssuerOK        bool
	SubjectCN       string
	InnerAlg        string
	OuterPatched    bool
	NotBefore       time.Time
	NotAfter        time.Time
	KeyMatches      bool
}

type tbsCert struct {
	Version   int `asn1:"optional,explicit,default:0,tag:0"`
	Serial    *big.Int
	SigAlg    pkix.AlgorithmIdentifier
	Issuer    asn1.RawValue
	Validity  struct{ NotBefore, NotAfter time.Time }
	Subject   asn1.RawValue
	PublicKey asn1.RawValue
	Rest      asn1.RawValue `asn1:"optional"`
}

type rawCert struct {
	TBS    asn1.RawValue
	SigAlg pkix.AlgorithmIdentifier
	Sig    asn1.BitString
}

func parseName(raw []byte) (pkix.Name, string, error) {
	var rdn pkix.RDNSequence
	if _, err := asn1.Unmarshal(raw, &rdn); err != nil {
		return pkix.Name{}, "", err
	}
	var n pkix.Name
	n.FillFromRDNSequence(&rdn)
	return n, rdn.String(), nil
}

// Inspect parses the certificate/key pair without trusting Go's x509 parser
// (it refuses the inner/outer algorithm mismatch on purpose).
func Inspect(certPEM, keyPEM []byte) (Info, error) {
	var info Info
	blk, _ := pem.Decode(certPEM)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return info, errors.New("no PEM CERTIFICATE block")
	}
	var rc rawCert
	if _, err := asn1.Unmarshal(blk.Bytes, &rc); err != nil {
		return info, fmt.Errorf("certificate DER: %w", err)
	}
	var tbs tbsCert
	if _, err := asn1.Unmarshal(rc.TBS.FullBytes, &tbs); err != nil {
		return info, fmt.Errorf("certificate TBS: %w", err)
	}
	iss, issStr, err := parseName(tbs.Issuer.FullBytes)
	if err != nil {
		return info, fmt.Errorf("issuer: %w", err)
	}
	sub, subStr, err := parseName(tbs.Subject.FullBytes)
	if err != nil {
		return info, fmt.Errorf("subject: %w", err)
	}
	info.Issuer, info.Subject, info.SubjectCN = issStr, subStr, sub.CommonName
	info.IssuerOK = iss.CommonName == IssuerCN && len(iss.Organization) > 0 && iss.Organization[0] == IssuerO
	info.InnerAlg = tbs.SigAlg.Algorithm.String()
	info.OuterPatched = rc.SigAlg.Algorithm.Equal(oidRSAEncryption) && tbs.SigAlg.Algorithm.Equal(oidMD5WithRSA)
	info.NotBefore, info.NotAfter = tbs.Validity.NotBefore, tbs.Validity.NotAfter
	pub, err := x509.ParsePKIXPublicKey(tbs.PublicKey.FullBytes)
	if err != nil {
		return info, fmt.Errorf("public key: %w", err)
	}
	if kb, _ := pem.Decode(keyPEM); kb != nil {
		var priv any
		if k, e := x509.ParsePKCS1PrivateKey(kb.Bytes); e == nil {
			priv = k
		} else if k, e := x509.ParsePKCS8PrivateKey(kb.Bytes); e == nil {
			priv = k
		}
		if rk, ok := priv.(*rsa.PrivateKey); ok {
			if rp, ok := pub.(*rsa.PublicKey); ok {
				info.KeyMatches = rk.PublicKey.N.Cmp(rp.N) == 0 && rk.PublicKey.E == rp.E
			}
		}
	}
	return info, nil
}

// Problems lists why the certificate would be refused by the client for the
// given Redirector hosts (empty = fine).
func Problems(certPEM, keyPEM []byte, hosts []string, now time.Time) []string {
	info, err := Inspect(certPEM, keyPEM)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	if !info.IssuerOK {
		out = append(out, fmt.Sprintf("issuer is %q, the game only accepts CN=%s, O=%s", info.Issuer, IssuerCN, IssuerO))
	}
	if !info.OuterPatched {
		out = append(out, fmt.Sprintf("signature algorithms not in the Old-ProtoSSL form (inner %s, outer must be rsaEncryption)", info.InnerAlg))
	}
	if !info.KeyMatches {
		out = append(out, "the private key does not belong to this certificate")
	}
	if now.Before(info.NotBefore) || now.After(info.NotAfter) {
		out = append(out, fmt.Sprintf("valid %s .. %s only", info.NotBefore.Format(time.RFC3339), info.NotAfter.Format(time.RFC3339)))
	}
	for _, h := range hosts {
		if !MatchesHost(info.SubjectCN, h) {
			out = append(out, fmt.Sprintf("CN=%q does not cover %s", info.SubjectCN, h))
		}
	}
	return out
}

// MatchesHost: exact match or a one-label wildcard ("*.example.org" covers
// "nfs.example.org", not "a.b.example.org").
func MatchesHost(cn, host string) bool {
	cn, host = strings.ToLower(cn), strings.ToLower(strings.TrimSuffix(host, "."))
	if cn == host {
		return true
	}
	if strings.HasPrefix(cn, "*.") {
		suffix := cn[1:]
		if strings.HasSuffix(host, suffix) {
			label := strings.TrimSuffix(host, suffix)
			return label != "" && !strings.Contains(label, ".")
		}
	}
	return false
}

// ---------------------------------------------------------------- game exe

// RedirectorSlots: the four Redirector hosts in the PC 1.1.0.0 exe as
// tools\patch_client.py writes them (file offset, slot size incl. NUL, the
// prefix of the patched host, the retail value).
var RedirectorSlots = []struct {
	Offset int
	Size   int
	Prefix string
	Retail string
}{
	{0x02234EA0, 21, "nfs.", "gosredirector.ea.com"},
	{0x02234EB8, 27, "scert-nfs.", "gosredirector.scert.ea.com"},
	{0x02234ED4, 27, "stest-nfs.", "gosredirector.stest.ea.com"},
	{0x02234EF0, 28, "redirector.", "gosredirector.online.ea.com"},
}

// ExeHosts reads the Redirector hosts of a patched game exe and their base
// domain. ErrRetailExe: the exe still points to EA's Redirector.
var ErrRetailExe = errors.New("still has EA's Redirector hosts: patch it with tools\\patch_client.bat first")

func ExeHosts(exePath string) (hosts []string, domain string, err error) {
	f, err := os.Open(exePath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	if err := checkPE(f); err != nil {
		return nil, "", fmt.Errorf("%s: %w", exePath, err)
	}
	for _, s := range RedirectorSlots {
		buf := make([]byte, s.Size)
		if _, err := f.ReadAt(buf, int64(s.Offset)); err != nil {
			return nil, "", fmt.Errorf("%s: reading the Redirector slot at 0x%08X: %w", exePath, s.Offset, err)
		}
		h := string(bytes.TrimRight(buf, "\x00"))
		if i := strings.IndexByte(h, 0); i >= 0 {
			h = h[:i]
		}
		switch {
		case h == s.Retail:
			return nil, "", fmt.Errorf("%s (%s): %w", exePath, h, ErrRetailExe)
		case !strings.HasPrefix(h, s.Prefix):
			return nil, "", fmt.Errorf("%s: unexpected Redirector host %q at 0x%08X (not a PC 1.1.0.0 exe patched by tools\\patch_client.py?)", exePath, h, s.Offset)
		}
		d := strings.TrimPrefix(h, s.Prefix)
		if !validDomain(d) {
			return nil, "", fmt.Errorf("%s: Redirector host %q has no valid domain", exePath, h)
		}
		if domain == "" {
			domain = d
		} else if d != domain {
			return nil, "", fmt.Errorf("%s: the Redirector hosts use two domains (%s, %s)", exePath, domain, d)
		}
		hosts = append(hosts, h)
	}
	return hosts, domain, nil
}

func checkPE(f *os.File) error {
	hdr := make([]byte, 0x40)
	if _, err := f.ReadAt(hdr, 0); err != nil || hdr[0] != 'M' || hdr[1] != 'Z' {
		return errors.New("not a Windows executable")
	}
	peOff := int64(binary.LittleEndian.Uint32(hdr[0x3c:]))
	sig := make([]byte, 4)
	if _, err := f.ReadAt(sig, peOff); err != nil || string(sig) != "PE\x00\x00" {
		return errors.New("not a Windows executable")
	}
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() < int64(RedirectorSlots[3].Offset+RedirectorSlots[3].Size) {
		return errors.New("too small to be NFS The Run 1.1.0.0")
	}
	return nil
}

// HostsFor: the four Redirector hosts of a base domain (same templates).
func HostsFor(domain string) []string {
	var out []string
	for _, s := range RedirectorSlots {
		out = append(out, s.Prefix+domain)
	}
	return out
}

// PatchRedirector points the four Redirector hosts of an exe image (retail or
// patched before) at domain, in place.
func PatchRedirector(d []byte, domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !validDomain(domain) {
		return fmt.Errorf("invalid domain %q", domain)
	}
	for _, s := range RedirectorSlots {
		host := s.Prefix + domain
		if len(host) >= s.Size {
			return fmt.Errorf("domain %q is too long for the game's Redirector slot (%s at most %d characters)", domain, s.Prefix, s.Size-1)
		}
		if s.Offset+s.Size > len(d) {
			return fmt.Errorf("Redirector slot 0x%08X is not in the exe", s.Offset)
		}
		cur := string(d[s.Offset : s.Offset+s.Size])
		if i := strings.IndexByte(cur, 0); i >= 0 {
			cur = cur[:i]
		}
		if cur != s.Retail && !strings.HasPrefix(cur, s.Prefix) {
			return fmt.Errorf("unexpected Redirector host %q at 0x%08X: not NFS The Run PC 1.1.0.0", cur, s.Offset)
		}
		slot := make([]byte, s.Size)
		copy(slot, host)
		copy(d[s.Offset:], slot)
	}
	return nil
}
