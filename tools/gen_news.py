#!/usr/bin/env python3
"""Write server/autolog_news.json from launcher/modes.py (one News item per game mode) plus any extra items.
Usage: tools/gen_news.py [active_mode]   - the active mode is listed first. The server reads the file per request."""
import datetime, json, os, sys
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "launcher"))
import modes

def stamp(d):
    return d.strftime("%m/%d/%Y ") + str(int(d.strftime("%I"))) + d.strftime(":%M:%S %p")

if len(sys.argv) > 1 and sys.argv[1] == "--vanilla":             # plain server: one welcome item, no custom modes
    now = datetime.datetime.now()
    json.dump([{"NewsId": 1, "NewsIcon": "/news/banner.jpg", "NewsViewed": "NewsViewed_No", "Territory": "", "MetaContent": "",
                "Subject": "Welcome", "ShortMessage": "Standard rules - no custom game modes on this server.",
                "Message": "This server runs the normal game with standard rules.", "Author": "TR Campaign", "PublishedDate": stamp(now)}],
              open(os.path.join(ROOT, "server", "autolog_news.json"), "w"), indent=1)
    print("wrote vanilla news"); sys.exit(0)
active = sys.argv[1] if len(sys.argv) > 1 else None
names = sorted(modes.MODES, key=lambda m: (m != active, m))
now = datetime.datetime.now()
items = []
for i, m in enumerate(names):
    text = modes.MODES[m][2]
    items.append({"NewsId": 100 + i, "NewsIcon": "/news/banner.jpg", "NewsViewed": "NewsViewed_No", "Territory": "", "MetaContent": "",
                  "Subject": ("Now playing: " if m == active else "Mode: ") + m.capitalize(),
                  "ShortMessage": text[:110], "Message": text, "Author": "TR Campaign",
                  "PublishedDate": stamp(now - datetime.timedelta(minutes=i))})
with open(os.path.join(ROOT, "server", "autolog_news.json"), "w", encoding="utf-8") as f:
    json.dump(items, f, indent=1)
print("wrote %d news items (active: %s)" % (len(items), active))
