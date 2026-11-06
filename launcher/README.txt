NFSRUN-LAUNCHER (not used by NFSTR Online players)
==================================================

source/cmd/nfsrun-launcher is the original emulator's Windows player launcher (invite code + key login, it patches its own copy of the
game). NFSTR Online does not ship it: players use the game plugin and the Origin helper instead (see the main README, client/ and helper/).

The code is kept because the server still shares its Origin/LSX parts. Its notices are in THIRD_PARTY_NOTICES.txt and must stay with the code.
The launcher-key login is described in docs/LOGIN.md for reference.
