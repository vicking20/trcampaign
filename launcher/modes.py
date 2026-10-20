"""Game modes = scoring/rule layers on top of the playlist races (pure functions, testable without the game).

A mode gets the finished race as an ordered list of (name, seconds) - finishers first by time, DNF (seconds 0) last -
and the current standings, and returns a list of human readable events. Standings are plain dicts:

  {"mode": "cannonball", "races": N, "players": {name: {"points": 0, "total": 0.0, "best": None, "wins": 0,
                                                       "races": 0, "dnf": 0, "eliminated": False}}}
Team modes add  "teams": {player: team}  and  "team_names": [..]  to the standings.
Add a mode: write `apply(st, results)` + `ranking(st)` and register it in MODES.
"""
POINTS = [10, 8, 6, 4, 3, 2, 1]          # cannonball: points per place
BOUNTY = 3                                 # bounty mode: bonus points for beating the points leader
COMEBACK = 1                               # bounty mode: bonus for the slowest finisher of a race with 3+ finishers
STREAK_BONUS = 2                           # streak mode: bonus per extra consecutive win (1st repeat +2, 2nd +4, ...)
STREAK_CAP = 3                             # streak mode: the streak bonus stops growing after this many repeats
SURVIVOR_PENALTY = 2                       # survivor mode: points lost by a non-finisher and by the last finisher (3+ finishers)
RELAY_N = 2                                # team relay: the team's race time is the sum of its N fastest members
RELAY_POINTS = [10, 6, 3, 1]               # team relay: team points per team place in a race
RIVAL_BONUS = 2                            # rivals mode: bonus for finishing ahead of your rival (the player ranked just above you)
JACKPOT_EVERY = 3                          # jackpot mode: every Nth race counts double
UNDERDOG_FACTOR = 1.5                      # underdog mode: points multiplier for the bottom third of the standings (3+ players)
HARE_CAUGHT = 3                            # hare mode: bonus for each hound that finishes ahead of the hare (the previous race winner)
HARE_ESCAPE = 1                            # hare mode: bonus for the hare per hound it beats
PLAGUE_SHARE = 0.5                         # plague mode: infected players keep this share of a race's points
MARGIN_MAX = 10                           # margin mode: points for the winner; every 2 s behind the winner costs 1 point
TEAM_MODES = ("teamcannonball", "teamrelay", "teamcaptain")
DNF_PENALTY = 60.0                         # seconds added to the total time of a non-finisher (on top of the slowest finisher)


SCHEMES = {
    "field": "one point per player in that race: 1st gets N, last finisher 1 (N = players present at the results), DNF 0",
    "top3": "5 / 3 / 1 for the first three finishers, nothing else",
    "fixed": "10, 8, 6, 4, 3, 2, 1 for places 1-7",
}


def points_for(place, n, scheme):
    """place: 0-based finishing place among finishers, n: number of players in that race."""
    if scheme == "field":
        return max(n - place, 1)
    if scheme == "top3":
        return (5, 3, 1)[place] if place < 3 else 0
    return POINTS[place] if place < len(POINTS) else 0


def parse_teams(text):
    """'Red=Ana,Bo;Blue=Cy,Di' -> ({'Ana': 'Red', ...}, ['Red', 'Blue']). Empty text -> two empty teams Red and Blue."""
    teams, names = {}, []
    for part in (text or "").split(";"):
        if "=" not in part:
            continue
        t, members = part.split("=", 1)
        t = t.strip()[:12]
        if not t or t in names:
            continue
        names.append(t)
        for m in members.split(","):
            if m.strip():
                teams[m.strip()[:16]] = t
    for default in ("Red", "Blue"):
        if len(names) < 2 and default not in names:
            names.append(default)
    return teams, names


def new_standings(mode, scheme="field", teams_text=None):
    st = {"mode": mode, "scheme": scheme, "races": 0, "players": {}}
    if mode in TEAM_MODES:
        st["teams"], st["team_names"] = parse_teams(teams_text)
    return st


def _team_of(st, name):
    """Team of a player (team modes): the listed one, else the team with the fewest members so far."""
    teams = st.get("teams")
    if teams is None:
        return None
    if name not in teams:
        count = {t: 0 for t in st["team_names"]}
        for t in teams.values():
            count[t] = count.get(t, 0) + 1
        teams[name] = min(st["team_names"], key=lambda t: (count.get(t, 0), st["team_names"].index(t)))
    return teams[name]


