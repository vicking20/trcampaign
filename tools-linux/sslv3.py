"""Minimal SSLv3 server (RSA key exchange, RC4-128 with MD5 or SHA1 MAC).

Needed because EA's Blaze client only speaks SSLv3 + RC4, which modern TLS
libraries refuse. Pure Python, no dependencies. Not secure, by design: this is
a private-server shim for an old game, not a general TLS stack.
"""
import base64, hashlib, os, re, struct

MD5, SHA1 = hashlib.md5, hashlib.sha1
RAWLOG = True

# ---------- DER / key loading ----------
def _der_read(b, i):
    tag = b[i]; l = b[i + 1]; i += 2
    if l & 0x80:
        n = l & 0x7F; l = int.from_bytes(b[i:i + n], "big"); i += n
    return tag, b[i:i + l], i + l

def load_rsa_key(pem_path):
    pem = open(pem_path).read()
    der = base64.b64decode("".join(re.findall(r"^[A-Za-z0-9+/=]+$", pem, re.M)))
    _, body, _ = _der_read(der, 0)            # SEQUENCE
    i, ints = 0, []
    while i < len(body):
        _, v, i = _der_read(body, i)
        ints.append(int.from_bytes(v, "big"))
    return ints[1], ints[3]                   # n, d  (version,n,e,d,...)

def load_cert_der(pem_path):
    pem = open(pem_path).read()
    return base64.b64decode("".join(re.findall(r"^[A-Za-z0-9+/=]+$", pem, re.M)))

# ---------- primitives ----------
class RC4:
    def __init__(self, key):
        S = list(range(256)); j = 0
        for i in range(256):
            j = (j + S[i] + key[i % len(key)]) & 255
            S[i], S[j] = S[j], S[i]
        self.S, self.i, self.j = S, 0, 0

    def crypt(self, data):
        S, i, j = self.S, self.i, self.j
        out = bytearray(len(data))
        for n, c in enumerate(data):
            i = (i + 1) & 255
            j = (j + S[i]) & 255
            S[i], S[j] = S[j], S[i]
            out[n] = c ^ S[(S[i] + S[j]) & 255]
        self.i, self.j = i, j
        return bytes(out)

def _prf(secret, seed):
    out = b""
    for n, label in enumerate((b"A", b"BB", b"CCC", b"DDDD", b"EEEEE", b"FFFFFF")):
        out += MD5(secret + SHA1(label + secret + seed).digest()).digest()
    return out

def _mac(h, secret, seq, rtype, data):
    pad = 48 if h is MD5 else 40
    inner = h(secret + b"\x36" * pad + struct.pack(">QBH", seq, rtype, len(data)) + data).digest()
    return h(secret + b"\x5c" * pad + inner).digest()

