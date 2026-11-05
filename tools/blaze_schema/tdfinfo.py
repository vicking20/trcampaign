import sys,json,re,struct,pickle
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import ps3
L=pickle.load(open(__import__("common").out("blazesyms.pkl"),'rb'))
D=json.load(open(__import__("common").out("dwarf.json"))); C=D['classes']
def tag(w):
    t=w>>8; s=''
    for i in range(4):
        c=(t>>(18-6*i))&0x3f
        if c: s+=chr(c+0x20)
    return s.strip()
SZ={'Blaze::TdfMemberInfo':8,'Blaze::TdfMemberInfoInt':12,'Blaze::TdfMemberInfoClass':12,'Blaze::TdfMemberInfoInt64':16,'Blaze::TdfMemberInfoString':16,'Blaze::TdfMemberInfoEnum':16,'Blaze::TdfMemberInfoTimeValue':16}
WIRE={0:5,1:4,3:0,4:1,6:7,7:0,8:2,9:6,10:3,11:8,12:9,13:0,14:0,15:0,16:0,17:0,18:0,19:0,20:0,21:0,22:0,23:0,24:0,25:10,26:10,27:0}
tabaddr={}
for a,n,d in L:
    m=re.match(r'(Blaze::.*)::(\w+)_TDF_MEMBER_INFO$',d)
    if m and a<0x3800000: tabaddr.setdefault(m.group(1),a)
def cstr(p):
    b=ps3.rd(p,200) if p else None
    return b.split(b'\0')[0].decode(errors='replace') if b else None
out={}
for cls,a in tabaddr.items():
    dfn=C.get(cls+'::TdfMemberInfoDefinition')
    if not dfn: continue
    mem={m[0]:m for m in C.get(cls,{}).get('members',[])}
    ents=[]
    for off,name,ty,rty in dfn['members']:
        b=ps3.rd(a+off,SZ[rty])
        w0,w1=struct.unpack('>II',b[:8])
        kind=w1>>24; flags=(w1>>16)&0xff; moff=w1&0xffff
        e={'tag':tag(w0),'name':re.sub(r'^m|Def$','',name),'kind':kind,'wire':WIRE.get(kind),'flags':flags,'off':moff,'info':rty.split('::')[-1]}
        if rty=='Blaze::TdfMemberInfoInt': e['default']=struct.unpack('>i',b[8:12])[0]
        if rty=='Blaze::TdfMemberInfoInt64': e['default']=struct.unpack('>q',b[8:16])[0]
        if rty=='Blaze::TdfMemberInfoEnum':
            e['default']=struct.unpack('>i',b[8:12])[0]
        if rty=='Blaze::TdfMemberInfoString':
            p,mx=struct.unpack('>II',b[8:16]); e['default']=cstr(p); e['maxlen']=mx
        byname={m[1]:m for m in C.get(cls,{}).get('members',[])}
        dm=byname.get('m'+e['name']) or mem.get(moff) or byname.get('m'+e['name']+'Ptr')
        if dm: e['ctype']=dm[2]; e['rtype']=dm[3]
        ents.append(e)
    out[cls]={'members':ents,'size':C.get(cls,{}).get('size'),'bases':C.get(cls,{}).get('bases')}
json.dump(out,open(__import__("common").out("tdf.json"),'w'),indent=0)
import collections
c=collections.Counter((e['kind'],e['wire']) for v in out.values() for e in v['members'])
print(len(out),sorted(c.items()))
print(json.dumps(out['Blaze::GameManager::NotifyGameSetup'],indent=0)[:1500])
