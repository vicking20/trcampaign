/* NFSTR_LAN.asi - clean-room replacement for the closed "LAN Script" helper of
 * NFS The Run (nfsmods.xyz/mod/5541). Applies the same in-memory tweaks so the
 * game's own Frostbite client/server can run playlist multiplayer on a LAN.
 *
 * One .asi serves both roles. Role is detected from the game exe itself:
 *   client exe:  bytes 6a 00 52 at VA 0x4180CA
 *   server exe:  bytes 52 6a 01 at VA 0x4180CA   (NFSTR_Server.exe, a 3-byte patched client)
 *
 * Every patch verifies the bytes it replaces. If they do not match (different exe build,
 * or another plugin already changed them) the patch is skipped and logged, never forced.
 *
 * Config: Scripts\NFSTR_LAN.ini (falls back to the old NFSTR_LANOnlineHelper.ini, same keys).
 * Log:    Scripts\NFSTR_LAN.log
 *
 * Build:  i686-w64-mingw32-gcc -O2 -shared -o NFSTR_LAN.asi nfstr_lan.c -static-libgcc
 */
#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <windows.h>
#include <stdio.h>
#include <stdarg.h>
#include <string.h>
#include <stdlib.h>

/* ---- game addresses (image base 0x400000, the cracked/SteamRIP 1.0.0.0 exe) ---- */
#define ROLE_PROBE      0x4180CA
#define CLIENT_GAMEOBJ  0x28823BC   /* client: pointer to game object, +0xC = career data */
#define SERVER_OBJ      0x27A3300   /* server: static game object, +0xC = career data */
#define SET_PLAYLIST_FN 0x84CEA0    /* client thiscall(obj, playlist_ptr - 0x10) */

static FILE *g_log;
static char g_dir[MAX_PATH];          /* folder of the exe */
static char g_ini[MAX_PATH + 40];
static int g_is_server;

static void LOG(const char *fmt, ...)
{
    if (!g_log) return;
    va_list ap; va_start(ap, fmt);
    vfprintf(g_log, fmt, ap); va_end(ap);
    fputc('\n', g_log); fflush(g_log);
}

/* ---- patching ---- */
static int mem_write(DWORD va, const void *src, int n)
{
    DWORD old;
    if (!VirtualProtect((void *)va, n, PAGE_EXECUTE_READWRITE, &old)) return 0;
    memcpy((void *)va, src, n);
    FlushInstructionCache(GetCurrentProcess(), (void *)va, n);
    VirtualProtect((void *)va, n, old, &old);
    return 1;
}

/* oldb == NULL: no verification (data we own). */
static int patch(const char *what, DWORD va, const void *oldb, const void *newb, int n)
{
    if (oldb && memcmp((void *)va, oldb, n)) {
        if (!memcmp((void *)va, newb, n)) { LOG("[%s] %08X already patched (another plugin?)", what, (unsigned)va); return 1; }
        LOG("[%s] %08X MISMATCH, skipped", what, (unsigned)va);
        return 0;
    }
    if (!mem_write(va, newb, n)) { LOG("[%s] %08X VirtualProtect failed", what, (unsigned)va); return 0; }
    LOG("[%s] %08X ok (%d bytes)", what, (unsigned)va, n);
    return 1;
}
#define PATCHS(what, va, o, n) patch(what, va, o, n, sizeof(n) - 1)
static void nop(const char *what, DWORD va, const void *oldb, int n)
{
    BYTE buf[32]; memset(buf, 0x90, n);
    patch(what, va, oldb, buf, n);
}
static void jmp_to(const char *what, DWORD va, const void *oldb, int n, int is_call, void *dest)
{
    BYTE buf[16]; memset(buf, 0x90, n);
    buf[0] = is_call ? 0xE8 : 0xE9;
    *(DWORD *)(buf + 1) = (DWORD)dest - va - 5;
    patch(what, va, oldb, buf, n);
}

/* ---- ini ---- */
static int ini_int(const char *sec, const char *key, int def) { return GetPrivateProfileIntA(sec, key, def, g_ini); }
static void ini_str(const char *sec, const char *key, const char *def, char *out, int n) { GetPrivateProfileStringA(sec, key, def, out, n, g_ini); }

/* ---- playlist selection (shared by client and server hooks) ---- */
static int g_set, g_id;
unsigned g_playlist_ptr;

static unsigned pick_playlist(const char *career)
{
    unsigned **sets = *(unsigned ***)(career + 0x14);
    int nsets = *(int *)((char *)sets - 4);
    int set = g_set, id = g_id;
    if (set < 0 || id < 0 || set > nsets - 1 || id > *(int *)((char *)sets[set] - 4) - 1) {
        LOG("playlist set/id %d/%d out of range (%d sets), using 0/0", set, id, nsets);
        set = id = 0;
    }
    return sets[set][id];
}

/* The hook sits right before the host function's ret, so EAX is that function's return value:
 * like the original helper, hand back the result of the game's own set-playlist call. */
unsigned nfl_client_hook_c(void)
{
    char *obj = *(char **)CLIENT_GAMEOBJ;
    if (!obj) return 0;
    unsigned p = pick_playlist(*(char **)(obj + 0xC));
    typedef unsigned (__attribute__((thiscall)) *setpl_t)(void *, unsigned);
    unsigned r = ((setpl_t)SET_PLAYLIST_FN)(obj, p - 0x10);
    LOG("client playlist set %d/%d -> %08X (returns %08X)", g_set, g_id, p, r);
    return r;
}
void nfl_server_ptr_c(void)  { g_playlist_ptr = pick_playlist(*(char **)(SERVER_OBJ + 0xC)); }
volatile int g_session_started;
void nfl_server_session_c2(void) { g_session_started = 1; BYTE v = *(BYTE *)(SERVER_OBJ + 4); mem_write(0x1400182, &v, 1); LOG("session-sync: session id %u", v); }

/* call target for the client cave: preserves every register and flag */
__attribute__((naked)) void nfl_client_cave(void)
{ __asm__ volatile("pushl %ecx\n call _nfl_client_hook_c\n popl %ecx\n ret\n"); }
/* server: replaces "mov eax,[esi+0xc]; mov ecx,[eax+0x10]" at 0x1400168 (6 bytes), resumes at 0x140016E */
__attribute__((naked)) void nfl_server_cave_pl(void)
{ __asm__ volatile("movl 0xc(%esi),%eax\n pushfl\n pushal\n call _nfl_server_ptr_c\n popal\n popfl\n"
                   "movl _g_playlist_ptr,%ecx\n pushl $0x140016E\n ret\n"); }
/* server: replaces "mov dword [esi+0x1084],1" at 0x13FFE3F (10 bytes), resumes at 0x13FFE49 */
__attribute__((naked)) void nfl_server_cave_sess(void)
{ __asm__ volatile("movl $1,0x1084(%esi)\n pushfl\n pushal\n call _nfl_server_session_c2\n popal\n popfl\n"
                   "pushl $0x13FFE49\n ret\n"); }

/* ---- the tweaks ---- */
static void common_patches(void)
{
    /* both roles: unconditional in the original helper */
    PATCHS("common", 0xDE6BA7, "\x05\x10", "\x15\x00");
}

