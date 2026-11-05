import sys,re,struct,pickle,json,bisect
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import ps3,xref
L=pickle.load(open(__import__("common").out("blazesyms.pkl"),'rb'))
vt=pickle.load(open(__import__("common").out("blazevt.pkl"),'rb'))
vts=sorted(vt)
ctor={}
for a,n,d in L:
    m=re.match(r'(Blaze::[\w:]+)::(\w+)\((unsigned char|Blaze::MemoryGroupId.*)?\)$',d)
    if m and m.group(1).split('::')[-1]==m.group(2): ctor[a]=m.group(1)
def scan(site,n=200):
    regs={};job=None;cmd=None;comp=None;reqs=[]
    ws=[struct.unpack('>I',ps3.rd(site-4*k,4))[0] for k in range(n,0,-1)]
    base=site-4*n
    for i,w in enumerate(ws):
        op=w>>26; rd=(w>>21)&31; ra=(w>>16)&31; imm=w&0xffff
        if op==15 and ra==0: regs[rd]=imm<<16
        elif op in(14,12,13) and ra in regs:
            v=(regs[ra]+(imm if imm<0x8000 else imm-0x10000))&0xffffffff
            j=bisect.bisect_right(vts,v)-1
            if j>=0 and 0<=v-vts[j]<16:
                nmv=vt[vts[j]]
                if nmv.startswith('Blaze::RpcJob'): job=nmv
                elif not nmv.startswith(('Blaze::Functor','Blaze::TdfStructVector','Blaze::TdfPrimitive','Blaze::TdfStructMap','Blaze::TdfString')): reqs.append(nmv)
        if op==14 and ra==0:
            if rd==5: cmd=imm
            if rd==4: comp=imm
        if (w&0xfc000003)==0x48000001:
            off=w&0x03fffffc
            if off&0x02000000: off-=0x04000000
            t=base+4*i+off
            if t in ctor: reqs.append(ctor[t])
    return comp,cmd,job,reqs
rows=[]
for s in xref.callers(0x12cb894):
    s=int(s); comp,cmd,job,reqs=scan(s)
    rows.append({'site':hex(s),'caller':ps3.DM(ps3.name(s)),'comp':comp,'cmd':cmd,'job':job,'reqs':reqs[-3:]})
json.dump(rows,open(__import__("common").out("sites.json"),'w'),indent=0)
for r in rows: print(r['comp'],r['cmd'],(r['job'] or '')[:90],'|',r['reqs'][-1:] ,'<-',r['caller'][:80])
