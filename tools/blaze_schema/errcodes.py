import sys,struct,re,json
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import pcstr,capstone
md=capstone.Cs(capstone.CS_ARCH_X86,capstone.CS_MODE_32)
d=pcstr.data
codes={}
names=set(m.group(1) for m in re.finditer(rb'\0([A-Z][A-Z0-9_]*ERR_[A-Z0-9_]+)\0',d))
names|=set(m.group(1) for m in re.finditer(rb'\0(ERR_[A-Z0-9_]+)\0',d))
done_fn=set()
for nm in names:
    sv=pcstr.off2va(d.find(b'\0'+nm+b'\0')+1)
    for m in re.finditer(re.escape(b'\xb8'+struct.pack('<I',sv)),d):
        site=pcstr.off2va(m.start()); o=m.start()
        if not (0x401000<=site<0x2202000): continue
        s0=d.rfind(b'\xcc\xcc',0,o)+2; e0=d.find(b'\xcc\xcc',o)
        fn=pcstr.off2va(s0)
        if fn in done_fn: continue
        done_fn.add(fn)
        ins=list(md.disasm(d[s0:e0],fn))
        # map: address -> instr index
        lastcmp=None; jtarget={}
        for i,x in enumerate(ins):
            if x.mnemonic=='cmp' and re.search(r', (0x[0-9a-f]+|\d+)$',x.op_str): lastcmp=int(x.op_str.split(', ')[1],0)
            if x.mnemonic=='je' and lastcmp is not None and x.op_str.startswith('0x'): jtarget[int(x.op_str,16)]=lastcmp
            if x.mnemonic=='jne' and lastcmp is not None and i+1<len(ins): jtarget[ins[i+1].address]=lastcmp
        for x in ins:
            if x.mnemonic=='mov' and x.op_str.startswith('eax, 0x'):
                v=int(x.op_str.split(', ')[1],16); s=pcstr.cstr(v)
                if s and s in names and x.address in jtarget:
                    codes[s.decode()]=jtarget[x.address]
json.dump({k:hex(v) for k,v in sorted(codes.items())},open(__import__("common").out("errcodes.json"),'w'),indent=0)
print(len(codes),'of',len(names))
for k in ('USER_ERR_USER_NOT_FOUND','ERR_SYSTEM','ERR_COMMAND_NOT_FOUND','ERR_AUTHENTICATION_REQUIRED','GAMEMANAGER_ERR_INVALID_GAME_ID','AUTH_ERR_INVALID_TOKEN','AUTH_ERR_PERSONA_NOT_FOUND'):
    print(k,hex(codes[k]) if k in codes else None)
