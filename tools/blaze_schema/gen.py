import json,re
T=json.load(open(__import__("common").out("tdf.json")))
D=json.load(open(__import__("common").out("dwarf.json"))); E=D['enums']; C=D['classes']
R=json.load(open(__import__("common").out("rpc.json")))
N=json.load(open(__import__("common").out("notifs.json")))
def split_args(s):
    out=[];depth=0;cur=''
    for ch in s:
        if ch=='<': depth+=1
        if ch=='>': depth-=1
        if ch==',' and depth==0: out.append(cur.strip()); cur=''
        else: cur+=ch
    out.append(cur.strip()); return out
def prim(t):
    t=t.replace('const ','').strip()
    if t in T: return 'struct:'+t
    if t.endswith('TdfString') or t=='Blaze::TdfString': return 'string'
    if 'TdfBlob' in t: return 'blob'
    if t in ('float','double'): return 'float'
    if t=='Blaze::BlazeObjectType': return 'objtype'
    if t=='Blaze::BlazeObjectId': return 'objid'
    return 'int'
UNIONS={u for u,c in C.items() if 'Blaze::TdfUnion' in (c['bases'] or [])}
def spec(e):
    w=e['wire']; rt=(e.get('rtype') or '').rstrip('*')
    if w==3: return 'struct:'+rt if rt in T else 'struct:?'+rt
    if w==6: return 'union:'+rt
    if w==4:
        m=re.match(r'Blaze::Tdf(Struct|Primitive)Vector<(.*)>$',rt)
        if m:
            a=split_args(m.group(2))[0]
            if a in UNIONS: return 'list:union:'+a
            return 'list:'+('struct:'+a if m.group(1)=='Struct' else prim(a))
        return 'list:?'+rt
    if w==5:
        m=re.match(r'Blaze::Tdf(Struct|Primitive)Map<(.*)>$',rt)
        if m:
            a=split_args(m.group(2))
            vv=('union:'+a[1]) if a[1] in UNIONS else ('struct:'+a[1] if m.group(1)=='Struct' else prim(a[1]))
            return 'map:'+prim(a[0])+'|'+vv
        return 'map:?'+rt
    return {0:'int',1:'string',2:'blob',7:'variable',8:'objtype',9:'objid',10:'float'}[w]
types={}
bad=[]
for cls,v in T.items():
    ms=[]
    for e in v['members']:
        s=spec(e)
        if '?' in s: bad.append((cls,e['tag'],s))
        m={'tag':e['tag'],'name':e['name'],'spec':s}
        if 'default' in e and e['default'] not in (0,None,''): m['default']=e['default']
        ms.append(m)
    types[cls]=ms
# visit-style TDFs (NFS component, GameReporting NFS reports): schema from
# their visit() functions (tag + TdfVisitor slot), element types from DWARF.
VIS=json.load(open(__import__("common").out("visit.json")))
KW={'vector':4,'map':5,'string':1,'blob':2,'struct':3,'union':6,'variable':7,'objtype':8,'objid':9,'float':10}
for cls,mems in VIS.items():
    if cls in types or not mems or cls.startswith('Blaze::TdfHeat2') or cls=='Blaze::Tdf': continue
    dm={m[0]:m for m in C.get(cls,{}).get('members',[])}
    ms=[]
    for e in sorted(mems,key=lambda e:e['tag'] or ''):
        if not e['tag']: continue
        d=dm.get(e['off'])
        ee={'wire':KW.get(e['kind'],0),'rtype':d[3] if d else None}
        sp=spec(ee)
        ms.append({'tag':e['tag'],'name':(d[1][1:] if d and d[1].startswith('m') else (d[1] if d else '')),'spec':sp,'cpp':e['kind']})
    types[cls]=ms