def _player(st, name):
    p = st["players"].setdefault(name, {"points": 0, "total": 0.0, "best": None, "wins": 0, "races": 0, "dnf": 0, "eliminated": False, "bonus": 0})
    if st.get("teams") is not None:
        p["team"] = _team_of(st, name)
    return p


def _active(results, st):
    return [(n, t) for n, t in results if not st["players"].get(n, {}).get("eliminated")]


def _book(st, name, t, slowest):
    p = _player(st, name)
    p["races"] += 1
    if t > 0:
        p["total"] += t
        p["best"] = t if p["best"] is None else min(p["best"], t)
    else:
        p["dnf"] += 1
        p["total"] += slowest + DNF_PENALTY
    return p


# ---------------------------------------------------------------- cannonball: points per place, total time as tiebreak
def cannonball(st, results):
    ev = []
    slowest = max([t for _, t in results] + [0.0])
    field = max(len(results), st.get("field", 0))       # a win keeps its value when players drop out (biggest field seen counts)
    st["field"] = field
    for place, (name, t) in enumerate(results):
        p = _book(st, name, t, slowest)
        if t > 0:
            pts = points_for(place, field, st.get("scheme", "field"))
            p["points"] += pts
            if place == 0:
                p["wins"] += 1
            ev.append("%d. %s %s  +%d" % (place + 1, name, fmt(t), pts))
        else:
            ev.append("DNF %s  +0" % name)
    st["races"] += 1
    return ev


def rank_cannonball(st):
    return sorted(st["players"].items(), key=lambda kv: (-kv[1]["points"], kv[1]["total"]))


# ---------------------------------------------------------------- team cannonball: same points per place, the team total counts
def team_points(st):
    """{team: points} over all players (also teams without a player yet)."""
    tot = {t: 0 for t in st.get("team_names", [])}
    if st.get("mode") == "teamrelay":                  # relay: team totals come from the team results, not from member points
        tot.update(st.get("relay", {}))
        return tot
    for p in st["players"].values():
        tot[p.get("team")] = tot.get(p.get("team"), 0) + p["points"]
    return tot


def team_ranking(st):
    """[(team, points, [members])] best first; ties are broken by the team's total race time (lower is better)."""
    pts, total, members = team_points(st), {}, {}
    for n, p in st["players"].items():
        total[p.get("team")] = total.get(p.get("team"), 0.0) + p["total"]
        members.setdefault(p.get("team"), []).append(n)
    return sorted(((t, pts[t], sorted(members.get(t, []))) for t in pts), key=lambda r: (-r[1], total.get(r[0], 0.0)))


def team_cannonball(st, results):
    ev = cannonball(st, results)
    ev.append("TEAMS  " + "   ".join("%s %d" % (t, pts) for t, pts, _ in team_ranking(st)))
    return ev


# ---------------------------------------------------------------- bounty: the leader carries a price, beating the leader pays; the slowest finisher gets a small catch-up bonus
def bounty(st, results):
    ranked = rank_cannonball(st) if st["races"] else []
    leader = ranked[0][0] if ranked else None
    times = dict(results)
    ev = cannonball(st, results)                      # normal points first
    finishers = [(n, t) for n, t in results if t > 0]
    if leader in times and len(results) > 1:
        lt = times[leader]
        for n, t in finishers:
            if n != leader and (lt == 0 or t < lt):
                p = _player(st, n); p["points"] += BOUNTY; p["bonus"] = p.get("bonus", 0) + BOUNTY
                ev.append("BOUNTY  %s beat leader %s  +%d" % (n, leader, BOUNTY))
    if len(finishers) >= 3:
        n = finishers[-1][0]
        p = _player(st, n); p["points"] += COMEBACK; p["bonus"] = p.get("bonus", 0) + COMEBACK
        ev.append("COMEBACK  %s  +%d" % (n, COMEBACK))
    return ev


# ---------------------------------------------------------------- time attack: best single race time counts
def time_attack(st, results):
    ev = []
    slowest = max([t for _, t in results] + [0.0])
    for name, t in results:
        p = _book(st, name, t, slowest)
        ev.append("%s %s%s" % (name, fmt(t) if t > 0 else "DNF", "  (new best)" if t > 0 and p["best"] == t else ""))
    st["races"] += 1
    return ev


def rank_time_attack(st):
    return sorted(st["players"].items(), key=lambda kv: (kv[1]["best"] is None, kv[1]["best"] or 0))


