#!/bin/sh
# Rebuild source/internal/blaze/schema.json and docs/BLAZE_API.md from the game.
#   PS3_ELF=/path/NFS.Main_Ps3_Release_unsigned.elf PC_EXE="/path/Need For Speed The Run.exe" sh run_all.sh
# Needs python3 with pyelftools, capstone, pefile, numpy, unicorn; c++filt (binutils).
set -e
cd "$(dirname "$0")"
python3 prep.py
python3 dwarf.py
python3 tdfinfo.py
python3 visittdf.py
python3 emuinit.py 0x23da130 0x23da56c   # PC: run the C++ static initializers that fill the member tables
python3 pcvt.py
python3 pcschema.py                     # PC vs PS3 member tables (verification report)
python3 pcvisit.py
python3 pcswitch2.py >/dev/null
python3 errcodes.py
python3 sites.py >/dev/null
python3 jobsites.py >/dev/null
python3 notifs.py >/dev/null
python3 build.py >/dev/null
python3 gen.py
cp "$(python3 -c "import common;print(common.out('blaze_schema.json'))")" ../../source/internal/blaze/schema.json
(cd ../../source && SCHEMA_STATUS_OUT="$PWD/../tools/blaze_schema/status.json" go test -count=1 -run TestEveryCommandAgainstSchema ./internal/server/ >/dev/null)
python3 gen_doc.py