static void client_patches(void)
{
    char name[64], flow[64];
    if (ini_int("OnlineSettings", "EnableLANOnlineTweaks", 1)) {
        PATCHS("lan-tweaks", 0x2269335, "\xEB\x60\x2C\xF2", "\xFC\x08\x5A\xB0");
        nop("lan-tweaks", 0x8848F5, (BYTE[]){0x80,0x7C,0x24,0x4C,0x00,0x74,0x06,0x80,0x78,0x19,0x00,0x74,0x1F}, 13);
        PATCHS("lan-tweaks", 0xE9B2A8, "\x74\x08\x8B\x11\x8B", "\xE9\x05\x00\x00\x00");
    }
    if (ini_int("OnlineSettings", "DisableAutologConnectAttempts", 1))
        PATCHS("autolog", 0x8FEF9E, "\x00", "\x01");
    if (ini_int("GameSettings", "EnableDebugModeMenus", 1))
        PATCHS("debug-menus", 0x968F50, "\x75\x29\x38\x5E\x40", "\xE9\x26\x00\x00\x00");
    if (ini_int("GameSettings", "GameFastBoot", 0))
        PATCHS("fastboot", 0x8A9361, "\x00", "\x01");

    ini_str("GameSettings", "ForceInitFlow", "MainMenu", flow, sizeof flow);
    if (flow[0] && strcmp(flow, "BootFlow") && strlen(flow) < 12) {
        BYTE b[12] = {0}; memcpy(b, flow, strlen(flow));
        BYTE o[12] = {'B','o','o','t','F','l','o','w',0,0,0,0};
        patch("init-flow", 0x247CF70, o, b, 12);
    }
    if (ini_int("OnlineSettings", "DisableUserComputerName", 1)) {
        ini_str("OnlineSettings", "ClientName", "Player", name, sizeof name);
        if (GetEnvironmentVariableA("NFSTR_CLIENT_NAME", name, sizeof name) == 0)   /* per-instance override */
            ini_str("OnlineSettings", "ClientName", "Player", name, sizeof name);
        name[16] = 0;
        BYTE b[16] = {0}; memcpy(b, name, strlen(name));
        PATCHS("name", 0x4F2B07, "\x10", "\x00");
        patch("name", 0x23EB5A8, NULL, b, 16);
        PATCHS("name", 0x832C32, "\xC0\x4C\x47\x02", "\xA8\xB5\x3E\x02");
    }
    if (ini_int("PlaylistSettings", "ForceClientPlaylistPtrInMemory", 1)) {
        PATCHS("playlist", 0x1033DD5, "\x89\x7E\x10", "\x90\x90\x90");
        jmp_to("playlist", 0x85DB93, (BYTE[]){0xC7,0x45,0x10,0,0,0,0}, 7, 1, nfl_client_cave);
    }
}

/* The server object is built by the game's own startup, AFTER we are loaded: writing into it at
 * load time gets overwritten. Wait for its vtable pointer, then set the fields. */
static int g_custom, g_custom_session, g_preload_state;

/* ---- local control channel (server role): UDP on 127.0.0.1:<ControlPort> -------------------------------
 *   status        -> "state=2 timer=12.3 session=5 race=-1 hold=1"
 *   start         -> skip the pre-load countdown now (state 2 only)
 *   hold on|off   -> freeze the countdown at 0 (host decides when to start)
 * State lives in the game's server object: +0x1070 = state (2 = pre-load, 3 = loading, 6 = racing),
 * +0x1074 = float seconds spent in the state; the state ends when it reaches the PreLoad timer.   */

/* ---- research: value scan of the whole process (like a memory scanner) ----
 *   scanf <value> [tol]   32-bit floats equal to value +- tol (default 0.01)
 *   scani <value>         32-bit integers equal to value
 * Writes the hits (address, +offset into the ServerCareerManager if close) to the log, answers with the count. */
static int mem_scan(int is_float, double val, double tol, char *out, int outsz)
{
    SYSTEM_INFO si; GetSystemInfo(&si);
    unsigned char *p = (unsigned char *)si.lpMinimumApplicationAddress; int hits = 0;
    while (p < (unsigned char *)si.lpMaximumApplicationAddress) {
        MEMORY_BASIC_INFORMATION mi;
        if (!VirtualQuery(p, &mi, sizeof mi)) break;
        unsigned char *e = (unsigned char *)mi.BaseAddress + mi.RegionSize;
        if (mi.State == MEM_COMMIT && !(mi.Protect & (PAGE_GUARD | PAGE_NOACCESS)) &&
            (mi.Protect & (PAGE_READWRITE | PAGE_WRITECOPY | PAGE_EXECUTE_READWRITE))) {
            for (unsigned char *q = (unsigned char *)mi.BaseAddress; q + 4 <= e; q += 4) {
                int hit;
                if (is_float) { float f = *(float *)q; hit = (f >= val - tol && f <= val + tol); }
                else hit = (*(int *)q == (int)val);
                if (hit) { if (hits < 60) LOG("scan hit %08X (obj%+d) = %08X", (unsigned)q, (int)((unsigned)q - SERVER_OBJ), *(unsigned *)q); hits++; }
            }
        }
        p = e;
    }
    snprintf(out, outsz, "ok hits=%d (first 60 in the log)", hits);
    return hits;
}


static void dump_area(unsigned a, int len, const char *tag);
/* ServerCareerManager session: global DAT_028E5C90 -> participants vector at +0x56C (begin) / +0x570 (end), 4-byte entries */
#define SESSION_MGR 0x28E5C90
static int participants(unsigned **list)
{
    unsigned m = *(unsigned *)SESSION_MGR;
    if (!m || IsBadReadPtr((void *)(m + 0x56C), 8)) return 0;
    unsigned b = *(unsigned *)(m + 0x56C), e = *(unsigned *)(m + 0x570);
    if (!b || e < b || ((e - b) & 3) || (e - b) / 4 > 64) return 0;
    if (list) *list = (unsigned *)b;
    return (e - b) / 4;
}

/* ---- race results reader (found by value scans, see STATUS.md) -------------------------------------------
 * The server keeps result entries in a pool as:  [name string ...] [n][n] [float seconds] [0x952C945B]
 * with n a small length field (0x12..0x14 seen). Addresses move from race to race, so we take a baseline
 * of all such entries when a race starts and report the NEW/CHANGED ones at the results state.        */
typedef struct { unsigned addr; float t; char name[24]; } resentry_t;
#define MAXRES 256
static resentry_t g_base[MAXRES]; static int g_nbase;

