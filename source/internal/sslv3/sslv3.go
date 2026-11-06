// Package sslv3 is a minimal SSLv3 server (RSA key exchange, RC4-128 with MD5
// or SHA-1 MAC), a port of the old Python shim. The game's 2011 ProtoSSL layer
// speaks nothing else and Go's crypto/tls dropped SSLv3. Not secure by design:
// it is a private-server shim for an old game, not a general TLS stack.
package sslv3

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"time"
)

// Creds is the server key and certificate (DER, sent as-is: the game accepts
// a certificate that modern parsers reject, so it is never parsed).
type Creds struct {
	Key  *rsa.PrivateKey
	Cert []byte
}

// LoadCreds reads a PEM certificate and RSA private key.
func LoadCreds(certPath, keyPath string) (*Creds, error) {
	cp, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	kp, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(cp)
	kb, _ := pem.Decode(kp)
	if cb == nil || kb == nil {
		return nil, errors.New("sslv3: certificate or key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(kb.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err == nil {
		k, ok := k8.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("sslv3: key is not RSA")
		}
		key = k
	} else {
		return nil, errors.New("sslv3: cannot read the RSA key")
	}
	return &Creds{Key: key, Cert: cb.Bytes}, nil
}

func prf(secret, seed []byte) []byte {
	var out []byte
	for _, label := range []string{"A", "BB", "CCC", "DDDD", "EEEEE", "FFFFFF"} {
		s := sha1.Sum(append(append([]byte(label), secret...), seed...))
		m := md5.Sum(append(append([]byte(nil), secret...), s[:]...))
		out = append(out, m[:]...)
	}
	return out
}

func mac(h func() hash.Hash, secret []byte, seq uint64, typ byte, data []byte) []byte {
	pad := 40
	if h().Size() == md5.Size {
		pad = 48
	}
	var hdr [11]byte
	binary.BigEndian.PutUint64(hdr[0:], seq)
	hdr[8] = typ
	binary.BigEndian.PutUint16(hdr[9:], uint16(len(data)))
	in := h()
	in.Write(secret)
	in.Write(repeat(0x36, pad))
	in.Write(hdr[:])
	in.Write(data)
	out := h()
	out.Write(secret)
	out.Write(repeat(0x5c, pad))
	out.Write(in.Sum(nil))
	return out.Sum(nil)
}

func repeat(b byte, n int) []byte {
	r := make([]byte, n)
	for i := range r {
		r[i] = b
	}
	return r
}

// Conn is a server-side SSLv3 connection. Handshake runs on first use.
type Conn struct {
	net.Conn
	creds *Creds

	buf    []byte // raw bytes read, not yet parsed
	hs     []byte // handshake transcript
	hsbuf  []byte
	rd, wr *rc4.Cipher
	rseq   uint64
	wseq   uint64
	macH   func() hash.Hash
	cmac   []byte
	smac   []byte
	done   bool
	app    []byte // decrypted application data not yet returned
	eof    bool
}

// Server wraps an accepted connection.
func Server(c net.Conn, creds *Creds) *Conn { return &Conn{Conn: c, creds: creds} }

func (c *Conn) readExact(n int) ([]byte, error) {
	for len(c.buf) < n {
		tmp := make([]byte, 4096)
		k, err := c.Conn.Read(tmp)
		c.buf = append(c.buf, tmp[:k]...)
		if err != nil && len(c.buf) < n {
			return nil, err
		}
	}
	r := c.buf[:n:n]
	c.buf = c.buf[n:]
	return r, nil
}

func (c *Conn) readRecord() (byte, []byte, error) {
	hdr, err := c.readExact(5)
	if err != nil {
		return 0, nil, err
	}
	t := hdr[0]
	ln := int(binary.BigEndian.Uint16(hdr[3:5]))
	body, err := c.readExact(ln)
	if err != nil {
		return 0, nil, err
	}
	if c.rd != nil {
		pt := make([]byte, len(body))
		c.rd.XORKeyStream(pt, body)
		ms := c.macH().Size()
		if len(pt) < ms {
			return 0, nil, errors.New("sslv3: short record")
		}
		data, m := pt[:len(pt)-ms], pt[len(pt)-ms:]
		if subtle.ConstantTimeCompare(m, mac(c.macH, c.cmac, c.rseq, t, data)) != 1 {
			return 0, nil, errors.New("sslv3: bad record MAC")
		}
		c.rseq++
		body = data
	}
	return t, body, nil
}

