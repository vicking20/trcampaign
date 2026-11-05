# Legacy SSLv3 gateway

A small x86 C program that accepts the game's SSLv3 connections and forwards
the decrypted stream to the Go server on 127.0.0.1. It exists only because
the 2011 client cannot talk to a modern TLS stack.

```text
Redirector   game SSLv3 :42127 -> Go front -> gateway 127.0.0.1:42129 -> Go 127.0.0.1:42128
Web Autolog  game HTTPS :18443 -> gateway -> Go HTTP 127.0.0.1:18080
Blaze        game TCP   :10041 -> Go 127.0.0.1:10042 (plaintext, no gateway)
```

## Build

Requirements: Visual Studio Build Tools (C++ x86), Perl, PowerShell,
`tar.exe`, internet access for the first build.

```bat
build_x86.bat     build build\nfsrun-ssl-gateway.exe (also run by Build_All.bat)
clean.bat         remove obj\ and build\ (the OpenSSL download is kept)
```

`build_x86.bat` downloads OpenSSL 1.0.2u, checks SHA-256
`ecd0c6ffb493dd06707d38b14bb4d8c2288bb7033735606569d8f90f89669d16` and
builds it statically with SSLv3 enabled.

## Run

```bat
nfsrun-ssl-gateway.exe [config file]
```

Default config file: `config\gateway.ini` (written by first-run setup;
`start_server.bat` starts the gateway).

## `gateway.ini`

```ini
[gateway]
cert_file=certs\protossl_redirector.crt
key_file=certs\protossl_redirector.key
cipher_list=RC4-SHA:RC4-MD5:AES128-SHA:AES256-SHA:DES-CBC3-SHA
log_file=logs\gateway.log

[redirector]
enabled=1
name=REDIRECTOR
listen_host=127.0.0.1
listen_port=42129
backend_host=127.0.0.1
backend_port=42128

[blaze]
enabled=1
name=WEBAUTOLOG
listen_host=0.0.0.0
listen_port=18443
backend_host=127.0.0.1
backend_port=18080
```

| Key | Default | Description |
|---|---|---|
| `[gateway] cert_file` | `certs\protossl_redirector.crt` | Certificate presented to the game (made by `nfsrun-server.exe --make-cert`). |
| `[gateway] key_file` | `certs\protossl_redirector.key` | Its private key. |
| `[gateway] cipher_list` | as above | OpenSSL cipher list. |
| `[gateway] log_file` | `logs\gateway.log` | Log file. |
| `enabled` | `1` | `1` on, `0` off. |
| `name` | `REDIRECTOR` / `WEBAUTOLOG` | Name in the log. |
| `listen_host` | `127.0.0.1` | Listen address. Use `0.0.0.0` for a public listener. |
| `listen_port` | `42129` / `18443` | Listen port. |
| `backend_host` | `127.0.0.1` | Plaintext backend. |
| `backend_port` | `42128` / `18080` | Backend port. |

`[redirector]` sits behind the Go front on 42127. `[blaze]` is the public
HTTPS front of the web Autolog; `run_server.ps1` restores it on every start
unless `WEB_AUTOLOG_HOST=off`.

## Security

OpenSSL 1.0.2u, SSLv3 and RC4-era ciphers are obsolete. Keep backends on
127.0.0.1 and never use this gateway for anything but the game.
See [docs/LEGACY_PROTOSSL.md](../docs/LEGACY_PROTOSSL.md).
