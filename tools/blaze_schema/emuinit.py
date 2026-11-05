import sys,struct
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import pcstr
from unicorn import *
from unicorn.x86_const import *
pe=pcstr.pe; d=pcstr.data
mu=Uc(UC_ARCH_X86,UC_MODE_32)
BASE=0x400000
mu.mem_map(BASE,0x1000)
mu.mem_write(BASE,d[:0x1000])
for s in pe.sections:
    va=BASE+s.VirtualAddress; size=(max(s.Misc_VirtualSize,s.SizeOfRawData)+0xfff)&~0xfff
    mu.mem_map(va,size)
    mu.mem_write(va,d[s.PointerToRawData:s.PointerToRawData+s.SizeOfRawData])
STACK=0x10000000; mu.mem_map(STACK,0x100000)
HEAP=0x20000000; mu.mem_map(HEAP,0x1000000)
RET=0x30000000; mu.mem_map(RET,0x1000); mu.mem_write(RET,b'\xcc'*16)
# fs segment (TLS) dummy
FS=0x31000000; mu.mem_map(FS,0x10000)
def u32(v): return struct.unpack('<I',mu.mem_read(v,4))[0]
inits=[u32(a) for a in range(int(sys.argv[1],16),int(sys.argv[2],16)+4,4)]
ok=fail=0
for f in inits:
    if not f: continue
    sp=STACK+0x80000
    mu.mem_write(sp,struct.pack('<I',RET))
    mu.reg_write(UC_X86_REG_ESP,sp); mu.reg_write(UC_X86_REG_EBP,sp)
    for r in (UC_X86_REG_EAX,UC_X86_REG_ECX,UC_X86_REG_EDX,UC_X86_REG_EBX,UC_X86_REG_ESI,UC_X86_REG_EDI): mu.reg_write(r,0)
    try:
        mu.emu_start(f,RET,count=200000); ok+=1
    except UcError as e:
        fail+=1
print('ok',ok,'fail',fail)
img=bytearray(d)
for s in pe.sections:
    va=BASE+s.VirtualAddress
    if s.Name.startswith((b'.data',b'.rdata',b'fieldinf',b'typeinfo')):
        n=s.SizeOfRawData
        img[s.PointerToRawData:s.PointerToRawData+n]=mu.mem_read(va,n)
open(__import__("common").out("nfs_inited.bin"),'wb').write(img)