static int find_results(resentry_t *out, int max)
{
    SYSTEM_INFO si; GetSystemInfo(&si);
    unsigned char *p = (unsigned char *)si.lpMinimumApplicationAddress; int n = 0;
    while (p < (unsigned char *)si.lpMaximumApplicationAddress && n < max) {
        MEMORY_BASIC_INFORMATION mi;
        if (!VirtualQuery(p, &mi, sizeof mi)) break;
        unsigned char *e = (unsigned char *)mi.BaseAddress + mi.RegionSize;
        if (mi.State == MEM_COMMIT && !(mi.Protect & (PAGE_GUARD | PAGE_NOACCESS)) && (mi.Protect & (PAGE_READWRITE | PAGE_WRITECOPY))) {
            unsigned *q0 = (unsigned *)mi.BaseAddress, *q1 = (unsigned *)(e - 4);
            for (unsigned *q = q0 + 16; q < q1 && n < max; q++) {
                if (q[0] != 0x952C945B || q[-2] < 8 || q[-2] > 0x40) continue;      /* race 2+ entries have two DIFFERENT length words */
                float t = *(float *)&q[-1];
                if (!(t == 0.0f || (t > 1.0f && t < 36000.0f))) continue;
                /* name: last printable run (>=3 chars) within 0x80 bytes before the length pair, ignoring path-like runs
                 * (race 2+ entries carry a car bundle path such as ".../BB_" between the name and the numbers) */
                unsigned char *end = (unsigned char *)&q[-3], *st = end - 0x80; if (st < (unsigned char *)q0) st = (unsigned char *)q0;
                char best[24] = ""; unsigned char *r = st;
                while (r < end) {
                    unsigned char *a = r; while (a < end && *a >= 32 && *a < 127) a++;
                    if (a - r >= 3 && a < end && *a == 0 && !memchr(r, '/', a - r) && !memchr(r, '\\', a - r)) { int l = (int)(a - r); if (l > 23) l = 23; memcpy(best, r, l); best[l] = 0; }
                    r = (a == r) ? r + 1 : a + 1;
                }
                if (!best[0]) continue;
                out[n].addr = (unsigned)q; out[n].t = t; strcpy(out[n].name, best); n++;
            }
        }
        p = e;
    }
    return n;
}
static void results_baseline(void) { g_nbase = find_results(g_base, MAXRES); LOG("results baseline: %d entries", g_nbase); }
/* Compact result list for the control channel / log: "name=seconds;name=seconds" (finishers sorted by time, DNF = 0 last).
 * Only entries in the game's static pool (0x02700000-0x03000000) count; heap copies and stale entries are ignored. */
static int results_collect(char names[16][24], float times[16])
{
    static resentry_t cur[MAXRES]; int n = find_results(cur, MAXRES), m = 0;
    for (int i = 0; i < n && m < 16; i++) {
        if (cur[i].addr < 0x02700000 || cur[i].addr >= 0x03000000) continue;
        int old = 0;
        for (int k = 0; k < g_nbase; k++) if (g_base[k].addr == cur[i].addr && g_base[k].t == cur[i].t) { old = 1; break; }
        if (old) continue;
        int dup = -1; for (int k = 0; k < m; k++) if (!strcmp(names[k], cur[i].name)) dup = k;
        if (dup >= 0) { if (cur[i].t > times[dup]) times[dup] = cur[i].t; continue; }
        strcpy(names[m], cur[i].name); times[m] = cur[i].t; m++;
    }
    for (int a = 0; a < m; a++) for (int b = a + 1; b < m; b++) {      /* finishers first by time, DNF last */
        int ka = times[a] > 0 ? 0 : 1, kb = times[b] > 0 ? 0 : 1;
        if (kb < ka || (ka == kb && kb == 0 && times[b] < times[a])) {
            float t = times[a]; times[a] = times[b]; times[b] = t;
            char tmp[24]; strcpy(tmp, names[a]); strcpy(names[a], names[b]); strcpy(names[b], tmp);
        }
    }
    return m;
}
static void results_format(char *out, int sz)
{
    char names[16][24]; float times[16]; int m = results_collect(names, times), n = 0;
    out[0] = 0;
    for (int i = 0; i < m; i++) n += snprintf(out + n, sz - n, "%s%s=%.2f", i ? ";" : "", names[i], times[i]);
}
static void results_report(void)
{
    char buf[512]; results_format(buf, sizeof buf);
    LOG("RESULTS session=%d race=%d %s", *(int *)(SERVER_OBJ + 4), *(int *)(SERVER_OBJ + 8), buf);
    /* diagnostic: every candidate entry and why it was (not) counted - for the "finisher shows 0.00" case seen once in a real race */
    static resentry_t cur[MAXRES]; int n = find_results(cur, MAXRES);
    for (int i = 0; i < n && i < 40; i++) {
        int pool = cur[i].addr >= 0x02700000 && cur[i].addr < 0x03000000, old = 0;
        for (int k = 0; k < g_nbase; k++) if (g_base[k].addr == cur[i].addr && g_base[k].t == cur[i].t) { old = 1; break; }
        LOG("  cand %08X %-16s t=%.2f %s%s", cur[i].addr, cur[i].name, cur[i].t, pool ? "pool" : "HEAP", old ? " BASELINE" : " NEW");
    }
}

/* ready list of the session manager (state 3 waits for it): [+0x58C, +0x590), 4-byte entries */
static int ready_list(unsigned **list)
{
    unsigned m = *(unsigned *)SESSION_MGR;
    if (!m || IsBadReadPtr((void *)(m + 0x58C), 8)) return -1;
    unsigned b = *(unsigned *)(m + 0x58C), e = *(unsigned *)(m + 0x590);
    if (!b) return 0;
    if (e < b || ((e - b) & 3) || (e - b) / 4 > 64) return -1;
    if (list) *list = (unsigned *)b;
    return (e - b) / 4;
}

static void log_lists(const char *why)
{
    unsigned *pl = NULL, *rl = NULL; int np = participants(&pl), nr = ready_list(&rl);
    char buf[512]; int o = snprintf(buf, sizeof buf, "LISTS (%s) state=%d participants=%d [", why, *(int *)(SERVER_OBJ + 0x1070), np);
    for (int i = 0; i < np && o < 400; i++) o += snprintf(buf + o, sizeof buf - o, "%08X ", pl[i]);
    o += snprintf(buf + o, sizeof buf - o, "] ready=%d [", nr);
    for (int i = 0; i < nr && o < 480; i++) o += snprintf(buf + o, sizeof buf - o, "%08X ", rl[i]);
    snprintf(buf + o, sizeof buf - o, "]");
    LOG("%s", buf);
}

/* State 3 (Intermission/loading) only advances when the "still loading" list is empty. A client that joined late can never
 * finish loading, so its entry stays and the room freezes (timer pinned at 0). Dropping the stale entries lets the others race. */
static int prune_loading_list(const char *why)
{
    unsigned m = *(unsigned *)SESSION_MGR;
    if (!m || IsBadReadPtr((void *)(m + 0x58C), 8)) return 0;
    int n = ready_list(NULL);
    if (n <= 0) return 0;
    LOG("PRUNE (%s): dropping %d stale entr%s from the loading list", why, n, n == 1 ? "y" : "ies");
    log_lists("before prune");
    *(unsigned *)(m + 0x590) = *(unsigned *)(m + 0x58C);
    return n;
}

static void dump_participants(const char *why)
{
    unsigned *l; int n = participants(&l);
    LOG("participants (%s): %d", why, n);
    for (int i = 0; i < n; i++) { char tag[32]; snprintf(tag, sizeof tag, "player%d@%08X", i, l[i]); dump_area(l[i], 0x180, tag); }
}

/* ---- ghost reaper (server) ------------------------------------------------------------------------------------
 * A client that vanishes without a clean disconnect (crash, kill) stays in the server's participant list forever
 * and wedges the room (race never ends, next race never starts). The server reads its packets with WSARecvFrom
 * (IAT slot 0x2A8C47C): we record the last packet time per source address; a source silent for GhostSeconds while
 * others are active is a ghost -> call the game's own RemoveParticipant (FUN_013CFE00, thiscall on the session
 * manager) for the participant with the same index (connection order = participant order, to be verified).      */
