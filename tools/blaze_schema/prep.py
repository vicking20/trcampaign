"""Step 1: symbol tables of the PS3 build (demangled Blaze symbols, vtables,
DWARF compile units of the BlazeSDK and of the NFS client code)."""
import os, sys, pickle, subprocess
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from common import out, PS3_ELF
import ps3  # builds out/syms.pkl
from elftools.elf.elffile import ELFFile
s = ps3.syms
def demangle(names):
    return subprocess.run(['c++filt'], input='\n'.join(n.lstrip('.') for n in names), capture_output=True, text=True).stdout.split('\n')
names = [(a, n) for a, n in s.items() if n.lstrip('.').startswith(('_ZN5Blaze', '_ZNK5Blaze'))]
pickle.dump(list(zip([a for a, n in names], [n for a, n in names], demangle([n for a, n in names]))), open(out('blazesyms.pkl'), 'wb'))
vt = [(a, n) for a, n in s.items() if n.startswith('_ZTVN5Blaze')]
pickle.dump({a: d[len('vtable for '):] for (a, n), d in zip(vt, demangle([n for a, n in vt]))}, open(out('blazevt.pkl'), 'wb'))
e = ELFFile(open(PS3_ELF, 'rb')); dw = e.get_dwarf_info(); ar = dw.get_aranges()
cus = {}
for cu in dw.iter_CUs():
    nm = cu.get_top_DIE().attributes['DW_AT_name'].value.decode(errors='replace')
    if 'blaze' in nm.lower():
        cus[cu.cu_offset] = nm
L = pickle.load(open(out('blazesyms.pkl'), 'rb'))
want = set()
for a, n, d in L:
    if (d.startswith('Blaze::NFS::') or 'GameReporting::' in d) and a < 0x2a00000:
        o = ar.cu_offset_at_addr(a)
        if o is not None:
            want.add(o)
for cu in dw.iter_CUs():
    if cu.cu_offset in want and cu.cu_offset not in cus:
        cus[cu.cu_offset] = cu.get_top_DIE().attributes['DW_AT_name'].value.decode(errors='replace')
pickle.dump(sorted(cus.items()), open(out('blazecus.pkl'), 'wb'))
print(len(L), 'Blaze symbols,', len(vt), 'vtables,', len(cus), 'compile units')
