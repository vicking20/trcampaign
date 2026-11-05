import sys,struct,re,json
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import pcstr
data=pcstr.data; v2o=pcstr.va2off; o2v=pcstr.off2va
def u32(va):
    o=v2o(va); return struct.unpack('<I',data[o:o+4])[0] if o is not None else None
# full class name strings
names={}
for m in re.finditer(rb'\0(Blaze::[A-Za-z0-9_:]+)\0',data):
    names[o2v(m.start()+1)]=m.group(1).decode()
print('names',len(names))
# functions: B8 imm32 C3 returning those
fn={}
for m in re.finditer(rb'\xb8(....)\xc3',data,re.S):
    v=struct.unpack('<I',m.group(1))[0]
    if v in names: fn[o2v(m.start())]=names[v]
print('getFullClassName fns',len(fn))
# pointers to those fns in .rdata = vtable slots
text_fn_ptr={}
for s in pcstr.pe.sections:
    if not s.Name.startswith(b'.rdata'): continue
    base=s.PointerToRawData; arr=data[base:base+s.SizeOfRawData]
    import array
    for k in range(0,len(arr)-4,4):
        v=struct.unpack_from('<I',arr,k)[0]
        if v in fn: text_fn_ptr.setdefault(fn[v],[]).append(o2v(base+k))
print('slots',len(text_fn_ptr))
def ret_imm(fva):
    o=v2o(fva)
    if o is None: return None
    b=data[o:o+6]
    if len(b)==6 and b[0]==0xB8 and b[5]==0xC3: return struct.unpack('<I',b[1:5])[0]
    return None
out={}
for name,slots in text_fn_ptr.items():
    for sl in slots:
        for d in range(-12,13):
            f=u32(sl+4*d)
            t=ret_imm(f) if f else None
            if t and t not in names and v2o(t) is not None:
                w0=u32(t)
                if w0 and (w0&0xff)==0 and 0<(w0>>8)<0x1000000 and data[v2o(t)+4]<=27:
                    out[name]={'table':t,'d':d}; break
        if name in out: break
json.dump({k:{'table':hex(v['table']),'d':v['d']} for k,v in out.items()},open(__import__("common").out("pcvt.json"),'w'),indent=0)
import collections
print('tables',len(out),collections.Counter(v['d'] for v in out.values()).most_common(5))