#define IAT_WSARECVFROM 0x2A8C47C
#define IAT_RECVFROM    0x2A8C444
typedef struct { unsigned ip; unsigned short port; DWORD last; int alive; int late; } src_t;
static src_t g_src[16]; static int g_nsrc;
static CRITICAL_SECTION g_srccs; static int g_srccs_init;
static int g_waiting_room = 1, g_stuck_s = 35, g_admit_window = 0;       /* admitting at the session break was tested: the race still gets stuck, so off */
static volatile int g_stuck;
static int g_ghost_ms = 20000, g_stall_s = 900, g_postreap_s = 0, g_loadgrace_s = 45;       /* postreap 0 = off: forcing EndRace while others still race killed the server (see STATUS.md) */
static volatile DWORD g_reap_tick;
typedef int (WSAAPI *wsarecvfrom_t)(SOCKET, LPWSABUF, DWORD, LPDWORD, LPDWORD, struct sockaddr *, LPINT, LPWSAOVERLAPPED, LPWSAOVERLAPPED_COMPLETION_ROUTINE);
static wsarecvfrom_t real_wsarecvfrom;
static volatile int c_calls, c_ok, c_wb, c_err, c_ovl, c_nofrom, c_dropped;

/* Waiting room: a source that first appears after the session left its join phase (career state >= 3) never gets a per-player record
 * from the game, becomes a half-created player, keeps the race below its real racing state and finally trips the server's own 90 s
 * pre-race abort (see STATUS.md). So its packets are dropped; its game keeps retrying until the next session start. Returns 1 for such a source. */
static int note_src(struct sockaddr_in *a)
{
    int late = 0;
    EnterCriticalSection(&g_srccs);
    int i; for (i = 0; i < g_nsrc; i++) if (g_src[i].ip == a->sin_addr.s_addr && g_src[i].port == a->sin_port) break;
    int st = *(volatile DWORD *)SERVER_OBJ ? *(volatile int *)(SERVER_OBJ + 0x1070) : 0;
    int window = (st >= 9 && st <= 11);                        /* SessionSummary / Vote / WaitOnVotes: the game builds the next session for everybody connected */
    if (i < g_nsrc && g_src[i].late && window && g_admit_window) {
        g_src[i].late = 0; g_src[i].alive = 1;
        LOG("source %d: %s:%d  -> ADMITTED from the waiting room (session break, state %d)", i, inet_ntoa(a->sin_addr), ntohs(a->sin_port), st);
    }
    if (i == g_nsrc) {
        late = g_waiting_room && st >= 3 && !(window && g_admit_window);
        if (g_nsrc < 16) {
            g_src[i].ip = a->sin_addr.s_addr; g_src[i].port = a->sin_port; g_src[i].alive = !late; g_src[i].late = late; g_nsrc++;
            LOG("source %d: %s:%d%s", i, inet_ntoa(a->sin_addr), ntohs(a->sin_port), late ? "  -> WAITING ROOM (joined after the session started; held until the next session)" : "");
        }
    } else late = g_src[i].late;
    if (i < g_nsrc) g_src[i].last = GetTickCount();
    LeaveCriticalSection(&g_srccs);
    return late;
}
static int WSAAPI my_wsarecvfrom(SOCKET s, LPWSABUF b, DWORD n, LPDWORD got, LPDWORD fl, struct sockaddr *from, LPINT fromlen, LPWSAOVERLAPPED ov, LPWSAOVERLAPPED_COMPLETION_ROUTINE cr)
{
    int r = real_wsarecvfrom(s, b, n, got, fl, from, fromlen, ov, cr);
    c_calls++; if (ov) c_ovl++; if (!from) c_nofrom++;
    if (r == 0) c_ok++; else if (WSAGetLastError() == WSAEWOULDBLOCK) c_wb++; else c_err++;
    for (int tries = 0; tries < 64 && r == 0 && got && *got > 0 && from && from->sa_family == AF_INET && !ov; tries++) {
        if (!note_src((struct sockaddr_in *)from)) break;            /* waiting room: drop the packet and look at the next one */
        c_dropped++;
        r = real_wsarecvfrom(s, b, n, got, fl, from, fromlen, ov, cr);
    }
    if (ov && r == 0 && got && *got > 0 && from && from->sa_family == AF_INET) note_src((struct sockaddr_in *)from);
    return r;
}
typedef int (WSAAPI *recvfrom_t)(SOCKET, char *, int, int, struct sockaddr *, int *);
static recvfrom_t real_recvfrom;
static volatile int c_rf_calls, c_rf_ok;
static int WSAAPI my_recvfrom(SOCKET s, char *buf, int len, int flags, struct sockaddr *from, int *fromlen)
{
    int r = real_recvfrom(s, buf, len, flags, from, fromlen);
    c_rf_calls++;
    for (int tries = 0; tries < 64 && r > 0 && from && from->sa_family == AF_INET; tries++) {
        c_rf_ok++;
        if (!note_src((struct sockaddr_in *)from)) break;            /* waiting room */
        c_dropped++;
        r = real_recvfrom(s, buf, len, flags, from, fromlen);
    }
    return r;
}
static void install_recv_hook(void)
{
    InitializeCriticalSection(&g_srccs); g_srccs_init = 1;
    DWORD *slot = (DWORD *)IAT_WSARECVFROM;
    if (IsBadReadPtr(slot, 4) || !*slot) { LOG("ghost reaper: WSARecvFrom slot not readable"); return; }
    real_wsarecvfrom = (wsarecvfrom_t)*slot;
    DWORD v = (DWORD)my_wsarecvfrom;
    mem_write(IAT_WSARECVFROM, &v, 4);
    DWORD *slot2 = (DWORD *)IAT_RECVFROM;
    if (!IsBadReadPtr(slot2, 4) && *slot2) { real_recvfrom = (recvfrom_t)*slot2; DWORD v2 = (DWORD)my_recvfrom; mem_write(IAT_RECVFROM, &v2, 4); }
    LOG("ghost reaper: WSARecvFrom/recvfrom hooked (real %08X / %08X), ghost after %d s", (unsigned)real_wsarecvfrom, (unsigned)real_recvfrom, g_ghost_ms / 1000);
}
static int reap_participant(int idx, const char *why)
{
    unsigned *l; int n = participants(&l);
    if (idx < 0 || idx >= n) { LOG("reap: index %d out of range (%d participants)", idx, n); return 0; }
    unsigned mgr = *(unsigned *)SESSION_MGR, p = l[idx];
    LOG("REAP participant %d/%d @%08X (%s)", idx, n, p, why);
    ((void (__attribute__((thiscall)) *)(void *, void *))0x13CFE00)((void *)mgr, (void *)p);
    LOG("REAP done, participants now %d", participants(NULL));
    return 1;
}

/* Find a participant by driver name: look for the ASCII name inside the participant object and one pointer level below it.
 * Best effort (the participant layout is not fully mapped); returns -1 when not found. */
static int scan_for_str(unsigned a, unsigned len, const char *name, size_t nl)
{
    if (a < 0x10000 || IsBadReadPtr((void *)(DWORD_PTR)a, len)) return 0;
    const unsigned char *b = (const unsigned char *)(DWORD_PTR)a;
    for (unsigned i = 0; i + nl <= len; i++) if (!memcmp(b + i, name, nl) && (i + nl == len || b[i + nl] == 0 || b[i + nl] < 32)) return 1;
    return 0;
}
static int find_participant_by_name(const char *name)
{
    unsigned *l; int n = participants(&l); size_t nl = strlen(name);
    if (nl < 2) return -1;
    for (int i = 0; i < n; i++) {
        unsigned p = l[i];
        if (scan_for_str(p, 0x400, name, nl)) return i;
        for (unsigned off = 0; off < 0x200; off += 4) {
            if (IsBadReadPtr((void *)(DWORD_PTR)(p + off), 4)) break;
            unsigned q = *(unsigned *)(DWORD_PTR)(p + off);
            if (q > 0x10000 && scan_for_str(q, 0x80, name, nl)) return i;
        }
    }
    return -1;
}
/* Actions that must run on the game's own thread are queued here and executed by nfl_game_tick(), which is called
 * once per server frame from a stub we put at the call to FUN_01403A90 (0x0140455B). */
