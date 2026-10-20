"""Tiny local web UI for the launcher (stdlib only, no tkinter needed). `nfstr_launcher.py gui` opens it."""
import json, os, re, subprocess, sys, threading, webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
LAUNCHER = os.path.join(HERE, "nfstr_launcher.py")
STATE = {"proc": None, "mode": "idle", "code": "", "log": []}
LOCK = threading.Lock()

try:
    sys.path.insert(0, HERE)
    import modes as _modes
    MODE_NAMES = tuple(_modes.MODES)
    MODE_OPTIONS = "".join("<option value=%s>%s - %s</option>" % (k, k, v[2][:64].replace("<", "&lt;")) for k, v in _modes.MODES.items())
except ImportError:
    MODE_NAMES, MODE_OPTIONS = (), ""

PAGE = """<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>NFS The Run LAN</title>
<style>
:root{--bg:#14161a;--fg:#e8eaed;--mut:#9aa0a6;--card:#1d2026;--acc:#ff8a00;--bd:#2c3038}
@media (prefers-color-scheme:light){:root{--bg:#f4f5f7;--fg:#1b1d21;--mut:#5f6368;--card:#fff;--acc:#d96f00;--bd:#d9dce1}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px system-ui,sans-serif}
main{max-width:640px;margin:0 auto;padding:16px}
h1{font-size:20px;margin:8px 0 16px} h2{font-size:15px;margin:0 0 10px;color:var(--mut);font-weight:600;text-transform:uppercase;letter-spacing:.04em}
.card{background:var(--card);border:1px solid var(--bd);border-radius:10px;padding:14px;margin-bottom:14px}
label{display:block;margin:8px 0 3px;color:var(--mut);font-size:13px}
input,select{width:100%;box-sizing:border-box;padding:9px;border-radius:7px;border:1px solid var(--bd);background:var(--bg);color:var(--fg);font:inherit}
button{margin-top:12px;padding:10px 16px;border:0;border-radius:7px;background:var(--acc);color:#111;font:inherit;font-weight:600;cursor:pointer}
button.sec{background:transparent;color:var(--fg);border:1px solid var(--bd)} button:disabled{opacity:.5}
.row{display:flex;gap:10px}.row>*{flex:1}
code{display:block;word-break:break-all;background:var(--bg);border:1px solid var(--bd);padding:9px;border-radius:7px;margin-top:6px}
#sttab td,#sttab th,#ldtab td,#ldtab th{padding:5px 6px;text-align:left;border-bottom:1px solid var(--bd)} #sttab th{color:var(--mut);font-weight:600;font-size:12px} #sttab .out{opacity:.5;text-decoration:line-through} #sttab .num,#ldtab .num{text-align:right}
#status{font-weight:600} pre{max-height:140px;overflow:auto;font-size:12px;color:var(--mut);white-space:pre-wrap}
</style>
<main>
<h1>NFS The Run - LAN</h1>
<div class=card><h2>You</h2><label>Nickname (max 16)</label><input id=nick maxlength=16></div>
<div class=card><h2>Lobby</h2><label>Lobby address</label><input id=lurl placeholder="http://host:26100">
<div class=row><button class=sec id=lref>Refresh list</button><button class=sec id=lladder>Season ladder</button></div><div id=games style="margin-top:10px"></div><div id=ladder style="margin-top:10px"></div></div>
<div class=card><h2>Join a game</h2><label>Join code or host:port</label><input id=code placeholder="NFSTR1-...">
<button id=join>Join</button></div>
<div class=card><h2>Host a game</h2>
<label>Server name</label><input id=sname value="NFSTR LAN">
<label>Playlist</label><select id=pl></select>
<label>First session</label><select id=ss></select>
<label>Register in the lobby (optional)</label><input id=hlobby placeholder="same as lobby address to list your game">
<div class=row><div><label>Game mode</label><select id=gmode><option value="">Plain playlist (no scoring)</option>@@MODEOPTS@@</select></div>
<div><label>Cannonball points</label><select id=gpts><option value=field>One per player (N..1)</option><option value=top3>5 / 3 / 1</option><option value=fixed>10, 8, 6, 4, 3, 2, 1</option></select></div></div>
<label>Eliminated players (elimination mode)</label><select id=gelim><option value="">Keep driving, marked OUT</option><option value=remove>Remove from the session (experimental)</option></select>\n<label>Teams (team mode only, optional)</label><input id=gteams placeholder="Red=Ana,Bo;Blue=Cy,Di  (unlisted players join the smaller team)">
<label>Race start</label><select id=smode><option value=manual>I start it (button below)</option><option value=auto>Automatically when the join window ends</option></select>
<div class=row><div><label>Join window (seconds, auto mode)</label><input id=pre type=number value=60></div>
<div><label>Public address (optional)</label><input id=addr placeholder="auto (LAN)"></div></div>
<button id=host>Start server</button></div>
<div class=card id=stcard style="display:none"><h2>Standings</h2><div id=stinfo style="color:var(--mut);margin-bottom:6px"></div><table id=sttab style="width:100%;border-collapse:collapse"></table></div>
<div class=card><h2>Status</h2><div id=status>Idle</div><div id=srv style="color:var(--mut);margin-top:4px"></div><button id=startnow style="display:none">Start race now</button><button id=lnow style="display:none">Launch for everyone</button><div id=codebox style="display:none">Send this to your players:<code id=outcode></code><button class=sec id=copy>Copy</button></div>
<pre id=log></pre><button class=sec id=stop>Stop</button></div>
</main>
<script>
const $=id=>document.getElementById(id); let cat=[];
async function api(p,b){const r=await fetch(p,b?{method:'POST',body:JSON.stringify(b)}:{});return r.json()}
function fillSessions(){const p=cat[$('pl').value];$('ss').innerHTML='<option value="">Game decides</option>'+p.sessions.map((s,i)=>`<option value=${i}>${i}: T${s.tier} ${s.name}</option>`).join('')}
async function init(){const s=await api('/api/state');cat=s.catalog;$('nick').value=s.nick||'';
 $('pl').innerHTML=cat.map((p,i)=>`<option value=${i}>${p.name} (${p.sessions.length})</option>`).join('');$('pl').value=cat.findIndex(p=>p.array==0&&p.id==4);fillSessions();poll()}
$('pl').onchange=fillSessions;
async function poll(){const s=await api('/api/state');const busy=s.mode!='idle';
 $('status').textContent={idle:'Idle',host:'Hosting',join:'Playing'}[s.mode];$('host').disabled=$('join').disabled=busy;
 $('codebox').style.display=s.code?'block':'none';$('srv').textContent=s.server||'';$('lnow').style.display=(s.mode=='host'&&s.lobby)?'inline-block':'none';$('startnow').style.display=(s.mode=='host'&&/state=2/.test(s.server||''))?'inline-block':'none';$('outcode').textContent=s.code;$('log').textContent=s.log.join('\\n');renderStandings(s.standings);setTimeout(poll,1500)}
function esc(t){return String(t).replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]))}
function fmt(t){return Math.floor(t/60)+':'+(t%60).toFixed(2).padStart(5,'0')}
function renderStandings(st){const c=$('stcard');if(!st||!st.rows||!st.rows.length){c.style.display='none';return}
 c.style.display='block';$('stinfo').innerHTML=esc(st.mode)+(st.scheme&&st.mode!='timeattack'&&st.mode!='elimination'?' / '+esc(st.scheme):'')+' - '+st.races+' race'+(st.races==1?'':'s')+' finished'+(st.teams?'<br><b>'+st.teams.map(t=>esc(t.team)+' '+t.points).join(' &nbsp;|&nbsp; ')+'</b>':'');
 $('sttab').innerHTML='<tr><th>#</th><th>Player</th><th class=num>Points</th><th class=num>Best</th><th class=num>Total</th><th class=num>Wins</th></tr>'+st.rows.map((r,i)=>`<tr class="${r.out?'out':''}"><td>${i+1}</td><td>${esc(r.name)}${r.team?' ['+esc(r.team)+']':''}${r.out?' (out)':''}${r.bonus?' +'+r.bonus+'b':''}</td><td class=num>${r.points}</td><td class=num>${r.best==null?'-':fmt(r.best)}</td><td class=num>${fmt(r.total)}</td><td class=num>${r.wins}</td></tr>`).join('')}
$('join').onclick=async()=>{if(!$('nick').value){alert('Choose a nickname first');return}
 const r=await api('/api/join',{nick:$('nick').value,code:$('code').value});if(r.error)alert(r.error)};
$('startnow').onclick=()=>api('/api/ctl',{cmd:'start'});
$('host').onclick=async()=>{if(!$('nick').value){alert('Choose a nickname first');return}
 const p=cat[$('pl').value];const r=await api('/api/host',{nick:$('nick').value,array:p.array,playlist:p.id,session:$('ss').value,name:$('sname').value,preload:$('pre').value,start:$('smode').value,gelim:$('gelim').value,lobby:$('hlobby').value,address:$('addr').value,gmode:$('gmode').value,gpts:$('gpts').value,gteams:$('gteams').value});if(r.error)alert(r.error)};
$('stop').onclick=()=>api('/api/stop',{});$('copy').onclick=()=>navigator.clipboard.writeText($('outcode').textContent);
$('lref').onclick=async()=>{const u=$('lurl').value;localStorage.setItem('lurl',u);const r=await api('/api/lobby/list',{url:u});
 if(r.error){$('games').textContent=r.error;return}
 $('games').innerHTML=r.games.length?r.games.map(g=>`<div class=row style="align-items:center;margin:6px 0"><div><b>${esc(g.name)}</b><br><span style="color:var(--mut)">${g.playlist}${g.mode?' - '+esc(g.mode)+(g.scheme?'/'+esc(g.scheme):''):''} - ${g.host} - ${g.players.length}/${g.max} - ${g.state}${g.state=='launching'?' ('+Math.max(0,Math.round(g.launch_at-g.now))+'s)':''}${g.board&&g.board.length?'<br>Race '+g.races+': '+g.board.slice(0,3).map((r,i)=>(i+1)+'. '+esc(r[0])+' '+r[1]).join(' | '):''}</span></div><div style="flex:0"><button data-id="${g.id}" class=jb ${g.state!='open'?'disabled':''}>Join</button></div></div>`).join(''):'No games right now';
 document.querySelectorAll('.jb').forEach(b=>b.onclick=async()=>{if(!$('nick').value){alert('Choose a nickname first');return}const r=await api('/api/lobby/join',{url:u,id:b.dataset.id,nick:$('nick').value});if(r.error)alert(r.error)})};
$('lladder').onclick=async()=>{const u=$('lurl').value;const r=await api('/api/lobby/ladder',{url:u});
 if(r.error){$('ladder').textContent=r.error;return}
 $('ladder').innerHTML='<div style="color:var(--mut);margin-bottom:4px">Season '+esc(r.season)+' - '+r.games+' game(s) counted</div>'+(r.entries.length?'<table style="width:100%;border-collapse:collapse" id=ldtab><tr><th>#</th><th>Player</th><th class=num>Games</th><th class=num>Gold</th><th class=num>Silver</th><th class=num>Bronze</th><th class=num>Points</th></tr>'+r.entries.slice(0,15).map((e,i)=>`<tr><td>${i+1}</td><td>${esc(e.nick)}</td><td class=num>${e.games}</td><td class=num>${e.gold}</td><td class=num>${e.silver}</td><td class=num>${e.bronze}</td><td class=num>${e.points}</td></tr>`).join('')+'</table>':'No finished games yet')};
$('lnow').onclick=()=>api('/api/lobby/launch',{});
try{$('lurl').value=localStorage.getItem('lurl')||''}catch(e){}
init();
</script>"""
PAGE = PAGE.replace("@@MODEOPTS@@", MODE_OPTIONS)