# ---------------------------------------------------------------- elimination: slowest / non-finisher of the active players is out after every race
def elimination(st, results):
    ev = []
    act = _active(results, st)
    slowest = max([t for _, t in results] + [0.0])
    for name, t in results:
        _book(st, name, t, slowest)
    if len(act) > 1:
        dnf = [x for x in act if x[1] == 0]
        out = dnf[0] if dnf else max(act, key=lambda x: x[1])      # a non-finisher goes first, otherwise the slowest finisher
        _player(st, out[0])["eliminated"] = True
        ev.append("ELIMINATED %s (%s)" % (out[0], fmt(out[1]) if out[1] > 0 else "DNF"))
    left = [n for n, p in st["players"].items() if not p["eliminated"]]
    if len(left) == 1 and len(act) > 1:
        _player(st, left[0])["wins"] += 1
        ev.append("WINNER %s" % left[0])
    st["races"] += 1
    return ev


def rank_elimination(st):
    return sorted(st["players"].items(), key=lambda kv: (kv[1]["eliminated"], kv[1]["total"]))


# ---------------------------------------------------------------- streak: cannonball plus a growing bonus for winning races back to back
def streak(st, results):
    ev = cannonball(st, results)
    winner = results[0][0] if results and results[0][1] > 0 else None
    s = st.setdefault("streak", {"name": None, "count": 0})
    if winner is None:
        s["name"], s["count"] = None, 0
    elif s["name"] == winner:
        s["count"] += 1
    else:
        s["name"], s["count"] = winner, 1
    if winner and s["count"] >= 2:
        bonus = STREAK_BONUS * min(s["count"] - 1, STREAK_CAP)
        p = _player(st, winner); p["points"] += bonus; p["bonus"] = p.get("bonus", 0) + bonus
        ev.append("STREAK  %s wins x%d  +%d" % (winner, s["count"], bonus))
    return ev


# ---------------------------------------------------------------- survivor: cannonball, but non-finishers and the last finisher lose points
def survivor(st, results):
    ev = cannonball(st, results)
    finishers = [n for n, t in results if t > 0]
    losers = [n for n, t in results if t == 0]
    if len(finishers) >= 3:
        losers.append(finishers[-1])
    for n in losers:
        p = _player(st, n)
        lost = min(SURVIVOR_PENALTY, p["points"])
        if lost:
            p["points"] -= lost; p["bonus"] = p.get("bonus", 0) - lost
            ev.append("SURVIVOR  %s  -%d" % (n, lost))
    return ev


# ---------------------------------------------------------------- team relay: a team's race time = sum of its fastest members, teams score per team place
def team_relay(st, results):
    ev = cannonball(st, results)                      # individual points and times as usual
    miss = max([t for _, t in results] + [0.0]) + DNF_PENALTY
    per = {}
    for name, t in results:
        per.setdefault(_team_of(st, name), []).append(t if t > 0 else miss)
    scored = []
    for team in st["team_names"]:
        ts = sorted(per.get(team, []))[:RELAY_N]
        ts += [miss] * (RELAY_N - len(ts))            # a missing racer counts as a non-finisher
        scored.append((sum(ts), team))
    scored.sort()
    relay = st.setdefault("relay", {t: 0 for t in st["team_names"]})
    for place, (tot, team) in enumerate(scored):
        pts = RELAY_POINTS[place] if place < len(RELAY_POINTS) else 0
        relay[team] = relay.get(team, 0) + pts
        ev.append("RELAY %d. %s %s  +%d" % (place + 1, team, fmt(tot), pts))
    return ev


# ---------------------------------------------------------------- stage race: no points, the lowest total time leads (stage wins are counted)
def stage_race(st, results):
    ev = []
    slowest = max([t for _, t in results] + [0.0])
    for place, (name, t) in enumerate(results):
        p = _book(st, name, t, slowest)
        if place == 0 and t > 0:
            p["wins"] += 1
            p["points"] = p["wins"]                    # the points column shows stage wins
    st["races"] += 1
    rk = rank_stage(st)
    lead = rk[0][1]["total"] if rk else 0.0
    ev.append("STAGE  " + "   ".join("%s %s" % (n, "LEAD" if i == 0 else "+" + fmt(p["total"] - lead)) for i, (n, p) in enumerate(rk[:6])))
    return ev


def rank_stage(st):
    return sorted(st["players"].items(), key=lambda kv: (kv[1]["total"] if kv[1]["races"] else 1e12, -kv[1]["wins"]))