static volatile int g_pending_reap = -1, g_pending_endrace = 0, g_pending_setstate = -1;
void nfl_game_tick(void)
{
    int r = g_pending_reap;
    if (r >= 0) { g_pending_reap = -1; reap_participant(r, "queued"); }
    if (g_pending_setstate >= 0) {    /* ServerCareerManager::SetState = FUN_013FBD70(this = obj 0x27A3300, newState) */
        int ns = g_pending_setstate; g_pending_setstate = -1;
        LOG("SETSTATE %d on the game thread", ns);
        ((void (__attribute__((thiscall)) *)(void *, int))0x13FBD70)((void *)SERVER_OBJ, ns);
    }
    if (g_pending_endrace) {          /* FUN_013CA160(session manager): marks every participant's result and ends the race */
        g_pending_endrace = 0; unsigned mgr = *(unsigned *)SESSION_MGR;
        if (mgr) { LOG("ENDRACE on the game thread"); ((void (__fastcall *)(void *))0x13CA160)((void *)mgr); }
    }
}
__attribute__((naked)) void nfl_tick_stub(void)
{ __asm__ volatile("pushfl\n pushal\n call _nfl_game_tick\n popal\n popfl\n pushl $0x1403A90\n ret\n"); }

static DWORD WINAPI ghost_thread(LPVOID x)
{
    for (;;) {
        Sleep(1000);
        if (g_stall_s > 0 && *(volatile DWORD *)SERVER_OBJ && *(volatile int *)(SERVER_OBJ + 0x1070) == 6 && *(float *)(SERVER_OBJ + 0x1074) > g_stall_s && !g_pending_endrace) {
            LOG("stall watchdog: race running for %.0f s, ending it", *(float *)(SERVER_OBJ + 0x1074)); g_pending_endrace = 1; Sleep(2000); g_pending_setstate = 7; Sleep(3000);
        }
        if (g_loadgrace_s > 0 && *(volatile DWORD *)SERVER_OBJ) {       /* frozen loading list (late joiner) */
            static DWORD stuck_since; static int stuck_n;
            int st3 = *(volatile int *)(SERVER_OBJ + 0x1070) == 3 && *(volatile char *)(SERVER_OBJ + 0x1278) != 0;
            int n = st3 ? ready_list(NULL) : 0;
            if (n > 0) {
                if (!stuck_since || n != stuck_n) { stuck_since = GetTickCount(); stuck_n = n; }
                else if (GetTickCount() - stuck_since > (DWORD)g_loadgrace_s * 1000) { prune_loading_list("loading list stuck"); stuck_since = 0; }
            } else stuck_since = 0;
        }
        if (g_reap_tick && *(volatile DWORD *)SERVER_OBJ) {          /* after removing a ghost, the race still waits for it: end it if it is still running */
            if (*(volatile int *)(SERVER_OBJ + 0x1070) != 6) g_reap_tick = 0;
            else if (g_postreap_s > 0 && GetTickCount() - g_reap_tick > (DWORD)g_postreap_s * 1000) {
                LOG("recovery: race still running %d s after removing a ghost, ending it", g_postreap_s);
                g_reap_tick = 0; g_pending_endrace = 1; Sleep(2000); g_pending_setstate = 7; Sleep(3000);
            }
        }
        if (!g_srccs_init || g_ghost_ms <= 0 || !*(volatile DWORD *)SERVER_OBJ) continue;
        DWORD now = GetTickCount(); int fresh = 0, ghost = -1;
        /* Only judge silence while a race is running: loading screens stall a healthy client's network for 20+ s.
         * A ghost is a source that WAS talking during this race and then went silent while another one still talks. */
        static int last_state = -1; static DWORD t6;
        int st = *(volatile int *)(SERVER_OBJ + 0x1070);
        if (st != last_state) { last_state = st; if (st == 6) t6 = now; }
        if (st != 6) continue;
        EnterCriticalSection(&g_srccs);
        for (int i = 0; i < g_nsrc; i++) if (g_src[i].alive && now - g_src[i].last < 3000) fresh++;
        for (int i = 0; i < g_nsrc; i++) if (g_src[i].alive && g_src[i].last >= t6 + 2000 && now - g_src[i].last > (DWORD)g_ghost_ms) { ghost = i; break; }
        LeaveCriticalSection(&g_srccs);
        if (ghost < 0 || fresh == 0) continue;                    /* nobody else talking: the whole room is idle, not a ghost */
        /* map: the k-th live source (connection order, ghosts already removed excluded) <-> k-th participant */
        int k = 0; EnterCriticalSection(&g_srccs); for (int i = 0; i < ghost; i++) if (g_src[i].alive) k++; g_src[ghost].alive = 0; LeaveCriticalSection(&g_srccs);
        LOG("ghost: source %d silent, queueing removal of participant %d for the game thread", ghost, k);
        g_pending_reap = k; g_reap_tick = GetTickCount();
    }
}


