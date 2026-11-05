import sys,bisect,pickle,os
from elftools.elf.elffile import ELFFile
import capstone
from common import PS3_ELF as P, out
f=open(P,"rb"); e=ELFFile(f)
secs=[(s['sh_addr'],s['sh_size'],s['sh_offset'],s.name) for s in e.iter_sections() if s['sh_addr']]
def rd(a,n):
    for sa,sz,so,nm in secs:
        if sa<=a<sa+sz and nm not in('.bss',):
            f.seek(so+a-sa); return f.read(n)
cache=out("syms.pkl")
if os.path.exists(cache): syms=pickle.load(open(cache,"rb"))
else:
    syms={}
    for s in e.get_section_by_name('.symtab').iter_symbols():
        if s['st_value'] and s.name: syms.setdefault(s['st_value'],s.name)
    pickle.dump(syms,open(cache,"wb"))
addrs=sorted(syms)
def name(a):
    i=bisect.bisect_right(addrs,a)-1
    if i<0: return hex(a)
    b=addrs[i]; return syms[b]+("" if a==b else "+%x"%(a-b))
def find(n): return [a for a,s in syms.items() if s==n]
md=capstone.Cs(capstone.CS_ARCH_PPC,capstone.CS_MODE_64|capstone.CS_MODE_BIG_ENDIAN)
def dis(a,n=400):
    code=rd(a,n*4)
    for i in md.disasm(code,a):
        extra=""
        if i.mnemonic in("bl","b") :
            t=int(i.op_str,16); extra="  ; "+name(t)
        print("%08x: %-8s %s%s"%(i.address,i.mnemonic,i.op_str,extra))
        if i.mnemonic=="blr" and n<0: break
if __name__=="__main__":
    t=sys.argv[1]; n=int(sys.argv[2]) if len(sys.argv)>2 else 200
    a=int(t,16) if t.startswith("0x") else find(t)[0]
    print("==",name(a)); dis(a,n)

def calls(a,maxn=3000):
    code=rd(a,maxn*4)
    import pickle
    for i in md.disasm(code,a):
        if i.mnemonic=="bl":
            t=int(i.op_str,16); print("%08x: bl %s"%(i.address,DM(name(t))))
        if i.mnemonic=="blr": print("%08x: blr"%i.address)
        # stop at next symbol start
        if i.address!=a and i.address in syms: break
_dm=None
def DM(n):
    global _dm
    import pickle
    if _dm is None: _dm={v:k for k,v in pickle.load(open(out("syms.pkl"),"rb")).items()}; 
    base=n.split('+')[0]
    import subprocess
    d=subprocess.run(["c++filt",base.lstrip('.')],capture_output=True,text=True).stdout.strip()
    return d+("+"+n.split('+')[1] if '+' in n else "")