class Conn:
    """Wraps a connected socket; call handshake() then recv()/send()."""
    def __init__(self, sock, key, cert_der, log=lambda *a: None):
        self.s, self.key, self.cert, self.log = sock, key, cert_der, log
        self.buf = b""
        self.hs = b""           # handshake transcript
        self.rd = self.wr = None  # RC4 states
        self.rseq = self.wseq = 0
        self.mac_h = SHA1
        self.hsbuf = b""
        self.pending_plain = b""

    # -- record layer --
    def _read_exact(self, n):
        while len(self.buf) < n:
            d = self.s.recv(4096)
            if not d:
                raise EOFError("peer closed")
            self.buf += d
        r, self.buf = self.buf[:n], self.buf[n:]
        return r

    def _read_record(self):
        hdr = self._read_exact(5)
        t, _ver, ln = struct.unpack(">BHH", hdr)
        body = self._read_exact(ln)
        if self.rd:
            body = self.rd.crypt(body)
            ms = self.mac_h().digest_size
            data, mac = body[:-ms], body[-ms:]
            if mac != _mac(self.mac_h, self.cmac, self.rseq, t, data):
                raise ValueError("bad record MAC")
            self.rseq += 1
            body = data
        return t, body

    def _send_record(self, t, data):
        if self.wr:
            data = data + _mac(self.mac_h, self.smac, self.wseq, t, data)
            data = self.wr.crypt(data)
            self.wseq += 1
        pkt = struct.pack(">BHH", t, 0x0300, len(data)) + data
        if RAWLOG: self.log("send record type", t, "len", len(data), pkt[:48].hex())
        self.s.sendall(pkt)

    def _hs_msg(self, t, body):
        m = struct.pack(">B", t) + len(body).to_bytes(3, "big") + body
        self.hs += m
        return m

    def _next_hs(self):
        """Return the next whole handshake message (plaintext phase only)."""
        while True:
            if len(self.hsbuf) >= 4:
                ln = int.from_bytes(self.hsbuf[1:4], "big")
                if len(self.hsbuf) >= 4 + ln:
                    m, self.hsbuf = self.hsbuf[:4 + ln], self.hsbuf[4 + ln:]
                    return m
            t, body = self._read_record()
            if t == 21:
                raise ValueError(f"alert from client: {body.hex()}")
            if t != 22:
                raise ValueError(f"unexpected record type {t}")
            self.hsbuf += body

    def _finished(self, sender, master):
        m = MD5(master + b"\x5c" * 48 + MD5(self.hs + sender + master + b"\x36" * 48).digest()).digest()
        s = SHA1(master + b"\x5c" * 40 + SHA1(self.hs + sender + master + b"\x36" * 40).digest()).digest()
        return m + s

    def handshake(self):
        n, d = self.key
        m = self._next_hs()
        if m[0] != 1:
            raise ValueError("expected ClientHello")
        self.hs += m
        b = m[4:]
        cr = b[2:34]
        p = 35 + b[34]
        cslen = struct.unpack(">H", b[p:p + 2])[0]
        suites = [struct.unpack(">H", b[p + 2 + i:p + 4 + i])[0] for i in range(0, cslen, 2)]
        self.log("client suites", [hex(x) for x in suites])
        if 0x0005 in suites:
            suite, self.mac_h = 0x0005, SHA1
        elif 0x0004 in suites:
            suite, self.mac_h = 0x0004, MD5
        else:
            raise ValueError("no RC4 suite offered")
        sr = os.urandom(32)
        out = self._hs_msg(2, struct.pack(">H", 0x0300) + sr + b"\x20" + os.urandom(32) +
                           struct.pack(">HB", suite, 0))
        certs = len(self.cert).to_bytes(3, "big") + self.cert
        out += self._hs_msg(11, len(certs).to_bytes(3, "big") + certs)
        out += self._hs_msg(14, b"")
        self._send_record(22, out)

        m = self._next_hs()
        if m[0] != 16:
            raise ValueError("expected ClientKeyExchange")
        self.hs += m
        enc = m[4:]
        k = (n.bit_length() + 7) // 8
        pt = pow(int.from_bytes(enc[-k:], "big"), d, n).to_bytes(k, "big")
        pms = pt[pt.index(b"\x00", 2) + 1:]
        master = _prf(pms, cr + sr)[:48]
        kb = _prf(master, sr + cr)
        ms = self.mac_h().digest_size
        self.cmac, self.smac = kb[:ms], kb[ms:2 * ms]
        ckey, skey = kb[2 * ms:2 * ms + 16], kb[2 * ms + 16:2 * ms + 32]

        # client: ChangeCipherSpec (plain) then Finished (encrypted)
        t, body = self._read_record()
        if t != 20:
            raise ValueError(f"expected ChangeCipherSpec, got {t}")
        self.rd = RC4(ckey)
        t, body = self._read_record()
        if t != 22 or body[0] != 20:
            raise ValueError("expected Finished")
        want = self._finished(struct.pack(">I", 0x434C4E54), master)
        if body[4:40] != want:
            raise ValueError("client Finished mismatch")
        self.hs += body[:40]

        # server: ChangeCipherSpec then Finished
        self._send_record(20, b"\x01")
        self.wr = RC4(skey)
        fin = self._finished(struct.pack(">I", 0x53525652), master)
        self._send_record(22, struct.pack(">B", 20) + (36).to_bytes(3, "big") + fin)
        self.log("handshake complete, suite", hex(suite))

    # -- application data --
    def recv(self, n=65536):
        """Return decrypted application data, b'' on close."""
        while True:
            try:
                t, body = self._read_record()
            except EOFError:
                return b""
            if t == 23:
                return body
            if t == 21:
                return b""
            # ignore anything else

    def send(self, data):
        for i in range(0, len(data), 16000):
            self._send_record(23, data[i:i + 16000])