# ---------------------------------------------------------------- rivals: cannonball plus a bonus for finishing ahead of the player ranked just above you
def rivals(st, results):
    before = [n for n, _ in rank_cannonball(st)] if st["races"] else []
    times = dict(results)
    ev = cannonball(st, results)
    for i, name in enumerate(before):
        rival = before[i - 1] if i > 0 else (before[1] if len(before) > 1 else None)    # the leader chases the runner-up
        if rival is None or name not in times or rival not in times or times[name] <= 0:
            continue
        if times[rival] == 0 or times[name] < times[rival]:
            p = _player(st, name); p["points"] += RIVAL_BONUS; p["bonus"] = p.get("bonus", 0) + RIVAL_BONUS
            ev.append("RIVAL  %s beat %s  +%d" % (name, rival, RIVAL_BONUS))
    return ev


# ---------------------------------------------------------------- jackpot / underdog: cannonball with a multiplier on this race's points
def _scaled(st, results, factor_for):
    before = {n: p["points"] for n, p in st["players"].items()}
    ranked = [n for n, _ in rank_cannonball(st)]
    ev = cannonball(st, results)
    for name, _ in results:
        p = st["players"][name]
        gained = p["points"] - before.get(name, 0)
        extra = int(round(gained * (factor_for(name, ranked) - 1)))
        if extra > 0:
            p["points"] += extra; p["bonus"] = p.get("bonus", 0) + extra
            ev.append("x%.1f  %s  +%d" % (factor_for(name, ranked), name, extra))
    return ev


def jackpot(st, results):
    double = (st["races"] + 1) % JACKPOT_EVERY == 0
    return _scaled(st, results, lambda n, ranked: 2.0 if double else 1.0)


def underdog(st, results):
    def factor(name, ranked):
        if len(ranked) >= 3 and name in ranked and ranked.index(name) >= len(ranked) - len(ranked) // 3:
            return UNDERDOG_FACTOR
        return 1.0
    return _scaled(st, results, factor)


# ---------------------------------------------------------------- reverse: the slowest finisher scores the most
def reverse(st, results):
    ev = []
    slowest = max([t for _, t in results] + [0.0])
    fin = [(n, t) for n, t in results if t > 0]
    for n, t in results:
        p = _book(st, n, t, slowest)
        if t <= 0:
            ev.append("DNF %s  +0" % n)
            continue
        place = fin.index((n, t))
        pts = max(len(fin) - (len(fin) - 1 - place), 1)           # last finisher = N points, winner = 1
        p["points"] += pts
        if place == len(fin) - 1:
            p["wins"] += 1
        ev.append("%d. %s %s  +%d" % (place + 1, n, fmt(t), pts))
    st["races"] += 1
    return ev


# ---------------------------------------------------------------- photofinish: cannonball plus bonuses for close finishes
PHOTO_GAP = 1.0                      # seconds between the winner and the runner-up that counts as a photo finish
PHOTO_BONUS = 2


def photofinish(st, results):
    ev = cannonball(st, results)
    fin = [(n, t) for n, t in results if t > 0]
    if len(fin) >= 2 and fin[1][1] - fin[0][1] < PHOTO_GAP:
        for n in (fin[0][0], fin[1][0]):
            _player(st, n)["points"] += PHOTO_BONUS
        ev.append("PHOTO FINISH (%.2f s): %s and %s +%d" % (fin[1][1] - fin[0][1], fin[0][0], fin[1][0], PHOTO_BONUS))
    return ev


# ---------------------------------------------------------------- personalbest: cannonball plus a bonus for beating your own best time
PB_BONUS = 3


def personal_best(st, results):
    before = {n: st["players"].get(n, {}).get("best") for n, _ in results}
    ev = cannonball(st, results)
    for n, t in results:
        if t > 0 and before.get(n) is not None and t < before[n]:
            _player(st, n)["points"] += PB_BONUS
            ev.append("PERSONAL BEST %s  +%d" % (n, PB_BONUS))
    return ev


# ---------------------------------------------------------------- hare (catch the leader): the previous winner is the hare, hounds score for catching it
def hare(st, results):
    h = st.get("hare")
    times = dict(results)
    ev = cannonball(st, results)
    if h in times and len(results) > 1:
        ht = times[h]
        for n, t in results:
            if n == h:
                continue
            if t > 0 and (ht == 0 or t < ht):
                p = _player(st, n); p["points"] += HARE_CAUGHT; p["bonus"] = p.get("bonus", 0) + HARE_CAUGHT
                ev.append("CAUGHT  %s passed hare %s  +%d" % (n, h, HARE_CAUGHT))
            elif ht > 0:
                p = _player(st, h); p["points"] += HARE_ESCAPE; p["bonus"] = p.get("bonus", 0) + HARE_ESCAPE
                ev.append("ESCAPED  hare %s beat %s  +%d" % (h, n, HARE_ESCAPE))
    st["hare"] = results[0][0] if results and results[0][1] > 0 else None
    if st["hare"]:
        ev.append("HARE  %s" % st["hare"])
    return ev


