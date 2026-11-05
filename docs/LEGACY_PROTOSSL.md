# Legacy ProtoSSL / SSLv3 compatibility

## Why this component exists

The 2011 PC client uses an SSL/ProtoSSL path older than modern Go TLS intentionally supports. The project isolates that compatibility requirement in a small x86 C gateway rather than weakening the main Go server.

Current Redirector path:

```text
client SSLv3 :42127
    -> Go public TCP front
    -> local C gateway :42129
    -> plaintext Go Redirector :42128
```

Web Autolog also uses the compatibility gateway on TCP 18443 before forwarding plaintext HTTP to the local server on 18080.

The observed Blaze game connection on TCP 10041 is plaintext and does not use this gateway.

## Build

`legacy_gateway/build_x86.bat`:

1. locates the Visual Studio x86 C++ tools;
2. downloads OpenSSL **1.0.2u** from the upstream archive if necessary;
3. verifies SHA-256:
   `ecd0c6ffb493dd06707d38b14bb4d8c2288bb7033735606569d8f90f89669d16`;
4. builds static x86 OpenSSL with SSLv3 methods and weak legacy cipher support retained;
5. builds `nfsrun-ssl-gateway.exe`;
6. places the OpenSSL 1.0.2u CLI beside it (not needed for the certificate).

OpenSSL source/build products are generated locally and ignored by Git.

## Certificate compatibility

The certificate is made by `nfsrun-server.exe` itself
(`source/internal/protocert`); OpenSSL is not involved.

What the game's old ProtoSSL accepts:

- issuer CN `OTG3 Certificate Authority`, O `Electronic Arts, Inc.`
  (OU Online Technology Group, L Redwood City, ST California, C US);
- subject CN covering the Redirector host: `*.<domain>` (one-label wildcard),
  OU Global Online Studio, O Electronic Arts, Inc., ST California, C US;
- X.509 v1, RSA 1024, signed with md5WithRSA, and the *outer* signature
  AlgorithmIdentifier written as rsaEncryption (`...01 01 01`) while the one
  inside the TBS stays md5WithRSA (`...01 01 04`). ProtoSSL then skips the
  signature check; the key pair stays a normal one for the SSL handshake.

The domain is `BASE_DOMAIN` in `config\server.env`. When the server's game
exe (`DS_GAME_DIR\DS_SOURCE_EXE`) was patched with `tools/patch_client.py`,
the domain in its four Redirector slots (`nfs.`, `scert-nfs.`, `stest-nfs.`,
`redirector.` + domain at file offsets 0x02234EA0..0x02234EF0) is used
instead.

```text
nfsrun-server.exe --exe-domain "<game folder>\Need For Speed The Run.exe"   print the domain
nfsrun-server.exe --make-cert [--domain X] [--game-exe path]               write a new certificate
nfsrun-server.exe --check-cert [--fix]                                     check (1 = refused); --fix makes a new one
```

First-run setup asks the game folder first and takes the domain from the exe;
`start_server.bat` runs `--check-cert --fix` before starting the gateway, and
the server logs `[CERT] ...` at startup. A replaced certificate is kept as
`*.old-<date>`. A hand-made certificate that passes the check is left alone.

No EA private key is required or distributed.

Generated files:

```text
certs/protossl_redirector.crt
certs/protossl_redirector.key
certs/protossl_redirector.info.txt
```

They are server-local generated artifacts and are ignored by Git.

## Security boundary

SSLv3, RC4-era cipher compatibility and OpenSSL 1.0.2u are obsolete and insecure by modern standards.

Use this component only as a compatibility boundary for this discontinued game protocol:

- keep decrypted backend ports on localhost;
- do not expose the OpenSSL CLI/gateway as a general TLS service;
- do not use it for web administration, credentials or unrelated applications;
- expose only the public game ports you actually need.
