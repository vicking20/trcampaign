import struct,sys,re
d=open(sys.argv[1],'rb').read()
BASE,OFF0,SIZE=0x401000,0x1000,0x1e003a1
def func_start(va):
    i=va-BASE+OFF0
    while i>OFF0:
        if d[i:i+3]==b'\x55\x8b\xec' and d[i-1] in (0xcc,0xc3,0x90) : return BASE+i-OFF0
        i-=1
def callers(tgt):
    out=[]
    for i in range(0,SIZE-5):
        b=d[OFF0+i]
        if b in (0xE8,0xE9):
            rel=struct.unpack_from('<i',d,OFF0+i+1)[0]
            if BASE+i+5+rel==tgt: out.append(BASE+i)
    return out
va=int(sys.argv[2],16)
fs=func_start(va); print("func start",hex(fs))
seen=set(); fr=[fs]
for depth in range(3):
    nxt=[]
    for f in fr:
        for c in callers(f):
            fs2=func_start(c)
            print("  "*depth,"callee",hex(f),"<- call at",hex(c),"in func",hex(fs2) if fs2 else None)
            if fs2 and fs2 not in seen: seen.add(fs2); nxt.append(fs2)
    fr=nxt