# PC overrides for visit-style TDFs: the PC build of the NFS component differs
# from the PS3 one (e.g. getSpeedwallOwnedCount takes a BLID list and returns
# SCRS rows). Kinds from the PC visit() calls (MSVC TdfVisitor slots).
PCV=json.load(open(__import__("common").out("pcvisit.json")))
PCSL={0x5c:'vector',0x58:'map',0x54:'bool',0x50:'char',0x4c:'i8',0x48:'u8',0x44:'i16',0x40:'u16',0x3c:'i32',0x38:'u32',0x34:'i64',0x30:'u64',0x2c:'float',0x28:'bitfield',0x24:'string',0x18:'struct',0x14:'union'}
AMBIG={0x10,0x1c,0x20,0x0c,0x08,0x04}
INTK={'bool','char','i8','u8','i16','u16','i32','u32','i64','u64','bitfield','enum','timevalue'}
pcnames=set(PCV)
for cls,mem in PCV.items():
    if not mem: continue
    ps={m['tag']:m for m in types.get(cls,[])}
    psk={e['tag']:e['kind'] for e in VIS.get(cls,[])}
    ms=[]
    for tag,sl in mem:
        kind=PCSL.get(sl)
        if sl in AMBIG:
            kind=psk.get(tag) if psk.get(tag) in INTK else 'enum'
        old=ps.get(tag)
        if kind in INTK: sp='int'
        elif kind=='string': sp='string'
        elif kind=='vector':
            sp=old['spec'] if old and old['spec'].startswith('list:') else None
            if not sp:
                if old and old['spec']=='int': sp='list:int'
                else:
                    rows=[c for c in pcnames if c.startswith(cls.replace('Response','').replace('Request',''))and c.endswith('Row')]
                    sp='list:struct:'+rows[0] if len(rows)==1 else 'list:?'
        elif kind=='map': sp=old['spec'] if old and old['spec'].startswith('map:') else 'map:?'
        elif kind=='struct': sp=old['spec'] if old and old['spec'].startswith('struct:') else 'struct:?'
        else: sp=old['spec'] if old else '?'
        ms.append({'tag':tag,'name':old['name'] if old else '','spec':sp,'cpp':kind,'source':'pc'})
    if [ (m['tag'],m['spec']) for m in ms] != [(m['tag'],m['spec']) for m in types.get(cls,[])]:
        types[cls]=ms
# unions
unions={}
up={}
for k in T: up.setdefault(k.split('::')[-1].upper(),[]).append(k)
for u,c in C.items():
    if 'Blaze::TdfUnion' not in (c['bases'] or []): continue
    vs={}
    for n,i in E.get(u+'::Member',[]):
        if i==127: continue
        nm=n[len('MEMBER_'):]; ns='::'.join(u.split('::')[:-1])
        cands=[x for x in up.get(nm,[]) if x.startswith(ns+'::')] or up.get(nm,[])
        vs[str(i)]=('struct:'+cands[0]) if cands else {'FLOATVALUE':'float','INTVALUE':'int','STRINGVALUE':'string','PS3TICKET':'blob'}.get(nm,'?'+nm)
    unions[u]=vs
cmds={}
for o in R['commands']:
    if o['cid'] is None: continue
    k='%04x:%04x'%(o['cid'],o['cmd'])
    nv=[x for x in o['resp'] if x!='void']
    if len(o['resp'])>1 and len(nv)==1: o['resp']=nv
    cmds[k]={'component':o['component'],'name':o['name'],'response':o['resp'][0] if len(o['resp'])==1 else (o['resp'] or None),'error':(o['err'][0] if o['err'] and o['err'][0]!='void' else None) if len(o['err'])<=1 else o['err'],'request':o['req'][0] if len(o['req'])==1 else (o['req'] or None)}
# Autolog RPCs the PS3 build never calls: types from the generated class names
# (the PC calls 0x11 live). Marked confidence=name.
AUTOLOG_BY_NAME={1:('SingleStoryRequest','SingleStoryResponse'),4:('SpeedwallRequest','SpeedwallResponse'),7:('AggregateSpeedwallRequest','AggregateSpeedwallResponse'),8:('AutologGenerateStoriesRequest',None),10:('RoadRulesRequest','RoadRulesResponse'),11:('BestScoresRequest','BestScoresResponse'),12:('CoreStatSpeedwallRequest','CoreStatSpeedwallResponse'),13:('MultipleStoryRequest','StoryResponse'),0x11:('SpeedWallOwnedRequest','SpeedWallOwnedResponse'),2:('StoryRequest','StoryResponse'),6:('MultipleSpeedwallRequest','MultipleSpeedwallResponse'),0x10:('RivalRequest','RivalResponse'),3:('FriendsPlayRequest','FriendsPlayResponse'),14:('StoryTagRequest','StoryTagResponse'),15:('StoryViewRequest','StoryViewResponse')}
for i,(rq,rs) in AUTOLOG_BY_NAME.items():
    k='0801:%04x'%i
    if k in cmds:
        e=cmds[k]
        if not e.get('request'): e['request']='Blaze::NFS::'+rq; e['request_confidence']='name'
        if not e.get('response') and rs: e['response']='Blaze::NFS::'+rs; e['response_confidence']='name'