# ---------------------------------------------------------------- plague (infected, results based): the last finisher / non-finishers are infected, a win cures
def plague(st, results):
    inf = st.setdefault("infected", [])
    before = {n: p["points"] for n, p in st["players"].items()}
    ev = cannonball(st, results)
    for n, _ in results:
        if n in inf:
            p = st["players"][n]
            lost = int((p["points"] - before.get(n, 0)) * (1 - PLAGUE_SHARE))
            if lost > 0:
                p["points"] -= lost; p["bonus"] = p.get("bonus", 0) - lost
                ev.append("INFECTED  %s keeps %d%% of the points  -%d" % (n, int(PLAGUE_SHARE * 100), lost))
    if results and results[0][1] > 0 and results[0][0] in inf:
        inf.remove(results[0][0])
        ev.append("CURED  %s" % results[0][0])
    finishers = [n for n, t in results if t > 0]
    sick = [n for n, t in results if t == 0]
    if len(finishers) >= 3:
        sick.append(finishers[-1])
    for n in sick:
        if n not in inf:
            inf.append(n)
            ev.append("INFECTED  %s" % n)
    return ev


# ---------------------------------------------------------------- halving (knockout): after every race the slower half of the active players is out
def halving(st, results):
    ev = []
    slowest = max([t for _, t in results] + [0.0])
    for name, t in results:
        _book(st, name, t, slowest)
    act = sorted(_active(results, st), key=lambda x: (x[1] == 0, x[1]))        # finishers by time, non-finishers last
    if len(act) > 1:
        keep = (len(act) + 1) // 2 if len(act) > 2 else 1
        for n, t in act[keep:]:
            _player(st, n)["eliminated"] = True
            ev.append("OUT  %s (%s)" % (n, fmt(t) if t > 0 else "DNF"))
    left = [n for n, p in st["players"].items() if not p["eliminated"]]
    if len(left) == 1 and len(act) > 1:
        _player(st, left[0])["wins"] += 1
        ev.append("WINNER %s" % left[0])
    st["races"] += 1
    return ev


