import sys,struct,re,json,collections
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import pcstr,capstone
md=capstone.Cs(capstone.CS_ARCH_X86,capstone.CS_MODE_32)
d=pcstr.data
VIS=json.load(open(__import__("common").out("visit.json")))
def u32(v):
    o=pcstr.va2off(v); return struct.unpack_from('<I',d,o)[0] if o is not None else None
def untag(w):
    t=w>>8; s=''
    for i in range(4):
        c=(t>>(18-6*i))&0x3f
        if c: s+=chr(c+0x20)
    return s
res={}
for m in re.finditer(rb'\0(Blaze::(?:NFS|GameReporting::NFS)::[A-Za-z0-9_:]+)\0',d):
    name=m.group(1).decode(); sv=pcstr.off2va(m.start()+1)
    fns=[pcstr.off2va(x.start()) for x in re.finditer(re.escape(b'\xb8'+struct.pack('<I',sv)+b'\xc3'),d)]
    for f in fns:
        for slot in pcstr.ptrs_to(f):
            vis=u32(slot-16)
            o=pcstr.va2off(vis)
            if o is None: continue
            mem=[];lastslot=None;pushes=[]
            for x in md.disasm(d[o:o+4000],vis):
                s=x.mnemonic+' '+x.op_str
                mm=re.match(r'mov e\wx, dword ptr \[e\wx \+ (0x[0-9a-f]+)\]',s)
                if mm: lastslot=int(mm.group(1),16)
                if x.mnemonic=='push' and x.op_str.startswith('0x'):
                    v=int(x.op_str,16)
                    if v&0xff==0 and v>0xffffff: pushes.append(v)
                if x.mnemonic=='call' and not x.op_str.startswith('0x'):
                    if lastslot is not None and pushes:
                        mem.append((untag(pushes[-1]),lastslot))
                    lastslot=None; pushes=[]
                if x.mnemonic=='ret': break
            res[name]=mem
json.dump(res,open(__import__("common").out("pcvisit.json"),'w'),indent=0)
print(len(res))
# vote slot->kind using PS3 visit kinds for identical tags
vote=collections.defaultdict(collections.Counter)
for cls,mem in res.items():
    ps={e['tag']:e['kind'] for e in VIS.get(cls,[])}
    for tag,sl in mem:
        if tag in ps: vote[sl][ps[tag]]+=1
for sl in sorted(vote): print(hex(sl),vote[sl].most_common(3))
