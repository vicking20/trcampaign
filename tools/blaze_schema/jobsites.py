import sys,struct,pickle,bisect,re,json
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import ps3,xref
V=pickle.load(open(__import__("common").out("blazevt.pkl"),'rb')); vts=sorted(V)
def w(a): return struct.unpack('>I',ps3.rd(a,4))[0]
rows=[]
for t in (0x12e0500,0x12e0494):
    for s in xref.callers(t):
        s=int(s); cmd=None
        for k in range(1,16):
            x=w(s-4*k)
            if x>>26==14 and ((x>>16)&31)==0 and ((x>>21)&31)==5: cmd=x&0xffff; break
        regs={}; job=None
        for k in range(1,24):
            a=s+4*k; x=w(a); op=x>>26; rd=(x>>21)&31; ra=(x>>16)&31; imm=x&0xffff
            if op==15 and ra==0: regs[rd]=imm<<16
            elif op in(14,12,13) and ra in regs:
                v=(regs[ra]+(imm if imm<0x8000 else imm-0x10000))&0xffffffff
                j=bisect.bisect_right(vts,v)-1
                if j>=0 and 0<=v-vts[j]<16 and V[vts[j]].startswith('Blaze::RpcJob'): job=V[vts[j]]; break
        rows.append((cmd,job,ps3.DM(ps3.name(s))))
for r in sorted(rows,key=lambda r:(str(r[1]))):
    if r[1] and ('Authentication' in r[1] or 'PostAuth' in r[1] or 'User' in r[1]): print(r[0],r[1][:110],'<-',r[2][:90])
json.dump(rows,open(__import__("common").out("jobsites.json"),'w'))
