import sys,struct,re,pickle,json
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import ps3
L=pickle.load(open(__import__("common").out("blazesyms.pkl"),'rb'))
ctor={}
ctor2={}
for a,n,d in L:
    m2=re.match(r'(Blaze::[\w:]+)::(\w+)\((unsigned char|Blaze::MemoryGroupId)?\)$',d)
    if m2 and m2.group(1).split('::')[-1]==m2.group(2): ctor2[a]=m2.group(1)
    m=re.match(r'Blaze::Functor\d<(Blaze::[\w:]+) const\*.*>::operator\(\)',d)
    if m: ctor[a]=m.group(1)
def w(a): return struct.unpack('>I',ps3.rd(a,4))[0]
def bl_target(a,x):
    off=x&0x03fffffc
    if off&0x02000000: off-=0x04000000
    return a+off
TAB={}
def sim(start,val,limit=400):
    pc=start; cr=None; reg=None; steps=0
    while steps<limit:
        steps+=1; x=w(pc); op=x>>26
        if op==11:  # cmpwi
            ra=(x>>16)&31; imm=x&0xffff; imm=imm-0x10000 if imm&0x8000 else imm
            if reg is None or ra==reg: cr=(val>imm)-(val<imm)
        elif op==10:  # cmplwi
            ra=(x>>16)&31; imm=x&0xffff
            if reg is None or ra==reg: cr=(val>imm)-(val<imm)
        elif op==16:  # bc
            bo=(x>>21)&31; bi=(x>>16)&3; bd=x&0xfffc; bd=bd-0x10000 if bd&0x8000 else bd
            cond={0:cr<0,1:cr>0,2:cr==0}.get(bi,False)
            if bo&0x14==0x14: take=True
            elif bo&0x08: take=cond
            else: take=not cond
            if take: pc=pc+bd; continue
        elif op==18 and not x&1:  # b
            pc=bl_target(pc,x); continue
        elif op==18 and x&1:
            t=bl_target(pc,x)
            if t in TAB: return TAB[t],pc
        elif x==0x4e800020: return None,pc
        pc+=4
    return None,pc
out={}
for a,n,d in sorted(L):
    m=re.match(r'Blaze::(.*)::handleNotification\(unsigned short, unsigned char const\*, unsigned int\)$',d)
    if not m or a>0x3000000: continue
    # find reg: clrlwi rX, r4, 16
    reg=None
    for k in range(0,40):
        x=w(a+4*k)
        if x>>26==21 and ((x>>21)&31)==4 and False: pass
    res={}
    for v in range(0,0x400):

        TAB=ctor; t,pc=sim(a,v)
        if not t:
            TAB=ctor2; t,pc=sim(a,v)
            if t: t='~'+t
        if t: res.setdefault(t,[]).append(v)
    out[m.group(1)]={t:vs for t,vs in res.items() if len(vs)<12}
    print(m.group(1),{t.split('::')[-1]:[hex(x) for x in vs] for t,vs in res.items() if len(vs)<12})
json.dump(out,open(__import__("common").out("notifs.json"),'w'),indent=0)