static void dump_area(unsigned a, int len, const char *tag);
static volatile int g_hold;
static float g_preload_secs = 60.0f;
static int g_ctl_port = 25299;
static DWORD WINAPI control_thread(LPVOID p)
{
    WSADATA wd; WSAStartup(MAKEWORD(2, 2), &wd);
    SOCKET s = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    struct sockaddr_in a = {0}; a.sin_family = AF_INET; a.sin_port = htons(g_ctl_port); a.sin_addr.s_addr = inet_addr("127.0.0.1");
    if (s == INVALID_SOCKET || bind(s, (struct sockaddr *)&a, sizeof a)) { LOG("control: cannot bind port %d", g_ctl_port); return 0; }
    DWORD tmo = 50; setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (char *)&tmo, sizeof tmo);
    LOG("control channel on 127.0.0.1:%d", g_ctl_port);
    for (;;) {
        if (*(volatile DWORD *)SERVER_OBJ && g_hold && *(volatile int *)(SERVER_OBJ + 0x1070) == 2) *(float *)(SERVER_OBJ + 0x1074) = 0.0f;
        char buf[128], out[256]; struct sockaddr_in from; int fl = sizeof from;
        int n = recvfrom(s, buf, sizeof buf - 1, 0, (struct sockaddr *)&from, &fl);
        if (n <= 0) continue;
        buf[n] = 0; while (n && (buf[n - 1] == '\n' || buf[n - 1] == '\r')) buf[--n] = 0;
        int state = *(int *)(SERVER_OBJ + 0x1070);
        if (!strcmp(buf, "status"))
        {
            int waiting = 0; DWORD tnow = GetTickCount();
            EnterCriticalSection(&g_srccs); for (int i = 0; i < g_nsrc; i++) if (g_src[i].late && tnow - g_src[i].last < 10000) waiting++; LeaveCriticalSection(&g_srccs);
            snprintf(out, sizeof out, "state=%d timer=%.1f session=%d race=%d hold=%d players=%d waiting=%d stuck=%d", state, *(float *)(SERVER_OBJ + 0x1074),
                     *(int *)(SERVER_OBJ + 4), *(int *)(SERVER_OBJ + 8), g_hold, participants(NULL), waiting, g_stuck);
        }
        else if (!strcmp(buf, "start")) {
            if (state == 2) { g_hold = 0; *(float *)(SERVER_OBJ + 0x1074) = g_preload_secs + 0.05f; snprintf(out, sizeof out, "ok starting"); LOG("control: start"); }
            else snprintf(out, sizeof out, "error not in pre-load (state=%d)", state);
        } else if (!strncmp(buf, "scanf ", 6)) {
            double v = 0, t = 0.01; sscanf(buf + 6, "%lf %lf", &v, &t); mem_scan(1, v, t, out, sizeof out);
        } else if (!strcmp(buf, "sources")) {
            int n = 0; DWORD now = GetTickCount(); out[0] = 0;
            if (g_srccs_init) { EnterCriticalSection(&g_srccs);
                for (int i = 0; i < g_nsrc; i++) n += snprintf(out + n, sizeof out - n, "%s%d:%s:%d idle=%.1fs %s", i ? " | " : "", i, inet_ntoa(*(struct in_addr *)&g_src[i].ip), ntohs(g_src[i].port), (now - g_src[i].last) / 1000.0, g_src[i].alive ? "" : "(reaped)");
                LeaveCriticalSection(&g_srccs); }
            if (!out[0]) snprintf(out, sizeof out, "no sources");
            { int l = (int)strlen(out); snprintf(out + l, sizeof out - l, " [calls=%d ok=%d wouldblock=%d err=%d overlapped=%d nofrom=%d]", c_calls, c_ok, c_wb, c_err, c_ovl, c_nofrom); l = (int)strlen(out); snprintf(out + l, sizeof out - l, " [recvfrom calls=%d ok=%d]", c_rf_calls, c_rf_ok); }
        } else if (!strncmp(buf, "setstate ", 9)) {
            g_pending_setstate = atoi(buf + 9); snprintf(out, sizeof out, "ok queued (runs on the next server frame)");
        } else if (!strcmp(buf, "endrace")) {
            g_pending_endrace = 1; snprintf(out, sizeof out, "ok queued (runs on the next server frame)");
        } else if (!strncmp(buf, "reapname ", 9)) {
            int ix = find_participant_by_name(buf + 9);
            if (ix < 0) { snprintf(out, sizeof out, "error: no participant named '%s' found", buf + 9); LOG("reapname: '%s' not found", buf + 9); }
            else { g_pending_reap = ix; snprintf(out, sizeof out, "ok queued removal of participant %d ('%s')", ix, buf + 9); LOG("reapname: '%s' is participant %d", buf + 9, ix); }
        } else if (!strncmp(buf, "reap ", 5)) {
            int ix = atoi(buf + 5); g_pending_reap = ix; snprintf(out, sizeof out, "ok queued removal of participant %d (runs on the next server frame)", ix);
        } else if (!strcmp(buf, "results")) {
            results_report(); results_format(out, sizeof out); if (!out[0]) snprintf(out, sizeof out, "none");
        } else if (!strcmp(buf, "prune")) {
            snprintf(out, sizeof out, "ok dropped %d entries from the loading list", prune_loading_list("requested"));
        } else if (!strcmp(buf, "lists")) {
            log_lists("requested"); snprintf(out, sizeof out, "ok participants=%d ready=%d (details in the log)", participants(NULL), ready_list(NULL));
        } else if (!strcmp(buf, "players")) {
            dump_participants("requested"); snprintf(out, sizeof out, "ok %d participants dumped to the log", participants(NULL));
        } else if (!strncmp(buf, "dump ", 5)) {
            unsigned a = 0; int n = 64; sscanf(buf + 5, "%x %d", &a, &n); if (n > 512) n = 512;
            dump_area(a, n, "req"); snprintf(out, sizeof out, "ok dumped %d bytes at %08X to the log", n, a);
        } else if (!strncmp(buf, "scani ", 6)) {
            double v = 0; sscanf(buf + 6, "%lf", &v); mem_scan(0, v, 0, out, sizeof out);
        } else if (!strcmp(buf, "resync")) {
            /* Re-run the server's session setup so clients that connected after the first start get their
             * per-player records (FUN_013FFE50 builds them from the clients connected at that moment). */
            if (state == 2) {
                *(int *)(SERVER_OBJ + 0x107C) = -1;                       /* "no current session" -> full rebuild */
                ((void (__fastcall *)(void *))0x13FFE50)((void *)SERVER_OBJ);
                snprintf(out, sizeof out, "ok resynced"); LOG("control: resync");
            } else snprintf(out, sizeof out, "error resync only in pre-load (state=%d)", state);
        } else if (!strncmp(buf, "hold ", 5)) { g_hold = !strcmp(buf + 5, "on"); snprintf(out, sizeof out, "ok hold=%d", g_hold); LOG("control: hold %d", g_hold); }
        else snprintf(out, sizeof out, "error unknown command");
        sendto(s, out, (int)strlen(out), 0, (struct sockaddr *)&from, fl);
    }
}


/* ---- research: log changes of the server's per-player records (vector at obj+0xF54..+0xF58, 0x3C bytes each) ---- */
static DWORD WINAPI records_thread(LPVOID p)
{
    unsigned prev[16][15]; int have[16] = {0};
    for (;;) {
        Sleep(500);
        if (!*(volatile DWORD *)SERVER_OBJ) continue;
        unsigned b = *(unsigned *)(SERVER_OBJ + 0xF54), e = *(unsigned *)(SERVER_OBJ + 0xF58);
        if (!b || e < b || (e - b) % 0x3C || (e - b) / 0x3C > 16) continue;
        int n = (e - b) / 0x3C;
        for (int i = 0; i < n; i++) {
            unsigned *r = (unsigned *)(b + i * 0x3C);
            char line[1200]; int len = 0;
            for (int k = 0; k < 15; k++) {
                if (!have[i] || r[k] != prev[i][k]) {
                    float f; memcpy(&f, &r[k], 4);
                    len += snprintf(line + len, sizeof line - len, " +%02x=%x(%g)", k * 4, r[k], (f > -1e6f && f < 1e6f) ? f : 0.0f);
                    prev[i][k] = r[k];
                }
            }
            have[i] = 1;
            if (len) LOG("rec[%d/%d] state=%d t=%.1f:%s", i, n, *(int *)(SERVER_OBJ + 0x1070), *(float *)(SERVER_OBJ + 0x1074), line);
        }
    }
}


/* ---- research/events: log every server state change; dump the suspected result areas when results begin ---- */
static void dump_area(unsigned a, int len, const char *tag)
{
    if (IsBadReadPtr((void *)a, len)) { LOG("dump %s %08X: unreadable", tag, a); return; }
    char line[400];
    for (int o = 0; o < len; o += 32) {
        int n = 0;
        for (int k = 0; k < 32 && o + k < len; k += 4) n += snprintf(line + n, sizeof line - n, " %08x", *(unsigned *)(a + o + k));
        LOG("dump %s %08X:%s", tag, a + o, line);
    }
}
/* the server aborts the whole session (self-shutdown) when, in career state 6, the RACE object stays below its own state 6 for longer than config+0x58
 * seconds (FUN_01403A90, see STATUS.md): log the numbers so such a stuck race is visible before it kills the server */
