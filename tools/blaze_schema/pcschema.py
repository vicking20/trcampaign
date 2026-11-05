import sys,struct,json,re
import os;sys.path.insert(0,os.path.dirname(os.path.abspath(__file__))); import pcstr
data=open(__import__("common").out("nfs_inited.bin"),"rb").read(); v2o=pcstr.va2off
PV=json.load(open(__import__("common").out("pcvt.json")))
T=json.load(open(__import__("common").out("tdf.json")))
INFO={0:'I',1:'I',3:'E',4:'S',6:'I',7:'I',8:'I',9:'I',10:'C',11:'I',12:'I',13:'TV',14:'N',15:'N',16:'N',17:'N',18:'N',19:'N',20:'N',21:'N',22:'L',23:'L',24:'L',25:'N',26:'N',27:'N'}
SIZE={'I':(8,4),'N':(12,4),'C':(12,4),'L':(16,8),'S':(16,4),'E':(16,4),'TV':(16,8)}
WIRE={0:5,1:4,3:0,4:1,6:7,7:0,8:2,9:6,10:3,11:8,12:9,13:0,14:0,15:0,16:0,17:0,18:0,19:0,20:0,21:0,22:0,23:0,24:0,25:10,26:10,27:0}
def untag(w):
    t=w>>8; s=''
    for i in range(4):
        c=(t>>(18-6*i))&0x3f
        if c: s+=chr(c+0x20)
    return s
def cstr(va):
    o=v2o(va) if va else None
    if o is None: return None
    e=data.find(b'\0',o); return data[o:e].decode(errors='replace')
import bisect
starts=sorted(int(v['table'],16) for v in PV.values())
out={}
for cls,v in PV.items():
    if v['d']!=3: continue
    t=int(v['table'],16); o=v2o(t); pos=0; ms=[]
    j=bisect.bisect_right(starts,t); limit=(starts[j]-t) if j<len(starts) else 4096
    ps3={e['tag']:e for e in T.get(cls,{}).get('members',[])}
    while True:
        if pos%8==4 and struct.unpack_from('<I',data,o+pos)[0]==0 and data[o+pos+8] in (13,22,23,24):
            pos+=4
        if pos>=limit: break
        w0=struct.unpack_from('<I',data,o+pos)[0]
        kind=data[o+pos+4]; flags=data[o+pos+5]; off=struct.unpack_from('<H',data,o+pos+6)[0]
        if w0==0 or w0&0xff or kind not in INFO: break
        if ms and (w0>>8)<=ms[-1]['_w']: break
        inf=INFO[kind]; sz,al=SIZE[inf]
        if pos%al: pos+=al-pos%al; continue
        e={'_w':w0>>8,'tag':untag(w0),'kind':kind,'wire':WIRE[kind],'flags':flags,'off':off}
        if inf=='N': e['default']=struct.unpack_from('<i',data,o+pos+8)[0]
        if inf=='L': e['default']=struct.unpack_from('<q',data,o+pos+8)[0]
        if inf=='E': e['default']=struct.unpack_from('<i',data,o+pos+8)[0]
        if inf=='S': e['default']=cstr(struct.unpack_from('<I',data,o+pos+8)[0]); e['maxlen']=struct.unpack_from('<I',data,o+pos+12)[0]
        p=ps3.get(e['tag'])
        if p:
            e['name']=p['name']; e['rtype']=p.get('rtype'); e['ctype']=p.get('ctype')
            if p['kind']!=kind: e['ps3kind']=p['kind']
        ms.append(e); pos+=sz
        if len(ms)>120: break
    for m in ms: m.pop('_w',None)
    out[cls]={'table':v['table'],'members':ms,'ps3_only':[x for x in ps3 if x not in {m['tag'] for m in ms}]}
json.dump(out,open(__import__("common").out("pcschema.json"),'w'),indent=0)
asc=sum(1 for c in out.values() if all(untag(0)<=a['tag'] for a in c['members']))
diff=[c for c,v in out.items() if v['ps3_only'] or any('ps3kind' in m or 'name' not in m for m in v['members'])]
print('classes',len(out),'differ from PS3',len(diff))
for c in diff[:80]:
    v=out[c]; print(c.replace('Blaze::',''),'PS3-only:',v['ps3_only'],'PC-only:',[m['tag'] for m in v['members'] if 'name' not in m],'kind:',[(m['tag'],m['ps3kind'],m['kind']) for m in v['members'] if 'ps3kind' in m])