# ---------------------------------------------------------------- margin: points shrink with the gap to the winner, so close finishes pay
def margin(st, results):
    ev = []
    slowest = max([t for _, t in results] + [0.0])
    lead = results[0][1] if results and results[0][1] > 0 else None
    for place, (name, t) in enumerate(results):
        p = _book(st, name, t, slowest)
        if lead is None or t <= 0:
            ev.append("DNF %s  +0" % name)
            continue
        pts = max(MARGIN_MAX - int((t - lead) // 2), 0)
        p["points"] += pts
        if place == 0:
            p["wins"] += 1
        ev.append("%d. %s %s  +%d" % (place + 1, name, fmt(t), pts))
    st["races"] += 1
    return ev


# ---------------------------------------------------------------- team captain: team cannonball where each team's captain (first listed) scores double
def team_captain(st, results):
    before = {n: p["points"] for n, p in st["players"].items()}
    ev = cannonball(st, results)
    seen = set()
    for name, _ in results:
        team = _team_of(st, name)
        if team in seen:
            continue
        cap = next((n for n, t in st["teams"].items() if t == team), None)
        seen.add(team)
        if cap in st["players"] and cap in dict(results):
            p = st["players"][cap]
            extra = p["points"] - before.get(cap, 0)
            if extra > 0:
                p["points"] += extra; p["bonus"] = p.get("bonus", 0) + extra
                ev.append("CAPTAIN  %s (%s) double  +%d" % (cap, team, extra))
    ev.append("TEAMS  " + "   ".join("%s %d" % (t, pts) for t, pts, _ in team_ranking(st)))
    return ev


def fmt(t):
    return "%d:%05.2f" % (int(t // 60), t % 60)


MODES = {
    "cannonball": (cannonball, rank_cannonball, "points per place (10,8,6,4,3,2,1), total time breaks ties"),
    "timeattack": (time_attack, rank_time_attack, "best single race time wins"),
    "elimination": (elimination, rank_elimination, "after every race the slowest (or a non-finisher) is eliminated"),
    "teamcannonball": (team_cannonball, rank_cannonball, "teams: points per place as in cannonball, the team total counts"),
    "streak": (streak, rank_cannonball, "cannonball plus a bonus that grows (+%d per repeat, up to %d) for winning races back to back" % (STREAK_BONUS, STREAK_CAP)),
    "survivor": (survivor, rank_cannonball, "cannonball, but a non-finisher and the last finisher (3+ finishers) lose %d points" % SURVIVOR_PENALTY),
    "teamrelay": (team_relay, rank_cannonball, "teams: team time = sum of its %d fastest members, team points %s per team place" % (RELAY_N, "/".join(map(str, RELAY_POINTS)))),
    "stagerace": (stage_race, rank_stage, "no points: the lowest total time leads, stage wins are counted"),
    "rivals": (rivals, rank_cannonball, "cannonball plus +%d for finishing ahead of your rival (the player ranked just above you)" % RIVAL_BONUS),
    "jackpot": (jackpot, rank_cannonball, "cannonball where every %dth race counts double" % JACKPOT_EVERY),
    "underdog": (underdog, rank_cannonball, "cannonball where the bottom third of the standings earns x%.1f points (3+ players)" % UNDERDOG_FACTOR),
    "hare": (hare, rank_cannonball, "catch the leader: the previous winner is the hare; hounds ahead of it +%d, the hare +%d per hound beaten" % (HARE_CAUGHT, HARE_ESCAPE)),
    "plague": (plague, rank_cannonball, "infected-style: the last finisher / non-finishers are infected and keep half the points until they win a race"),
    "halving": (halving, rank_elimination, "knockout: after every race the slower half of the active players is out"),
    "margin": (margin, rank_cannonball, "points shrink with the gap to the winner (%d for the winner, -1 per 2 s behind)" % MARGIN_MAX),
    "teamcaptain": (team_captain, rank_cannonball, "teams: cannonball points, the team captain (first listed) scores double"),
    "reverse": (reverse, rank_cannonball, "reverse cannonball: the slowest finisher scores the most points, the winner the least"),
    "photofinish": (photofinish, rank_cannonball, "cannonball plus +%d each for the winner and runner-up when they finish within %.1f s" % (PHOTO_BONUS, PHOTO_GAP)),
    "personalbest": (personal_best, rank_cannonball, "cannonball plus +%d for beating your own best race time" % PB_BONUS),
    "bounty": (bounty, rank_cannonball, "cannonball plus a +%d bounty for beating the points leader and +%d for the slowest finisher" % (BOUNTY, COMEBACK)),
}


def apply(st, results):
    return MODES[st["mode"]][0](st, results)


def ranking(st):
    return MODES[st["mode"]][1](st)


def table(st):
    lines = []
    if st.get("teams") is not None:
        lines.append("TEAMS: " + "   ".join("%s %d (%s)" % (t, pts, ", ".join(m) or "-") for t, pts, m in team_ranking(st)))
    lines.append("%-3s %-16s %6s %9s %5s %s" % ("#", "player", "points", "total", "wins", "status"))
    for i, (n, p) in enumerate(ranking(st), 1):
        extra = ("OUT" if p["eliminated"] else "") + (" INFECTED" if n in st.get("infected", []) else "") + (" HARE" if n == st.get("hare") else "")
        extra += "" + (" [%s]" % p["team"] if p.get("team") else "") + (" (%+d bonus)" % p["bonus"] if p.get("bonus") else "")
        lines.append("%-3d %-16s %6d %9s %5d %s" % (i, n, p["points"], fmt(p["total"]), p["wins"], extra.strip()))
    return "\n".join(lines)


def _selftest_schemes():
    for sch in SCHEMES:
        st = new_standings("cannonball", sch)
        apply(st, [("A", 100.0), ("B", 101.0), ("C", 102.0), ("D", 0)])            # 4 players
        apply(st, [("A", 99.0), ("B", 100.0)])                                     # C and D dropped out: only 2 players left
        print("scheme", sch, {n: p["points"] for n, p in st["players"].items()})


def _selftest_new():
    st = new_standings("teamcannonball", "fixed", "Red=Ana,Bo;Blue=Cy")
    apply(st, [("Ana", 100.0), ("Cy", 101.0), ("Bo", 102.0), ("Di", 103.0)])        # Di is unlisted -> joins the smaller team
    tr = team_ranking(st)
    assert st["players"]["Di"]["team"] == "Blue", st["players"]["Di"]            # Red has 2 members, Blue 1 -> Blue
    assert dict((t, p) for t, p, _ in tr) == {"Red": 10 + 6, "Blue": 8 + 4}, tr
    st = new_standings("bounty", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0), ("Cy", 102.0)])                     # race 1: no leader yet, Cy gets the comeback point
    assert st["players"]["Cy"]["points"] == 6 + COMEBACK
    apply(st, [("Bo", 99.0), ("Ana", 100.0), ("Cy", 101.0)])                      # Ana leads; Bo beats her -> bounty
    assert st["players"]["Bo"]["bonus"] == BOUNTY, st["players"]["Bo"]
    apply(st, [("Cy", 90.0), ("Ana", 0)])                                          # leader is now Bo (absent): no bounty
    print("new modes self-test ok")


def _selftest_more():
    st = new_standings("streak", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0)])                                      # Ana wins: streak 1, no bonus
    ev = apply(st, [("Ana", 99.0), ("Bo", 100.0)])                                  # streak 2: +2
    assert st["players"]["Ana"]["points"] == 10 + 10 + STREAK_BONUS and any("STREAK" in e for e in ev), st["players"]["Ana"]
    apply(st, [("Bo", 98.0), ("Ana", 99.0)])                                        # Bo wins: streak resets
    assert st["streak"] == {"name": "Bo", "count": 1}, st["streak"]
    for _ in range(6):                                                              # cap: bonus never above STREAK_BONUS*STREAK_CAP
        before = st["players"]["Bo"]["points"]
        ev = apply(st, [("Bo", 90.0), ("Ana", 95.0)])
    assert st["players"]["Bo"]["points"] - before == 10 + STREAK_BONUS * STREAK_CAP
    st = new_standings("survivor", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0), ("Cy", 102.0), ("Di", 0)])             # Cy last finisher and Di DNF: they had 0 points before -> floor 0
    assert st["players"]["Cy"]["points"] == 6 - SURVIVOR_PENALTY and st["players"]["Di"]["points"] == 0, st["players"]
    apply(st, [("Di", 90.0), ("Ana", 91.0)])                                        # only 2 finishers: no last-place penalty
    assert st["players"]["Ana"]["points"] == 10 + 8 - 0 and st["players"]["Di"]["points"] == 10, st["players"]
    st = new_standings("teamrelay", "fixed", "Red=Ana,Bo;Blue=Cy,Di")
    apply(st, [("Ana", 100.0), ("Cy", 101.0), ("Bo", 102.0), ("Di", 120.0)])        # Red 202, Blue 221 -> Red 10, Blue 6
    assert st["relay"] == {"Red": 10, "Blue": 6}, st["relay"]
    apply(st, [("Cy", 90.0), ("Di", 91.0), ("Ana", 0)])                             # Blue 181, Red: Ana DNF + missing Bo -> Blue wins
    assert st["relay"] == {"Red": 10 + 6, "Blue": 6 + 10}, st["relay"]
    assert dict((t, p) for t, p, _ in team_ranking(st)) == st["relay"]
    print("streak/survivor/teamrelay self-test ok")


