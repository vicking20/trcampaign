"""Blaze packet + TDF codec (enough for NFS The Run's client, Blaze 3.15).

Frame: u16 length | u16 component | u16 command | u16 error | u16 type|id ...
(12-byte header). type: 0x0000 message, 0x1000 reply, 0x2000 notification,
0x3000 error reply. TDF tags are 4 chars packed 6 bits each into 3 bytes.
"""
import struct

T_INT, T_STR, T_BLOB, T_STRUCT, T_LIST, T_MAP, T_UNION, T_INTLIST, T_OBJTYPE, T_OBJID, T_FLOAT, T_TIME = range(12)

# ------------------------------------------------------------ tags
def tag_encode(t):
    t = t.upper().ljust(4)
    v = 0
    for c in t[:4]:
        n = 0 if c == " " else ((ord(c) & 0x1F) | 0x20)
        v = (v << 6) | n
    return v.to_bytes(3, "big")

def tag_decode(b):
    v = int.from_bytes(b, "big")
    out = ""
    for sh in (18, 12, 6, 0):
        n = (v >> sh) & 0x3F
        out += " " if n == 0 else chr((n & 0x1F) | 0x40)
    return out.strip()

# ------------------------------------------------------------ varints
def enc_int(v):
    neg = v < 0
    v = -v if neg else v
    first = v & 0x3F
    v >>= 6
    out = bytearray()
    b0 = first | (0x40 if neg else 0)
    if v:
        out.append(b0 | 0x80)
        while True:
            b = v & 0x7F
            v >>= 7
            if v:
                out.append(b | 0x80)
            else:
                out.append(b); break
    else:
        out.append(b0)
    return bytes(out)

def dec_int(b, i):
    c = b[i]; i += 1
    v = c & 0x3F
    neg = bool(c & 0x40)
    sh = 6
    while c & 0x80:
        c = b[i]; i += 1
        v |= (c & 0x7F) << sh
        sh += 7
    return (-v if neg else v), i

# ------------------------------------------------------------ values
class Struct(dict): pass
class Union:
    def __init__(self, member, name=None, value=None):
        self.member, self.name, self.value = member, name, value
    def __repr__(self): return f"Union({self.member},{self.name}={self.value!r})"

def _enc_value(tp, v, sub=None):
    if tp == T_INT: return enc_int(v)
    if tp == T_STR:
        d = v.encode() + b"\0"; return enc_int(len(d)) + d
    if tp == T_BLOB: return enc_int(len(v)) + v
    if tp == T_STRUCT: return _enc_struct(v)
    if tp == T_FLOAT: return struct.pack(">f", v)
    if tp == T_TIME: return enc_int(v)
    if tp == T_LIST:
        st, items = v
        out = bytes([st]) + enc_int(len(items))
        for it in items:
            out += bytes(it) if isinstance(it, (bytes, bytearray)) else (_enc_struct(it) if st == T_STRUCT else _enc_value(st, it))
        return out
    if tp == T_INTLIST:
        return enc_int(len(v)) + b"".join(enc_int(x) for x in v)
    if tp == T_UNION:
        if v.name is None: return b"\x7f"
        t, val = v.value
        return bytes([v.member]) + tag_encode(v.name) + bytes([t]) + _enc_value(t, val)
    if tp == T_MAP:
        kt, vt, d = v
        out = bytes([kt, vt]) + enc_int(len(d))
        for k, x in d.items():
            out += _enc_value(kt, k)
            out += _enc_struct(x) if vt == T_STRUCT else _enc_value(vt, x)
        return out
    if tp == T_OBJTYPE: return enc_int(v[0]) + enc_int(v[1])
    if tp == T_OBJID: return enc_int(v[0]) + enc_int(v[1]) + enc_int(v[2])
    raise ValueError(tp)

def _enc_struct(fields, root=False):
    """fields: list of (tag, type, value[, ]) tuples."""
    out = b""
    for f in fields:
        t, tp, v = f[0], f[1], f[2]
        out += tag_encode(t) + bytes([tp]) + _enc_value(tp, v)
    return out if root else out + b"\0"

def encode(fields):
    return _enc_struct(fields, root=True)

def _dec_value(tp, b, i):
    if tp in (T_INT, T_TIME): return dec_int(b, i)
    if tp == T_STR:
        n, i = dec_int(b, i); return b[i:i + n - 1].decode("utf-8", "replace"), i + n
    if tp == T_BLOB:
        n, i = dec_int(b, i); return bytes(b[i:i + n]), i + n
    if tp == T_STRUCT: return _dec_struct(b, i)
    if tp == T_FLOAT: return struct.unpack(">f", b[i:i + 4])[0], i + 4
    if tp == T_LIST:
        st = b[i]; i += 1
        n, i = dec_int(b, i); out = []
        for _ in range(n):
            # Blaze 3.15 NetworkAddress lists advertise STRUCT elements but
            # prefix each IP-pair struct with the union discriminator (2).
            # The dedicated server sends this shape in createGame.HNET.
            if st == T_STRUCT and b[i] == 2:
                i += 1
            x, i = _dec_value(st, b, i); out.append(x)
        return (st, out), i
    if tp == T_INTLIST:
        n, i = dec_int(b, i); out = []
        for _ in range(n):
            x, i = dec_int(b, i); out.append(x)
        return out, i
    if tp == T_UNION:
        m = b[i]; i += 1
        if m == 0x7F: return Union(m), i
        name = tag_decode(b[i:i + 3]); t = b[i + 3]; i += 4
        val, i = _dec_value(t, b, i)
        return Union(m, name, (t, val)), i
    if tp == T_MAP:
        kt, vt = b[i], b[i + 1]; i += 2
        n, i = dec_int(b, i); out = {}
        for _ in range(n):
            k, i = _dec_value(kt, b, i); x, i = _dec_value(vt, b, i); out[k] = x
        return (kt, vt, out), i
    if tp == T_OBJTYPE:
        a, i = dec_int(b, i); c, i = dec_int(b, i); return (a, c), i
    if tp == T_OBJID:
        a, i = dec_int(b, i); c, i = dec_int(b, i); d, i = dec_int(b, i); return (a, c, d), i
    raise ValueError(f"unknown tdf type {tp}")

def _dec_struct(b, i, root=False):
    out = Struct()
    while i < len(b):
        if not root and b[i] == 0:
            return out, i + 1
        name = tag_decode(b[i:i + 3]); tp = b[i + 3]; i += 4
        out[name], i = _dec_value(tp, b, i)
    return out, i

def decode(b):
    s, _ = _dec_struct(b, 0, root=True)
    return s

# ------------------------------------------------------------ frames
def parse_frames(buf):
    """Split a byte buffer into (header_dict, payload) frames; returns (frames, rest)."""
    frames = []
    while len(buf) >= 12:
        ln, comp, cmd, err, ty, mid = struct.unpack(">HHHHHH", buf[:12])
        hl = 12
        if ty & 0x10:                      # extended header (nfs server): 2 more bytes = high part of the length
            if len(buf) < 14: break
            ln |= struct.unpack(">H", buf[12:14])[0] << 16
            hl = 14
        if len(buf) < hl + ln: break
        frames.append((dict(component=comp, command=cmd, error=err, type=ty, id=mid), buf[hl:hl + ln]))
        buf = buf[hl + ln:]
    return frames, buf

def build(comp, cmd, mid, payload, ty=0x1000, err=0):
    return struct.pack(">HHHHHH", len(payload), comp, cmd, err, ty, mid) + payload
