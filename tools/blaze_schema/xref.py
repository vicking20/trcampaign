import sys;import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
import numpy as np, ps3
sa,sz,so,_=[s for s in ps3.secs if s[3]=='.text'][0]
ps3.f.seek(so); buf=np.frombuffer(ps3.f.read(sz),dtype='>u4')
addr=sa+np.arange(len(buf),dtype=np.int64)*4
isbl=(buf & 0xfc000003)==0x48000001
off=(buf & 0x03fffffc).astype(np.int64); off=np.where(off&0x02000000,off-0x04000000,off)
tgt=addr+off
def callers(t):
    return addr[isbl & (tgt==t)]
if __name__=="__main__":
    for t in sys.argv[1:]:
        t=int(t,16); print("callers of",ps3.DM(ps3.name(t)))
        for a in callers(t): print("  %08x %s"%(a,ps3.DM(ps3.name(int(a)))))
