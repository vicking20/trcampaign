import json,re,collections
S=json.load(open(__import__("common").out("sites.json")))
SW=json.load(open(__import__("common").out("pcswitch2.json")))
T=json.load(open(__import__("common").out("tdf.json")))
COMP={'Authentication':1,'GameManager':4,'Redirector':5,'Playgroups':6,'Stats':7,'Util':9,'CensusData':10,'Clubs':11,'Mail':14,'Messaging':15,'Locker':20,'Rooms':21,'AssociationLists':25,'GameReporting':28,'Autolog':0x801,'UserSessions':0x7802}
# PC name tables: (jmp addr) -> component
first2comp={'createAccount':'Authentication','getSingleStory':'Autolog','subscribeToCensusData':'CensusData','getStatDescs':'Stats','fetchExtendedData':'UserSessions','fetchClientConfig':'Util','createGame':'GameManager','addUsersToList':'AssociationLists','createContent':'Locker','updateMailSettings':'Mail','selectViewUpdates':'Rooms','sendMessage':'Messaging','createPlaygroup':'Playgroups',
 'NotifyMatchmakingFailed':'GameManager!','UserSessionExtendedDataUpdate':'UserSessions!','NotifyDestroyPlaygroup':'Playgroups!','RoomViewUpdatedNotification':'Rooms!'}
names={};notnames={}
gr=[r for r in SW if r['names'] and list(r['names'].values())[0]=='submitGameReport']
for r in SW:
    if not r['names']: continue
    f=list(r['names'].values())[0]
    c=first2comp.get(f)
    if f=='submitGameReport':
        c='GameReporting' if 'getGameReportQuery' in r['names'].values() else 'GameReportingLegacy'
    if not c: continue
    tgt=notnames if c.endswith('!') else names
    tgt[c.rstrip('!')]={int(k):v for k,v in r['names'].items()}
NSMAP={'Util':'Util','Authentication':'Authentication','GameManager':'GameManager','Redirector':'Redirector','Playgroups':'Playgroups','Stats':'Stats','Messaging':'Messaging','Locker':'Locker','Rooms':'Rooms','Association':'AssociationLists','GameReporting':'GameReporting','NFS':'Autolog','Mail':'Mail','CensusData':'CensusData','Rsp':'Rsp','Clubs':'Clubs'}
def comp_of(r):
    for src in (r.get('job') or '', r['caller']):
        m=re.search(r'Blaze::(\w+)::',src.replace('Blaze::RpcJob','X').replace('Blaze::JobId','X'))
        for mm in re.finditer(r'Blaze::(\w+)::',src):
            ns=mm.group(1)
            if ns in NSMAP: return NSMAP[ns]
    if 'UserManager::LocalUser' in r['caller'] or 'UserManager' in r['caller'] or 'UserSessions::' in r['caller']: return 'UserSessions'
    if 'ConnectionManager' in r['caller'] or 'QosManager' in r['caller']: return 'Util'
    return None
rpc=collections.defaultdict(dict)
for r in S:
    if r['cmd'] is None or not r['job']: continue
    m=re.match(r'Blaze::RpcJob\d*<(.*)>$',r['job'])
    args=[]; depth=0; cur=''
    for ch in m.group(1):
        if ch=='<': depth+=1
        if ch=='>': depth-=1
        if ch==',' and depth==0: args.append(cur.strip()); cur=''
        else: cur+=ch
    args.append(cur.strip())
    resp,err=args[0],args[1]
    c=comp_of(r)
    # Util callers in ConnectionManager: check cmd names
    req=None
    mm=re.search(r'>\((Blaze::[\w:]+) const&',r['caller']) or re.search(r'::\w+\((Blaze::[\w:]+) const&',r['caller'])
    if mm and mm.group(1) in T and 'Functor' not in mm.group(1): req=mm.group(1)
    elif r['reqs']:
        cand=[x for x in r['reqs'] if x in T and (x.endswith('Request') or x.endswith('Parameters'))]
        if cand: req=cand[-1]
    e=rpc[(c,r['cmd'])]
    e.setdefault('resp',set()).add(resp); e.setdefault('err',set()).add(err)
    if req: e.setdefault('req',set()).add(req)
    e.setdefault('callers',set()).add(r['caller'].split('(')[0])
J=json.load(open(__import__("common").out("jobsites.json")))
for cmd,job,caller in J:
    if cmd is None or not job: continue
    m=re.match(r'Blaze::RpcJob\w*<(.*)>$',job)
    if not m: continue
    args=[]; depth=0; cur=''
    for ch in m.group(1):
        if ch=='<': depth+=1
        if ch=='>': depth-=1
        if ch==',' and depth==0: args.append(cur.strip()); cur=''
        else: cur+=ch
    args.append(cur.strip())
    c=comp_of({'job':job,'caller':caller})
    if 'UserSessions::' in caller: c='UserSessions'
    if 'LoginManager' in caller and 'Util::PostAuth' in job: c='Util'
    if 'LoginManager' in caller and c is None: c='Authentication'
    e=rpc[(c,cmd)]
    e.setdefault('resp',set()).add(args[0]); e.setdefault('err',set()).add(args[1])
    e.setdefault('callers',set()).add(caller.split('(')[0])
out=[]
for comp,tab in names.items():
    for cid,nm in sorted(tab.items()):
        e=rpc.get((comp,cid),{})
        out.append({'component':comp,'cid':COMP.get(comp),'cmd':cid,'name':nm,'resp':sorted(e.get('resp',[])),'err':sorted(e.get('err',[])),'req':sorted(e.get('req',[])),'callers':sorted(e.get('callers',[]))[:4]})
json.dump({'commands':out,'notifnames':{k:v for k,v in notnames.items()}},open(__import__("common").out("rpc.json"),'w'),indent=0,default=list)
for o in out:
    if o['resp'] or o['req']: print(o['component'],hex(o['cmd']),o['name'],o['req'],'->',o['resp'],o['err'] if o['err']!=['void'] else '')
print(sum(1 for o in out if o['resp']),'of',len(out))
