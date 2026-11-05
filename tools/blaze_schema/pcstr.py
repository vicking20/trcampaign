import sys,struct,pefile
from common import PC_EXE as P, out
pe=pefile.PE(P,fast_load=True); data=open(P,'rb').read()
base=0x400000
def off2va(o):
    for s in pe.sections:
        if s.PointerToRawData<=o<s.PointerToRawData+s.SizeOfRawData: return base+s.VirtualAddress+o-s.PointerToRawData
def va2off(v):
    r=v-base
    for s in pe.sections:
        if s.VirtualAddress<=r<s.VirtualAddress+s.SizeOfRawData: return s.PointerToRawData+r-s.VirtualAddress
def cstr(v):
    o=va2off(v); 
    if o is None: return None
    e=data.find(b'\0',o); return data[o:e]
def find_str(s):
    o=data.find(b'\0'+s+b'\0'); return off2va(o+1) if o>=0 else None
def ptrs_to(v):
    p=struct.pack('<I',v); res=[]; o=data.find(p)
    while o>=0: res.append(off2va(o)); o=data.find(p,o+1)
    return res
if __name__=="__main__":
    for s in sys.argv[1:]:
        v=find_str(s.encode()); print(s,hex(v) if v else None,[hex(x) for x in ptrs_to(v)] if v else '')
