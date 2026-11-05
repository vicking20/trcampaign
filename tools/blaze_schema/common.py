"""Paths for the schema extraction. Set the environment variables:
  PS3_ELF  NFS.Main_Ps3_Release_unsigned.elf (PS3 build with DWARF and symbols)
  PC_EXE   Need For Speed The Run.exe (retail PC 1.1)
  OUT      work folder (default: ./out next to these scripts)
Neither binary is part of the repository."""
import os
HERE = os.path.dirname(os.path.abspath(__file__))
PS3_ELF = os.environ.get('PS3_ELF', '')
PC_EXE = os.environ.get('PC_EXE', '')
OUT = os.environ.get('OUT', os.path.join(HERE, 'out'))
os.makedirs(OUT, exist_ok=True)
def out(name): return os.path.join(OUT, name)