def _selftest_batch2():
    st = new_standings("stagerace", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 105.0), ("Cy", 0)])                           # Cy DNF = slowest + penalty
    apply(st, [("Bo", 99.0), ("Ana", 101.0), ("Cy", 120.0)])
    rk = [n for n, _ in rank_stage(st)]
    assert rk == ["Ana", "Bo", "Cy"], rk                                           # totals 201 / 204 / 105+60+120=285
    assert st["players"]["Ana"]["wins"] == 1 and st["players"]["Bo"]["points"] == 1
    st = new_standings("rivals", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0), ("Cy", 102.0)])                       # race 1: no rivals yet
    assert all(p["bonus"] == 0 for p in st["players"].values())
    ev = apply(st, [("Cy", 90.0), ("Bo", 91.0), ("Ana", 92.0)])                     # order before: Ana, Bo, Cy
    assert st["players"]["Bo"]["bonus"] == RIVAL_BONUS and st["players"]["Cy"]["bonus"] == RIVAL_BONUS, st["players"]
    assert st["players"]["Ana"]["bonus"] == 0                                       # leader's rival is Bo: Bo finished ahead of Ana
    st = new_standings("jackpot", "fixed")
    gained = []
    for _ in range(3):
        b = st["players"].get("Ana", {"points": 0})["points"]
        apply(st, [("Ana", 100.0), ("Bo", 101.0)])
        gained.append(st["players"]["Ana"]["points"] - b)
    assert gained == [10, 10, 20], gained                                           # third race counts double
    st = new_standings("underdog", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0), ("Cy", 102.0)])                       # points 10 / 8 / 6
    apply(st, [("Cy", 90.0), ("Ana", 91.0), ("Bo", 92.0)])                          # Cy is the bottom third -> 10 x1.5 = 15
    assert st["players"]["Cy"]["points"] == 6 + 15, st["players"]["Cy"]
    assert st["players"]["Ana"]["points"] == 10 + 8, st["players"]["Ana"]
    print("stagerace/rivals/jackpot/underdog self-test ok")


