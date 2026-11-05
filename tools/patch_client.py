#!/usr/bin/env python3
"""NFS The Run PC 1.1.0.0 Blaze Redirector patcher.

Patches all four embedded EA Redirector environment hostnames in the user's
own executable. The analyzed binary is validated by SHA-256 by default.
"""
from __future__ import annotations
import argparse
import hashlib
import shutil
from pathlib import Path

KNOWN_SHA256 = "61488c6ef13953b92f3b4fad6e66c3a54d7e0285eb222a0cff08bc519948a22a"

PATCHES = (
    (b"gosredirector.ea.com",          0x02234EA0, "nfs.{domain}"),
    (b"gosredirector.scert.ea.com",    0x02234EB8, "scert-nfs.{domain}"),
    (b"gosredirector.stest.ea.com",    0x02234ED4, "stest-nfs.{domain}"),
    (b"gosredirector.online.ea.com",   0x02234EF0, "redirector.{domain}"),
)

def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()

def normalize_domain(value: str) -> str:
    s = value.strip().lower()
    for prefix in ("https://", "http://"):
        if s.startswith(prefix):
            s = s[len(prefix):]
    s = s.split("/", 1)[0].rstrip(".")
    if not s or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-." for c in s):
        raise ValueError("invalid domain")
    return s

def main() -> int:
    ap = argparse.ArgumentParser(description="Patch all NFS The Run 1.1.0.0 Blaze Redirector hosts")
    ap.add_argument("exe", nargs="?", default="Need For Speed The Run.exe")
    ap.add_argument("--domain", default=None, help="base domain used by your private server")
    ap.add_argument("--output", help="write the patched exe here instead of patching in place")
    ap.add_argument("--allow-unknown-hash", action="store_true")
    args = ap.parse_args()

    src = Path(args.exe)
    if not src.is_file():
        print(f"ERROR: not found: {src}")
        return 2

    digest = sha256(src)
    print("Input SHA-256:", digest)
    if digest != KNOWN_SHA256 and not args.allow_unknown_hash:
        print("ERROR: this is not the analyzed PC 1.1.0.0 executable.")
        print("Expected:", KNOWN_SHA256)
        return 3

    raw_domain = args.domain
    if raw_domain is None:
        raw_domain = input("Base domain: ").strip()
        if not raw_domain:
            print("ERROR: a base domain is required.")
            return 4
    try:
        domain = normalize_domain(raw_domain)
    except ValueError as exc:
        print("ERROR:", exc)
        return 4

    data = bytearray(src.read_bytes())
    prepared: list[tuple[bytes, int, bytes]] = []

    for old, expected_offset, template in PATCHES:
        host = template.format(domain=domain).encode("ascii")
        if len(host) > len(old):
            print(f"ERROR: {host.decode()} is {len(host)} chars, but its slot is {len(old)}.")
            return 5
        hits = []
        pos = 0
        while True:
            i = data.find(old, pos)
            if i < 0:
                break
            hits.append(i)
            pos = i + 1
        if len(hits) != 1:
            print(f"ERROR: expected one {old.decode()} string; found {len(hits)}")
            return 6
        off = hits[0]
        if digest == KNOWN_SHA256 and off != expected_offset:
            print(f"ERROR: {old.decode()} found at unexpected offset 0x{off:08X}")
            return 7
        prepared.append((old, off, host))

    for old, off, host in prepared:
        slot = len(old) + 1
        repl = host + b"\x00" * (slot - len(host))
        data[off:off + slot] = repl

    out = Path(args.output) if args.output else src
    if out.resolve() == src.resolve():
        bak = src.with_suffix(src.suffix + ".bak")
        if not bak.exists():
            shutil.copy2(src, bak)
            print("Backup:", bak)
    out.write_bytes(data)

    print("\nPatched Redirector table:")
    for old, off, host in prepared:
        print(f"  0x{off:08X}  {old.decode():31s} -> {host.decode()}")
    print("\nOutput:", out)
    print("Output SHA-256:", sha256(out))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