func (c *Conn) sendRecord(t byte, data []byte) error {
	if c.wr != nil {
		data = append(append([]byte(nil), data...), mac(c.macH, c.smac, c.wseq, t, data)...)
		enc := make([]byte, len(data))
		c.wr.XORKeyStream(enc, data)
		data = enc
		c.wseq++
	}
	pkt := []byte{t, 3, 0, byte(len(data) >> 8), byte(len(data))}
	_, err := c.Conn.Write(append(pkt, data...))
	return err
}

func (c *Conn) hsMsg(t byte, body []byte) []byte {
	m := append([]byte{t, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	c.hs = append(c.hs, m...)
	return m
}

func (c *Conn) nextHS() ([]byte, error) {
	for {
		if len(c.hsbuf) >= 4 {
			ln := int(c.hsbuf[1])<<16 | int(c.hsbuf[2])<<8 | int(c.hsbuf[3])
			if len(c.hsbuf) >= 4+ln {
				m := c.hsbuf[:4+ln]
				c.hsbuf = c.hsbuf[4+ln:]
				return m, nil
			}
		}
		t, body, err := c.readRecord()
		if err != nil {
			return nil, err
		}
		if t == 21 {
			return nil, fmt.Errorf("sslv3: alert from client %x", body)
		}
		if t != 22 {
			return nil, fmt.Errorf("sslv3: unexpected record type %d", t)
		}
		c.hsbuf = append(c.hsbuf, body...)
	}
}

func (c *Conn) finished(sender, master []byte) []byte {
	inner := func(h func() hash.Hash, pad int) []byte {
		x := h()
		x.Write(c.hs)
		x.Write(sender)
		x.Write(master)
		x.Write(repeat(0x36, pad))
		o := h()
		o.Write(master)
		o.Write(repeat(0x5c, pad))
		o.Write(x.Sum(nil))
		return o.Sum(nil)
	}
	return append(inner(md5.New, 48), inner(sha1.New, 40)...)
}

// Handshake runs the SSLv3 handshake.
func (c *Conn) Handshake() error {
	if c.done {
		return nil
	}
	m, err := c.nextHS()
	if err != nil {
		return err
	}
	if m[0] != 1 {
		return errors.New("sslv3: expected ClientHello")
	}
	c.hs = append(c.hs, m...)
	b := m[4:]
	if len(b) < 36 {
		return errors.New("sslv3: short ClientHello")
	}
	cr := b[2:34]
	p := 35 + int(b[34])
	if len(b) < p+2 {
		return errors.New("sslv3: short ClientHello")
	}
	cslen := int(binary.BigEndian.Uint16(b[p:]))
	if len(b) < p+2+cslen {
		return errors.New("sslv3: short ClientHello")
	}
	has := map[uint16]bool{}
	for i := 0; i+1 < cslen; i += 2 {
		has[binary.BigEndian.Uint16(b[p+2+i:])] = true
	}
	var suite uint16
	switch {
	case has[0x0005]:
		suite, c.macH = 0x0005, sha1.New
	case has[0x0004]:
		suite, c.macH = 0x0004, md5.New
	default:
		return errors.New("sslv3: no RC4 suite offered")
	}
	sr := make([]byte, 32)
	rand.Read(sr)
	sid := make([]byte, 32)
	rand.Read(sid)
	sh := append([]byte{3, 0}, sr...)
	sh = append(sh, 0x20)
	sh = append(sh, sid...)
	sh = append(sh, byte(suite>>8), byte(suite), 0)
	out := c.hsMsg(2, sh)
	certs := append([]byte{byte(len(c.creds.Cert) >> 16), byte(len(c.creds.Cert) >> 8), byte(len(c.creds.Cert))}, c.creds.Cert...)
	out = append(out, c.hsMsg(11, append([]byte{byte(len(certs) >> 16), byte(len(certs) >> 8), byte(len(certs))}, certs...))...)
	out = append(out, c.hsMsg(14, nil)...)
	if err := c.sendRecord(22, out); err != nil {
		return err
	}

	m, err = c.nextHS()
	if err != nil {
		return err
	}
	if m[0] != 16 {
		return errors.New("sslv3: expected ClientKeyExchange")
	}
	c.hs = append(c.hs, m...)
	enc := m[4:]
	k := (c.creds.Key.N.BitLen() + 7) / 8
	if len(enc) < k {
		return errors.New("sslv3: short ClientKeyExchange")
	}
	pms, err := rsa.DecryptPKCS1v15(nil, c.creds.Key, enc[len(enc)-k:])
	if err != nil {
		return fmt.Errorf("sslv3: key exchange: %w", err)
	}
	master := prf(pms, append(append([]byte(nil), cr...), sr...))[:48]
	kb := prf(master, append(append([]byte(nil), sr...), cr...))
	ms := c.macH().Size()
	c.cmac, c.smac = kb[:ms], kb[ms:2*ms]
	ckey, skey := kb[2*ms:2*ms+16], kb[2*ms+16:2*ms+32]

	t, _, err := c.readRecord()
	if err != nil {
		return err
	}
	if t != 20 {
		return fmt.Errorf("sslv3: expected ChangeCipherSpec, got %d", t)
	}
	if c.rd, err = rc4.NewCipher(ckey); err != nil {
		return err
	}
	t, body, err := c.readRecord()
	if err != nil {
		return err
	}
	if t != 22 || len(body) < 40 || body[0] != 20 {
		return errors.New("sslv3: expected Finished")
	}
	if subtle.ConstantTimeCompare(body[4:40], c.finished([]byte{0x43, 0x4c, 0x4e, 0x54}, master)) != 1 {
		return errors.New("sslv3: client Finished mismatch")
	}
	c.hs = append(c.hs, body[:40]...)

	if err := c.sendRecord(20, []byte{1}); err != nil {
		return err
	}
	if c.wr, err = rc4.NewCipher(skey); err != nil {
		return err
	}
	fin := c.finished([]byte{0x53, 0x52, 0x56, 0x52}, master)
	if err := c.sendRecord(22, append([]byte{20, 0, 0, 36}, fin...)); err != nil {
		return err
	}
	c.done = true
	return nil
}

// Read returns decrypted application data.
func (c *Conn) Read(p []byte) (int, error) {
	if err := c.Handshake(); err != nil {
		return 0, err
	}
	for len(c.app) == 0 {
		if c.eof {
			return 0, io.EOF
		}
		t, body, err := c.readRecord()
		if err != nil {
			return 0, err
		}
		switch t {
		case 23:
			c.app = body
		case 21:
			c.eof = true
		}
	}
	n := copy(p, c.app)
	c.app = c.app[n:]
	return n, nil
}

// Write sends application data.
func (c *Conn) Write(p []byte) (int, error) {
	if err := c.Handshake(); err != nil {
		return 0, err
	}
	for i := 0; i < len(p); i += 16000 {
		j := min(i+16000, len(p))
		if err := c.sendRecord(23, p[i:j]); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

// Gateway terminates SSLv3 on listen and relays plaintext to backend.
func Gateway(creds *Creds, listen, backend string, logf func(string, ...any)) (net.Listener, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go relay(raw, creds, backend, logf)
		}
	}()
	return ln, nil
}

func relay(raw net.Conn, creds *Creds, backend string, logf func(string, ...any)) {
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(30 * time.Second))
	c := Server(raw, creds)
	if err := c.Handshake(); err != nil {
		logf("[SSLV3] %s: %v", raw.RemoteAddr(), err)
		return
	}
	raw.SetDeadline(time.Time{})
	b, err := net.DialTimeout("tcp", backend, 10*time.Second)
	if err != nil {
		logf("[SSLV3] %s: backend %s: %v", raw.RemoteAddr(), backend, err)
		return
	}
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { copyConn(b, c); done <- struct{}{} }()
	go func() { copyConn(c, b); done <- struct{}{} }()
	<-done
}

func copyConn(dst, src net.Conn) {
	buf := make([]byte, 32768)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