static void log_raceobj(const char *why)
{
    unsigned m = *(unsigned *)SESSION_MGR; unsigned cfg = *(unsigned *)0x2880420;
    if (!m || IsBadReadPtr((void *)(m + 0x5AC), 4)) return;
    unsigned ro = *(unsigned *)(m + 0x5AC);
    float c20 = 0, c58 = 0, c5c = 0;
    if (cfg && !IsBadReadPtr((void *)(cfg + 0x20), 0x44)) { c20 = *(float *)(cfg + 0x20); c58 = *(float *)(cfg + 0x58); c5c = *(float *)(cfg + 0x5C); }
    if (ro && !IsBadReadPtr((void *)(ro + 0xB8), 0x10))
        LOG("RACEOBJ (%s) career=%d t_in_state=%.1f raceobj=%08X race_state=%d race_elapsed=%.1f | cfg activity=%.0f abort_prerace(+58)=%.0f careerTimeout(+5C)=%.0f | participants=%d",
            why, *(int *)(SERVER_OBJ + 0x1070), *(float *)(SERVER_OBJ + 0x1074), ro, *(int *)(ro + 0xB8), *(float *)(ro + 0xC0), c20, c58, c5c, participants(NULL));
    else
        LOG("RACEOBJ (%s) career=%d no race object", why, *(int *)(SERVER_OBJ + 0x1070));
}

static DWORD WINAPI events_thread(LPVOID p)
{
    int last = -1;
    for (;;) {
        Sleep(200);
        if (!*(volatile DWORD *)SERVER_OBJ) continue;
        int st = *(int *)(SERVER_OBJ + 0x1070);
        { static DWORD t_last, stuck_since; DWORD now = GetTickCount();
          if (st == 6 && now - t_last > 4000) { t_last = now; log_raceobj("periodic"); }
          unsigned m = *(unsigned *)SESSION_MGR; int rs = -1;
          if (st == 6 && m && !IsBadReadPtr((void *)(m + 0x5AC), 4)) { unsigned ro = *(unsigned *)(m + 0x5AC); if (ro && !IsBadReadPtr((void *)(ro + 0xB8), 4)) rs = *(int *)(ro + 0xB8); }
          if (st == 6 && rs >= 0 && rs < 6) {
              if (!stuck_since) stuck_since = now;
              else if (!g_stuck && g_stuck_s > 0 && now - stuck_since > (DWORD)g_stuck_s * 1000) { g_stuck = 1; LOG("STUCK RACE: race object has been below state 6 (now %d) for %d s; the server aborts itself at 90 s", rs, g_stuck_s); }
          } else { stuck_since = 0; g_stuck = 0; } }
        if (st == last) continue;
        LOG("EVENT state %d -> %d  session=%d race=%d", last, st, *(int *)(SERVER_OBJ + 4), *(int *)(SERVER_OBJ + 8));
        last = st;
        log_lists("state change");
        if (st == 6) results_baseline();
        if (st == 8) results_report();
        if (st == 3 || st == 6 || st == 7 || st == 8 || st == 9) dump_participants(st == 6 ? "race start" : st == 7 ? "results" : "state change");
    }
    return 0;
}


static void server_set_fields(void)
{
    *(int *)(SERVER_OBJ + 8) = -1;
    if (g_custom) *(int *)(SERVER_OBJ + 4) = g_custom_session;
    if (g_preload_state) *(int *)(SERVER_OBJ + 0x1070) = 2;
}
static DWORD WINAPI server_data_thread(LPVOID p)
{
    /* The game only starts its first session on the server's first tick. The state must already say
     * "pre-load, waiting for players" by then, or the session (and its races) start immediately.
     * So: poll tightly for the vtable pointer, then keep re-writing for 400 ms to beat the
     * constructor's own field initialisation. */
    for (int i = 0; i < 60000 && !*(volatile DWORD *)SERVER_OBJ; i++) Sleep(1);
    if (!*(volatile DWORD *)SERVER_OBJ) { LOG("server object never appeared"); return 0; }
    /* The game auto-starts a first session right after construction (race index -1 -> 0). The original
     * helper rewinds that afterwards: race index back to -1 and the state to pre-load, so the real countdown
     * runs. Doing it BEFORE the auto-start does not work, so wait for it, then write once. */
    for (int i = 0; i < (g_custom ? 1500 : 30000) && !g_session_started; i++) Sleep(1);   /* custom: hook not installed, fixed wait */
    Sleep(300);
    server_set_fields();
    LOG("server object ready: race index -1, session %d%s, career state %d", *(int *)(SERVER_OBJ + 4),
        g_custom ? " (forced)" : "", *(int *)(SERVER_OBJ + 0x1070));
    if (GetEnvironmentVariableA("NFSTR_LAN_TRACE2", NULL, 0)) {         /* debugging: dump the whole object every second */
        char fp[MAX_PATH + 40]; snprintf(fp, sizeof fp, "%s\\Scripts\\obj_trace.txt", g_dir);
        FILE *tf = fopen(fp, "w");
        for (int i = 0; tf && i < 150; i++, Sleep(1000)) {
            fprintf(tf, "%d", i);
            for (int k = 0; k < 0x1100; k += 4) fprintf(tf, " %x", *(unsigned *)(SERVER_OBJ + k));
            fputc('\n', tf); fflush(tf);
        }
    }
    if (GetEnvironmentVariableA("NFSTR_LAN_TRACE", NULL, 0)) {          /* debugging: log field changes */
        int a = -99, b = -99, c = -99, d = -99;
        for (int i = 0; i < 400; i++, Sleep(250)) {
            int x = *(int *)(SERVER_OBJ + 4), y = *(int *)(SERVER_OBJ + 8), z = *(int *)(SERVER_OBJ + 0x1070), w = *(int *)(SERVER_OBJ + 0x107c);
            if (x != a || y != b || z != c || w != d) { LOG("t=%.1fs obj: session=%d race=%d state=%d cur=%d", i * 0.25, x, y, z, w); a = x; b = y; c = z; d = w; }
        }
    }
    return 0;
}

