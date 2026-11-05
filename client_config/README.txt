Util.fetchClientConfig sections
===============================

One file per section: <SECTION>.cfg. The game asks for NFS12_THERUN.
Format: KEY = value, one per line; blank lines and # comments are ignored.
The files are read on every request (no restart needed).

NFS12_THERUN.cfg keys:

  OnlinePassKillSwitch = true    turns the Online Pass check off (keep it)
  V8ServerName = host:port       web Autolog host. Leave it unset: the
                                 server sends WEB_AUTOLOG_HOST, or
                                 PUBLIC_HOST:18443

Other keys the game reads in this section (not needed):
  V8XlspName, V8XlspPort, instantConnectTo, PcContentMapLength

See docs\CONFIGURATION.md.
