OpenSSL is NOT bundled in this source package.

build_x86.bat downloads the official OpenSSL 1.0.2u source archive from
openssl.org, verifies its SHA-256, extracts it here and compiles static x86
libraries with SSLv3 methods retained.

Expected archive SHA-256:
ecd0c6ffb493dd06707d38b14bb4d8c2288bb7033735606569d8f90f89669d16

OpenSSL 1.0.2u is EOL and is used here only as an isolated compatibility
boundary for a discontinued 2011 game protocol. Do not reuse this gateway as
a general-purpose TLS endpoint.
