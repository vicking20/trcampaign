"""Pure-Python RSA key generation (PKCS#1 PEM) for machines without openssl (e.g. Windows)."""
import base64, random

def der(tag, body):
    n = len(body)
    L = bytes([n]) if n < 128 else (bytes([0x81, n]) if n < 256 else bytes([0x82, n >> 8, n & 255]))
    return bytes([tag]) + L + body

def integer(v):
    return der(0x02, v.to_bytes((v.bit_length() + 8) // 8, "big"))


def generate_key(path, bits=1024):
    """Write a PKCS#1 RSA private key PEM without needing openssl (used when openssl is missing, e.g. on Windows)."""
    import random
    def is_prime(n):
        if n < 2: return False
        for q in (2, 3, 5, 7, 11, 13, 17, 19, 23, 29): 
            if n % q == 0: return n == q
        d, r = n - 1, 0
        while d % 2 == 0: d //= 2; r += 1
        for _ in range(24):
            a = random.randrange(2, n - 1); x = pow(a, d, n)
            if x in (1, n - 1): continue
            for _ in range(r - 1):
                x = x * x % n
                if x == n - 1: break
            else: return False
        return True
    def prime(b):
        while True:
            c = random.SystemRandom().getrandbits(b) | (1 << (b - 1)) | 1
            if is_prime(c): return c
    e = 65537
    while True:
        p, q = prime(bits // 2), prime(bits // 2)
        if p == q: continue
        phi = (p - 1) * (q - 1)
        if phi % e: break
    n = p * q; d = pow(e, -1, phi)
    body = b"".join(integer(v) for v in (0, n, e, d, p, q, d % (p - 1), d % (q - 1), pow(q, -1, p)))
    pem = base64.encodebytes(der(0x30, body)).decode().replace("\n", "")
    lines = "\n".join(pem[i:i + 64] for i in range(0, len(pem), 64))
    open(path, "w").write("-----BEGIN RSA PRIVATE KEY-----\n" + lines + "\n-----END RSA PRIVATE KEY-----\n")
