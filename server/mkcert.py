#!/usr/bin/env python3
"""Developer tool: build a minimal X.509 v1 certificate (no extensions,
sha1WithRSA, 1024-bit) for server/key.pem, written to server/cert.pem."""
import base64, hashlib, re, sys, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
HERE = os.path.dirname(os.path.abspath(__file__))

def der(tag, body): 
    n = len(body)
    L = bytes([n]) if n < 128 else (bytes([0x81, n]) if n < 256 else bytes([0x82, n >> 8, n & 255]))
    return bytes([tag]) + L + body
def integer(v):
    b = v.to_bytes((v.bit_length() + 8) // 8, "big")
    return der(0x02, b)
def oid(s):
    p = [int(x) for x in s.split(".")]
    out = bytes([40 * p[0] + p[1]])
    for x in p[2:]:
        t = [x & 0x7F]; x >>= 7
        while x: t.insert(0, 0x80 | (x & 0x7F)); x >>= 7
        out += bytes(t)
    return der(0x06, out)
def name(cn, org):
    def rdn(o, v): return der(0x31, der(0x30, oid(o) + der(0x13, v.encode())))
    return der(0x30, rdn("2.5.4.10", org) + rdn("2.5.4.3", cn))

pem = open(os.path.join(HERE, "key.pem")).read()
raw = base64.b64decode("".join(re.findall(r"^[A-Za-z0-9+/=]+$", pem, re.M)))
def rd(b, i):
    l = b[i + 1]; i += 2
    if l & 0x80:
        n = l & 0x7F; l = int.from_bytes(b[i:i + n], "big"); i += n
    return b[i:i + l], i + l
body, _ = rd(raw, 0); i = 0; ints = []
while i < len(body):
    v, i = rd(body, i); ints.append(int.from_bytes(v, "big"))
n, e, d = ints[1], ints[2], ints[3]

sigalg = der(0x30, oid("1.2.840.113549.1.1.5") + der(0x05, b""))
validity = der(0x30, der(0x17, b"100101000000Z") + der(0x17, b"491231235959Z"))
rsakey = der(0x30, integer(n) + integer(e))
spki = der(0x30, der(0x30, oid("1.2.840.113549.1.1.1") + der(0x05, b"")) + der(0x03, b"\x00" + rsakey))
nm = name("gosredirector.ea.com", "TRCampaign local server")
tbs = der(0x30, integer(1) + sigalg + nm + validity + nm + spki)
dig = hashlib.sha1(tbs).digest()
di = der(0x30, der(0x30, oid("1.3.14.3.2.26") + der(0x05, b"")) + der(0x04, dig))
k = (n.bit_length() + 7) // 8
em = b"\x00\x01" + b"\xff" * (k - len(di) - 3) + b"\x00" + di
sig = pow(int.from_bytes(em, "big"), d, n).to_bytes(k, "big")
cert = der(0x30, tbs + sigalg + der(0x03, b"\x00" + sig))
b64 = base64.encodebytes(cert).decode()
open(os.path.join(HERE, "cert.pem"), "w").write("-----BEGIN CERTIFICATE-----\n" + b64 + "-----END CERTIFICATE-----\n")
print("wrote cert.pem", len(cert), "bytes")