static void server_patches(void)
{
    int force_pl = ini_int("ServerSettings", "ForcePlaylistLoading", 1);
    int custom   = ini_int("ServerSettings", "ForceCustomPlaylistSessionFirstID", 0);

    g_custom = custom;
    g_custom_session = ini_int("ServerSettings", "PlaylistSessionID", 0);
    g_preload_state = ini_int("ServerSettings", "ForceCareerStatePreLoad", 1);
    g_hold = ini_int("ServerSettings", "HoldStart", 0);
    g_preload_secs = (float)ini_int("ServerSettings", "PreloadSeconds", 60);
    g_ctl_port = ini_int("ServerSettings", "ControlPort", 25299);
    g_loadgrace_s = ini_int("ServerSettings", "LoadGraceSeconds", 45);
    g_waiting_room = ini_int("ServerSettings", "WaitingRoom", 1);
    g_admit_window = ini_int("ServerSettings", "AdmitAtSessionBreak", 0);
    g_stuck_s = ini_int("ServerSettings", "StuckRaceSeconds", 35);
    g_ghost_ms = ini_int("ServerSettings", "GhostSeconds", 20) * 1000;
    g_stall_s = ini_int("ServerSettings", "RaceStallSeconds", 900);
    g_postreap_s = ini_int("ServerSettings", "PostReapSeconds", 0);
    {   /* redirect the per-frame call (call rel32 at 0x140455B -> FUN_01403A90) to our stub, which jumps on to the original */
        DWORD rel = (DWORD)nfl_tick_stub - 0x1404560;
        BYTE oldb[4] = {0x30, 0xF5, 0xFF, 0xFF};
        patch("game-thread-tick", 0x140455C, oldb, &rel, 4);
    }
    install_recv_hook();
    CreateThread(0, 0, ghost_thread, 0, 0, 0);
    CreateThread(0, 0, control_thread, 0, 0, 0);
    CreateThread(0, 0, events_thread, 0, 0, 0);
    if (GetEnvironmentVariableA("NFSTR_LAN_RECORDS", NULL, 0)) CreateThread(0, 0, records_thread, 0, 0, 0);
    CreateThread(0, 0, server_data_thread, 0, 0, 0);
    if (force_pl) {
        BYTE o1[10] = {0x83,0xEE,0x01,0x74,0x11,0x83,0xEE,0x01,0x75,0x1E};
        BYTE n1[10] = {0x90,0x90,0x90,0x90,0x90,0x83,0xEE,0x02,0x75,0x0C};
        patch("playlist-loading", 0x14011C8, o1, n1, 10);
        jmp_to("playlist-loading", 0x1400168, (BYTE[]){0x8B,0x46,0x0C,0x8B,0x48,0x10}, 6, 0, nfl_server_cave_pl);
        if (!custom)
            jmp_to("session-sync", 0x13FFE3F, (BYTE[]){0xC7,0x86,0x84,0x10,0,0,1,0,0,0}, 10, 0, nfl_server_cave_sess);
    }
    if (ini_int("ServerSettings", "ForceCareerStatePreLoad", 1)) {
        nop("preload", 0x1401BEF, (BYTE[]){0x89,0x9E,0x70,0x10,0,0}, 6);
    }
    if (ini_int("ServerSettings", "DisableDebugCarChangeInRace", 0))
        patch("no-car-change", 0x13CDC8C, NULL, (BYTE[]){0xEB}, 1);
}

/* ---- window title status ----
 * Client: NFSTR LANs: Connected | Playlist A<set> #<id> | Session: <n>, Race: <n>
 * Server: NFSTR LANs: Server | Session: <n>
 * Same prefix as the original helper, so scripts reading the title keep working. The game
 * itself rewrites the title; the call at 0xE5FE52 is removed on the client to keep ours. */
#define CLIENT_STATE_PTR 0x2888F7C  /* +0xA4 connect status (3 = connected) */
#define CLIENT_LEVEL_STR 0x289BDC8
static DWORD WINAPI title_thread(LPVOID p)
{
    char t[160], last[160] = "";
    for (;;) {
        Sleep(1000);
        HWND w = FindWindowA("Need for Speed(TM) The Run", NULL);
        if (!w) continue;
        if (g_is_server) {
            snprintf(t, sizeof t, "NFSTR LANs: Server | Session: %d", *(int *)(SERVER_OBJ + 4));
        } else {
            char *st = *(char **)CLIENT_STATE_PTR, *go = *(char **)CLIENT_GAMEOBJ;
            if (!st) continue;
            int status = *(int *)(st + 0xA4);
            const char *lvl = (const char *)CLIENT_LEVEL_STR;
            if (status == 3 && lvl[0] && strcmp(lvl, "_c4/Levels/FE/FrontEnd/FrontEnd") && go)
                snprintf(t, sizeof t, "NFSTR LANs: Playlist A%d #%d | Session: %d, Race: %d",
                         g_set, g_id, *(int *)(go + 4), *(int *)(go + 8) + 1);
            else if (status == 3) snprintf(t, sizeof t, "NFSTR LANs: Connected");
            else snprintf(t, sizeof t, "NFSTR LANs: Attempting to Connect... (status %d)", status);
        }
        if (strcmp(t, last)) { SetWindowTextA(w, t); strcpy(last, t); LOG("title: %s", t); }
    }
    return 0;
}

static void init(void)
{
    char exe[MAX_PATH], *s;
    GetModuleFileNameA(NULL, exe, MAX_PATH);
    strcpy(g_dir, exe); s = strrchr(g_dir, '\\'); if (s) *s = 0;
    char lp[MAX_PATH + 40];
    g_is_server = !memcmp((void *)ROLE_PROBE, "\x52\x6A\x01", 3);
    snprintf(lp, sizeof lp, "%s\\Scripts\\NFSTR_LAN_%s.log", g_dir, g_is_server ? "server" : "client");
    g_log = fopen(lp, "w");
    snprintf(g_ini, sizeof g_ini, "%s\\Scripts\\NFSTR_LAN.ini", g_dir);
    if (GetFileAttributesA(g_ini) == INVALID_FILE_ATTRIBUTES)
        snprintf(g_ini, sizeof g_ini, "%s\\Scripts\\NFSTR_LANOnlineHelper.ini", g_dir);
    LOG("NFSTR_LAN loaded, exe=%s ini=%s", exe, g_ini);

    char hp[MAX_PATH + 48]; snprintf(hp, sizeof hp, "%s\\Scripts\\NFSTR_LANOnlineHelper.asi", g_dir);
    /* the ASI loader loads alphabetically, so we may run before the original: check the file, not the module */
    /* debugging aid: NFSTR_LAN_FORCE=1 skips this guard, NFSTR_LAN_ROLES=client|server limits our roles */
    if (!GetEnvironmentVariableA("NFSTR_LAN_FORCE", NULL, 0) &&
        (GetModuleHandleA("NFSTR_LANOnlineHelper.asi") || GetFileAttributesA(hp) != INVALID_FILE_ATTRIBUTES)) {
        LOG("the original NFSTR_LANOnlineHelper.asi is loaded too - not patching, remove one of them");
        return;
    }
    if (!memcmp((void *)ROLE_PROBE, "\x6A\x00\x52", 3))      g_is_server = 0;
    else if (!memcmp((void *)ROLE_PROBE, "\x52\x6A\x01", 3)) g_is_server = 1;
    else { LOG("unknown exe build (probe bytes at %X do not match), not patching", ROLE_PROBE); return; }
    LOG("role: %s", g_is_server ? "server" : "client");
    { char roles[32] = "client,server"; GetEnvironmentVariableA("NFSTR_LAN_ROLES", roles, sizeof roles);
      if (!strstr(roles, g_is_server ? "server" : "client")) { LOG("role disabled by NFSTR_LAN_ROLES=%s", roles); return; } }

    g_set = ini_int("PlaylistSettings", "ForcePlaylistSetArrayID", 0);
    g_id  = ini_int("PlaylistSettings", "ForcePlaylistID", 0);
    common_patches();
    if (g_is_server) server_patches(); else client_patches();
    if (ini_int("OnlineSettings", "EnableWndTitleStatus", 1)) {
        if (!g_is_server)
            nop("title", 0xE5FE52, (BYTE[]){0x8B,0x8E,0x64,0x01,0x00,0x00,0x8B,0x01,0x8B,0x50,0x18,0x68,0x60,0x58,0x54,0x02,0xFF,0xD2}, 18);
        if (!g_is_server) CreateThread(0, 0, title_thread, 0, 0, 0);   /* the original has no server title thread either */
    }
}

BOOL WINAPI DllMain(HINSTANCE h, DWORD reason, LPVOID r)
{
    if (reason == DLL_PROCESS_ATTACH) { DisableThreadLibraryCalls(h); init(); }
    return TRUE;
}
