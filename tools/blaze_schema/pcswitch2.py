import sys,struct,re,json,capstone
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import pcstr
data=pcstr.data; va2off=pcstr.va2off; off2va=pcstr.off2va
md=capstone.Cs(capstone.CS_ARCH_X86,capstone.CS_MODE_32)
def u32(v): return struct.unpack('<I',data[va2off(v):va2off(v)+4])[0]
def case_str(v):
    o=va2off(v)
    if o is None: return None
    if data[o]==0xB8:
        s=struct.unpack('<I',data[o+1:o+5])[0]; t=pcstr.cstr(s)
        if t and re.fullmatch(rb'[A-Za-z_][\w]*',t): return t.decode()
    return None
res=[]
for m in re.finditer(rb'\xff\x24\x85',data):
    jva=off2va(m.start()); jt=struct.unpack('<I',data[m.end():m.end()+4])[0]
    if va2off(jt) is None: continue
    first=u32(jt)
    if not case_str(first): continue
    # disassemble back window
    best=None
    for back in range(60,4,-1):
        ins=list(md.disasm(data[m.start()-back:m.start()+7],jva-back))
        if ins and ins[-1].address==jva and ins[-1].mnemonic=='jmp': best=ins;break
    if not best: continue
    bias=0;mx=None;btab=None;src=None
    k=max([n for n,i in enumerate(best) if i.mnemonic=='movzx' and '[esp + 4]' in i.op_str] or [0])
    best=best[k:]
    for i in best:
        o=i.op_str
        if i.mnemonic in('movzx','mov') and ('[esp + 4]' in o or '[esp + 8]' in o) and src is None: src=o
        if i.mnemonic=='dec': bias+=1
        if i.mnemonic=='sub' and re.search(r', (0x[0-9a-f]+|\d+)$',o): bias+=int(o.split(', ')[1],0)
        if i.mnemonic=='add' and re.search(r'e[a-d]x, (-?0x[0-9a-f]+|-?\d+)$',o): bias-=int(o.split(', ')[1],0)
        if i.mnemonic=='cmp' and re.search(r', (0x[0-9a-f]+|\d+)$',o): mx=int(o.split(', ')[1],0)
        if i.mnemonic=='movzx' and 'byte ptr [eax + 0x' in o: btab=int(re.search(r'0x[0-9a-f]+',o.split('[')[1]).group(0),16)
    if mx is None or mx>5000: continue
    tab={}
    for i in range(mx+1):
        idx=data[va2off(btab+i)] if btab else i
        s=case_str(u32(jt+4*idx))
        if s: tab[(i+bias)&0xffff]=s
    res.append({'jmp':hex(jva),'bias':bias,'max':mx,'btab':hex(btab) if btab else None,'names':tab})
json.dump(res,open(__import__("common").out("pcswitch2.json"),'w'),indent=0)
for r in res: print(r['jmp'],r['bias'],r['max'],r['btab'],len(r['names']),list(r['names'].items())[:3])