def _selftest_batch3():
    st = new_standings("hare", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0), ("Cy", 102.0)])                       # Ana becomes the hare
    assert st["hare"] == "Ana"
    ev = apply(st, [("Bo", 99.0), ("Ana", 100.0), ("Cy", 101.0)])                   # Bo caught the hare; hare beat Cy
    assert st["players"]["Bo"]["bonus"] == HARE_CAUGHT and st["players"]["Ana"]["bonus"] == HARE_ESCAPE, st["players"]
    assert st["hare"] == "Bo"
    apply(st, [("Cy", 90.0), ("Bo", 0)])                                            # hare DNF: Cy caught it, no escape bonus
    assert st["players"]["Cy"]["bonus"] == HARE_CAUGHT and st["hare"] == "Cy"
    st = new_standings("plague", "fixed")
    apply(st, [("Ana", 100.0), ("Bo", 101.0), ("Cy", 102.0), ("Di", 0)])            # Cy (last) and Di (DNF) infected
    assert sorted(st["infected"]) == ["Cy", "Di"], st["infected"]
    apply(st, [("Cy", 90.0), ("Ana", 91.0), ("Bo", 92.0), ("Di", 93.0)])            # Cy wins infected: half points, then cured
    cy = st["players"]["Cy"]
    assert cy["points"] == 6 + 5 and "Cy" not in st["infected"], cy                 # 6 earlier + (10 -> 5)
    assert st["infected"] == ["Di"], st["infected"]                                 # Di finished last again; Bo and Ana stay healthy
    print("hare/plague self-test ok")


def _selftest_batch4():
    st = new_standings("halving", "fixed")
    apply(st, [("A", 100.0), ("B", 101.0), ("C", 102.0), ("D", 103.0), ("E", 0)])    # 5 active -> keep 3: A B C stay, D and E out
    assert {n for n, p in st["players"].items() if p["eliminated"]} == {"D", "E"}, st["players"]
    apply(st, [("C", 90.0), ("A", 91.0), ("B", 92.0)])                                # 3 active -> keep 2: C A stay, B out
    assert st["players"]["B"]["eliminated"] and not st["players"]["A"]["eliminated"]
    ev = apply(st, [("A", 80.0), ("C", 81.0)])                                        # 2 active -> keep 1
    assert st["players"]["C"]["eliminated"] and st["players"]["A"]["wins"] == 1 and any("WINNER A" in e for e in ev), ev
    st = new_standings("margin", "fixed")
    apply(st, [("A", 100.0), ("B", 101.5), ("C", 105.0), ("D", 130.0), ("E", 0)])
    pts = {n: p["points"] for n, p in st["players"].items()}
    assert pts == {"A": 10, "B": 10, "C": 8, "D": 0, "E": 0}, pts                     # gaps 0 / 1.5 / 5 / 30
    st = new_standings("teamcaptain", "fixed", "Red=Ana,Bo;Blue=Cy,Di")
    apply(st, [("Ana", 100.0), ("Cy", 101.0), ("Bo", 102.0), ("Di", 103.0)])
    pts = {n: p["points"] for n, p in st["players"].items()}
    assert pts == {"Ana": 20, "Cy": 16, "Bo": 6, "Di": 4}, pts                         # captains Ana and Cy score double
    assert dict((t, p) for t, p, _ in team_ranking(st)) == {"Red": 26, "Blue": 20}
    print("halving/margin/teamcaptain self-test ok")


if __name__ == "__main__":                 # offline self-test with synthetic races
    _selftest_new()
    _selftest_more()
    _selftest_batch2()
    _selftest_batch3()
    _selftest_batch4()
    _selftest_schemes()
    for mode in MODES:
        st = new_standings(mode, "field")
        for race in ([("Ana", 200.5), ("Bo", 205.0), ("Cy", 0)], [("Bo", 190.0), ("Ana", 201.0), ("Cy", 230.0)], [("Cy", 180.0), ("Bo", 0), ("Ana", 0)]):
            ev = apply(st, race)
            print(mode, "|", "; ".join(ev))
        print(table(st), "\n")
