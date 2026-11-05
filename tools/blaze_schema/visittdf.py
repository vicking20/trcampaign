import sys,struct,pickle,re,json
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import ps3
L=pickle.load(open(__import__("common").out("blazesyms.pkl"),'rb'))
T=json.load(open(__import__("common").out("tdf.json")))
SLOT={0x10:'vector',0x14:'map',0x18:'bool',0x1c:'char',0x20:'i8',0x24:'u8',0x28:'i16',0x2c:'u16',0x30:'i32',0x34:'u32',0x38:'i64',0x3c:'u64',0x40:'float',0x44:'bitfield',0x48:'string',0x4c:'blob',0x50:'enum',0x54:'struct',0x58:'union',0x5c:'variable',0x60:'objtype',0x64:'objid',0x68:'timevalue'}
WIRE={'vector':4,'map':5,'string':1,'blob':2,'struct':3,'union':6,'variable':7,'objtype':8,'objid':9,'float':10}
def w(a): return struct.unpack('>I',ps3.rd(a,4))[0]
def tagname(v):
    t=v>>8; s=''
    for i in range(4):
        c=(t>>(18-6*i))&0x3f
        if c: s+=chr(c+0x20)
    return s
def sx(i): return i-0x10000 if i&0x8000 else i
out={}
for a,n,d in L:
    m=re.match(r'(Blaze::.*)::visit\(Blaze::TdfVisitor&, Blaze::Tdf&, Blaze::Tdf const&\)$',d)
    if not m or not (0x33fe720<=a<0x357b230): continue
    cls=m.group(1)
    if cls in T: continue
    code=w(a)
    R={3:('THIS',0),4:('VIS',0),5:('ROOT',0),6:('REF',0)}
    ctr=None; mem=[]
    pc=code
    for _ in range(2000):
        x=w(pc); op=x>>26; rd=(x>>21)&31; ra=(x>>16)&31; rb=(x>>11)&31; imm=x&0xffff
        if x==0x4e800020: break
        if op==15: R[rd]=('C',(imm<<16)&0xffffffff) if ra==0 else None
        elif op==24:  # ori rA,rS,imm  (rd=rS, ra=rA)
            s=R.get(rd)
            if imm==0: R[ra]=s
            elif s and s[0]=='C': R[ra]=('C',s[1]|imm)
            else: R[ra]=None
        elif op in (14,12,13):  # addi/addic rD,rA,imm
            s=R.get(ra) if ra else ('C',0)
            if s and s[0] in('C','THIS'): R[rd]=(s[0],(s[1]+sx(imm))&0xffffffff)
            else: R[rd]=None
        elif op==30:  # rld* (clrldi rA,rS,..)
            R[ra]=R.get(rd)
        elif op==32:  # lwz rD, d(rA)
            s=R.get(ra)
            if s==('VIS',0) and imm==0: R[rd]=('VT',0)
            elif s and s[0]=='VT': R[rd]=('SLOT',imm)
            elif s and s[0]=='SLOT' and imm==0: R[rd]=('FN',s[1])
            else: R[rd]=None
        elif op==31 and ((x>>1)&0x3ff)==467 and ((x>>11)&0x3ff)==0x120:  # mtctr
            ctr=R.get(rd)
        elif x==0x4e800421 or x==0x4e800420 or (op==19 and ((x>>1)&0x3ff)==528):  # bctrl / bdnzctrl variants
            if ctr and ctr[0]=='FN':
                tg=R.get(6); mo=R.get(7)
                mem.append({'slot':ctr[1],'kind':SLOT.get(ctr[1],'?%x'%ctr[1]),'tag':tagname(tg[1]) if tg and tg[0]=='C' else None,'off':mo[1] if mo and mo[0]=='THIS' else None})
            ctr=None
            for r in range(0,13): R[r]=None
        elif (x&0xfc000003)==0x48000001:
            off=x&0x03fffffc
            if off&0x02000000: off-=0x04000000
            if 'WFrprep' not in ps3.name(pc+off):
                for r in range(3,13): R[r]=None
        pc+=4
    out[cls]=mem
json.dump(out,open(__import__("common").out("visit.json"),'w'),indent=0)
print(len(out))
for k in list(out)[:12]: print(k,out[k])