def _reader(proc):
    for line in proc.stdout:
        line = line.rstrip()
        m = re.search(r"(NFSTR1-[\w\-]+)", line)
        with LOCK:
            if m and STATE["mode"] == "host":
                STATE["code"] = m.group(1)
            if line and not line.startswith(("INFO:", "ProtonFixes")):
                STATE["log"] = (STATE["log"] + [line])[-30:]
    with LOCK:
        STATE.update(mode="idle", proc=None)


def start(args, mode):
    with LOCK:
        if STATE["proc"]:
            return {"error": "Already running"}
        STATE.update(mode=mode, code="", log=[])
        p = subprocess.Popen([sys.executable, "-u", LAUNCHER] + args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        STATE["proc"] = p
    threading.Thread(target=_reader, args=(p,), daemon=True).start()
    return {"ok": True}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass

    def _send(self, obj, ctype="application/json"):
        body = obj if isinstance(obj, bytes) else json.dumps(obj).encode()
        self.send_response(200); self.send_header("Content-Type", ctype); self.send_header("Content-Length", str(len(body))); self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/api/state":
            sys.path.insert(0, HERE)
            import nfstr_launcher as L
            with LOCK:
                mode = STATE["mode"]
            srv = L.server_ctl("status", 0.3) if mode == "host" else ""
            nice = ""
            if srv.startswith("state="):
                d = dict(x.split("=") for x in srv.split())
                nice = {"2": "Waiting for players (race not started)", "3": "Loading the race", "6": "Racing"}.get(d["state"], "state " + d["state"]) + "  " + srv
                if d.get("waiting", "0") not in ("0", ""):
                    nice += "  -  %s player(s) waiting outside, they join at the next race break" % d["waiting"]
            standings = None
            if mode == "host":
                try:
                    import modes
                    st = json.load(open(L.standings_path()))
                    standings = {"mode": st["mode"], "scheme": st.get("scheme", ""), "races": st["races"],
                                 "teams": [dict(team=t, points=pts, members=m) for t, pts, m in modes.team_ranking(st)] if st.get("teams") is not None else None,
                                 "rows": [dict(name=n, points=p["points"], best=p["best"], total=p["total"], wins=p["wins"], out=p["eliminated"], team=p.get("team"), bonus=p.get("bonus", 0)) for n, p in modes.ranking(st)]}
                except (OSError, ValueError, KeyError):
                    pass
            with LOCK:
                self._send({"standings": standings, "lobby": STATE.get("lobby", ""), "server": nice, "mode": STATE["mode"], "code": STATE["code"], "log": STATE["log"],
                            "nick": L.load_user().get("nick", ""), "catalog": L.load_catalog()})
        else:
            self._send(PAGE.encode(), "text/html; charset=utf-8")

    def do_POST(self):
        b = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        if self.path == "/api/join":
            sys.path.insert(0, HERE)
            import nfstr_launcher as L
            try:
                L.parse_code(b["code"])
            except ValueError as e:
                return self._send({"error": str(e)})
            self._send(start(["join", b["code"], "--nick", b["nick"]], "join"))
        elif self.path == "/api/host":
            a = ["host", "--start", b.get("start", "manual"), "--array", str(b["array"]), "--playlist", str(b["playlist"]), "--nick", b["nick"],
                 "--name", b["name"] or "NFSTR LAN", "--preload", str(b["preload"] or 80)]
            if str(b.get("session", "")) != "":
                a += ["--session", str(b["session"])]
            if b.get("address"):
                a += ["--address", b["address"]]
            if b.get("gmode") in MODE_NAMES:
                a += ["--mode", b["gmode"], "--points", b.get("gpts") if b.get("gpts") in ("field", "top3", "fixed") else "field"]
                if b.get("gelim") == "remove" and b["gmode"] in ("elimination", "halving"):
                    a += ["--enforce-elim"]
                if b["gmode"] in _modes.TEAM_MODES and str(b.get("gteams", "")).strip():
                    a += ["--teams", str(b["gteams"])[:200]]
            if b.get("lobby"):
                a += ["--lobby", b["lobby"]]
                STATE["lobby"] = b["lobby"]
            else:
                STATE["lobby"] = ""
            sys.path.insert(0, HERE)
            import nfstr_launcher as L
            u = L.load_user(); u["nick"] = b["nick"]; L.save_user(u)
            self._send(start(a, "host"))
        elif self.path == "/api/lobby/list":
            sys.path.insert(0, HERE)
            import nfstr_launcher as L
            r = L.lobby_call(b.get("url", ""), "GET", "/api/games")
            self._send(r)
        elif self.path == "/api/lobby/ladder":
            sys.path.insert(0, HERE)
            import nfstr_launcher as L
            self._send(L.lobby_call(b.get("url", ""), "GET", "/api/ladder"))
        elif self.path == "/api/lobby/join":
            self._send(start(["lobby", "join", b["url"], b["id"], "--nick", b["nick"]], "join"))
        elif self.path == "/api/lobby/launch":
            subprocess.call([sys.executable, LAUNCHER, "lobby", "launch"])
            self._send({"ok": True})
        elif self.path == "/api/ctl":
            sys.path.insert(0, HERE)
            import nfstr_launcher as L
            self._send({"reply": L.server_ctl(b["cmd"] if b.get("cmd") in ("start", "status") else "status")})
        elif self.path == "/api/stop":
            subprocess.call([sys.executable, LAUNCHER, "stop"])
            with LOCK:
                if STATE["proc"]:
                    STATE["proc"].terminate()
            self._send({"ok": True})


def run(port=8765):
    srv = ThreadingHTTPServer(("127.0.0.1", port), H)
    url = "http://127.0.0.1:%d/" % port
    print("Launcher UI:", url)
    try:
        webbrowser.open(url)
    except Exception:
        pass
    srv.serve_forever()


if __name__ == "__main__":
    run()