# UserSessions.fetchExtendedData(FetchExtendedDataRequest) -> UserSessionExtendedData (generated names)
if '7802:0003' in cmds:
    cmds['7802:0003'].update({'request':'Blaze::FetchExtendedDataRequest','response':'Blaze::UserSessionExtendedData','request_confidence':'name','response_confidence':'name'})
for k,rq in (('7802:000c','Blaze::UserIdentification'),('7802:000d','Blaze::LookupUsersRequest'),('7802:000e','Blaze::LookupUsersByPrefixRequest'),('0019:0001','Blaze::Association::UpdateListMembersRequest'),('0019:0002','Blaze::Association::UpdateListMembersRequest'),('0019:0004','Blaze::Association::UpdateListMembersRequest'),('0019:0005','Blaze::Association::GetListForUserRequest'),('0019:0006','Blaze::Association::GetListsRequest')):
    if k in cmds: cmds[k]['request']=rq; cmds[k]['request_confidence']='signature'
# Redirector: one RPC (PS3 ServiceResolver::sendRequestToRedirector: command 1,
# RpcJob<ServerInstanceInfo, ServerInstanceError>; request ServerInstanceRequest).
cmds['0005:0001']={'component':'Redirector','name':'getServerInstance','request':'Blaze::Redirector::ServerInstanceRequest','response':'Blaze::Redirector::ServerInstanceInfo','error':'Blaze::Redirector::ServerInstanceError'}
COMP={'Authentication':1,'GameManager':4,'Redirector':5,'Playgroups':6,'Stats':7,'Util':9,'CensusData':10,'Clubs':11,'Mail':14,'Messaging':15,'Locker':20,'Rooms':21,'AssociationLists':25,'GameReporting':28,'Autolog':0x801,'UserSessions':0x7802}
nots={}
nn=R['notifnames']
NC={'GameManager::GameManager':'GameManager','UserSessions':'UserSessions','Playgroups::PlaygroupsComponent':'Playgroups','Messaging::MessagingComponent':'Messaging','Association::AssociationListsComponent':'AssociationLists','Stats::StatsComponent':'Stats','Rooms::RoomsComponent':'Rooms'}
for k,v in N.items():
    c=NC.get(k)
    if not c: continue
    for t,ids in v.items():
        for i in ids:
            nm=nn.get(c,{}).get(str(i)) if isinstance(nn.get(c),dict) else None
            nots['%04x:%04x'%(COMP[c],i)]={'component':c,'name':nm,'type':t.lstrip('~'),'confidence':'functor' if not t.startswith('~') else 'ctor'}
NS={'GameManager':'GameManager','UserSessions':'','Playgroups':'Playgroups','Rooms':'Rooms','Messaging':'Messaging','AssociationLists':'Association','Stats':'Stats'}
for c,tab in nn.items():
    for i,nm in tab.items():
        key='%04x:%04x'%(COMP[c],int(i))
        nots.setdefault(key,{'component':c,'name':nm,'type':None})
        nots[key]['name']=nm
        ns=NS.get(c)
        cand=('Blaze::'+ns+'::'+nm) if ns else ('Blaze::'+nm)
        if cand not in T: cand=cand.replace('RemovedFrom','RemoveFrom')
        if cand in T and nots[key].get('confidence')!='functor':
            nots[key]['type']=cand; nots[key]['confidence']='name'
try:
    ERR=json.load(open(__import__("common").out("errcodes.json")))
except FileNotFoundError:
    ERR={}
out={'errors':ERR,'source':'NFS The Run: PS3 NFS.Main_Ps3_Release_unsigned.elf DWARF + TDF_MEMBER_INFO tables, verified against PC Need For Speed The Run.exe member tables (712/716 identical)','wire':{'0':'int','1':'string','2':'blob','3':'struct','4':'list','5':'map','6':'union','7':'variable','8':'objtype','9':'objid','10':'float'},'types':types,'unions':unions,'commands':cmds,'notifications':nots}
json.dump(out,open(__import__("common").out("blaze_schema.json"),'w'),indent=0,sort_keys=True)
print(len(types),'types',len(unions),'unions',len(cmds),'commands',len(nots),'notifications','unresolved specs',len(bad))
for b in bad[:30]: print(b)
