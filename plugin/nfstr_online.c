/* NFSTR_Online.asi - redirects the Blaze/GOS hostnames of NFS The Run to a
 * user-chosen server (local or remote). No game files are modified on disk.
 * Config: NFSTR_Online.ini next to the .asi:
 *   [Server]
 *   Host=127.0.0.1
 */
#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include <stdarg.h>
#include <stdlib.h>
#include <tlhelp32.h>

static char g_host[128] = "127.0.0.1";
static FILE *g_log;
static char g_ini[MAX_PATH];

static void logf_(const char *fmt, ...)
{
    if (!g_log) return;
    va_list ap; va_start(ap, fmt);
    vfprintf(g_log, fmt, ap); va_end(ap);
    fputc('\n', g_log); fflush(g_log);
}

static int is_ea_host(const char *n)
{
    if (!n) return 0;
    size_t l = strlen(n);
    return l > 7 && (!_stricmp(n + l - 7, ".ea.com") || !_stricmp(n + l - 4, ".ea."));
}

typedef struct hostent *(WSAAPI *gethostbyname_t)(const char *);
typedef int (WSAAPI *getaddrinfo_t)(PCSTR, PCSTR, const ADDRINFOA *, PADDRINFOA *);
static gethostbyname_t real_gethostbyname;
static getaddrinfo_t real_getaddrinfo;

/* Inline hook: overwrite the function prologue with a jmp. To call the original
 * we restore the bytes, call, and re-patch under a lock (simple, no trampoline). */
typedef struct { BYTE *fn; BYTE saved[5]; BYTE jmp[5]; } hook_t;
static CRITICAL_SECTION g_cs;
static hook_t h_ghbn, h_gai;

static void hook_install(hook_t *h, void *fn, void *repl)
{
    DWORD old;
    h->fn = (BYTE *)fn;
    memcpy(h->saved, fn, 5);
    h->jmp[0] = 0xE9;
    *(DWORD *)(h->jmp + 1) = (DWORD)((BYTE *)repl - h->fn - 5);
    VirtualProtect(fn, 5, PAGE_EXECUTE_READWRITE, &old);
    memcpy(fn, h->jmp, 5);
    FlushInstructionCache(GetCurrentProcess(), fn, 5);
}
static void hook_off(hook_t *h) { memcpy(h->fn, h->saved, 5); FlushInstructionCache(GetCurrentProcess(), h->fn, 5); }
static void hook_on(hook_t *h)  { memcpy(h->fn, h->jmp, 5);   FlushInstructionCache(GetCurrentProcess(), h->fn, 5); }

static struct hostent *WSAAPI my_gethostbyname(const char *name)
{
    struct hostent *r;
    if (is_ea_host(name)) { logf_("gethostbyname %s -> %s", name, g_host); name = g_host; }
    else logf_("gethostbyname %s", name ? name : "(null)");
    EnterCriticalSection(&g_cs);
    hook_off(&h_ghbn);
    r = real_gethostbyname(name);
    hook_on(&h_ghbn);
    LeaveCriticalSection(&g_cs);
    return r;
}

static int WSAAPI my_getaddrinfo(PCSTR name, PCSTR svc, const ADDRINFOA *h, PADDRINFOA *res)
{
    int r;
    if (is_ea_host(name)) { logf_("getaddrinfo %s -> %s", name, g_host); name = g_host; }
    else logf_("getaddrinfo %s", name ? name : "(null)");
    EnterCriticalSection(&g_cs);
    hook_off(&h_gai);
    r = real_getaddrinfo(name, svc, h, res);
    hook_on(&h_gai);
    LeaveCriticalSection(&g_cs);
    return r;
}

/* ---- SSL certificate check -------------------------------------------
 * Blaze's SSL layer (ProtoSSL) rejects any server certificate it cannot
 * chain to EA's CAs, unless the "no cert" flag at [state+0x2F8] is set.
 * Two branches test that flag; we make both unconditional (jne -> jmp).
 * Each site is verified byte for byte against this exact exe first.
 *   0x00DE6B79: cmp byte [edi+2F8h],0 / jne +8   (error 0x1004)
 *   0x00DE6B9A: cmp byte [edi+2F8h],0 / jne +8   (error 0x1005)
 */
static void patch_ssl_check(void)
{
    static const BYTE pat[] = { 0x80, 0xBF, 0xF8, 0x02, 0x00, 0x00, 0x00, 0x75, 0x08 };
    static const DWORD sites[] = { 0x00DE6B79, 0x00DE6B9A };
    for (int i = 0; i < 2; ++i) {
        BYTE *a = (BYTE *)(DWORD_PTR)sites[i];
        DWORD old;
        if (IsBadReadPtr(a, sizeof pat) || memcmp(a, pat, sizeof pat)) {
            logf_("ssl: site %08lx does not match, not patching", (unsigned long)sites[i]);
            continue;
        }
        VirtualProtect(a + 7, 1, PAGE_EXECUTE_READWRITE, &old);
        a[7] = 0xEB;
        VirtualProtect(a + 7, 1, old, &old);
        FlushInstructionCache(GetCurrentProcess(), a + 7, 1);
        logf_("ssl: patched certificate check at %08lx", (unsigned long)sites[i]);
    }
}

/* ---- call tracer: logs when selected game functions are entered ------------
 * ini:  [Trace]  T0=0x8a1dc0:6:autolog_profile   (address:stolen-bytes:name)
 * The stolen bytes must be whole, position-independent instructions.
 */
static DWORD t6s, t6; static int posted, forced;
static const char *safe_str(DWORD p);
static int posted_flag_check(int *c) { (void)c; return 0; }
static void __cdecl trace_hit(const char *name, DWORD ecx, DWORD arg1, DWORD arg2, DWORD arg3, DWORD ret)
{
    if (strncmp(name, "update", 6)) { SYSTEMTIME st_; GetLocalTime(&st_); logf_("CALL %02d:%02d:%02d %s ecx=%08lx args=%08lx %08lx %08lx ret=%08lx", st_.wHour, st_.wMinute, st_.wSecond, name, ecx, arg1, arg2, arg3, ret); }
    DWORD a = 0, b = 0, c = 0;
    /* Menu research: observe the persona callback before interpreting the later
     * code-5 notification as a matchmaking request. No game state is changed. */
    /* Game's own debug log function: print its format string (shows BLAZE/GameManager reasoning). */
    if (!strcmp(name, "blazelog")) {
        if (arg1 > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)arg1, 4)) {
            const char *t = safe_str(arg1);
            if (t[0] >= 32 && t[0] < 127) logf_("GAMELOG \"%.200s\"", t);
        }
        return;
    }
    if (!strcmp(name, "netplayer")) {
#define RD(a) (((a) > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)(a), 4)) ? *(DWORD *)(DWORD_PTR)(a) : 0)
        DWORD o1 = RD(ecx + 0x27a4), o2 = RD(ecx + 0x27a8);
        DWORD vt = RD(o1);
        logf_("NETPLAYER this=%08lx [+27a4]=%08lx [+27a8]=%08lx vt=%08lx slot1c=%08lx slot58=%08lx param3=%08lx",
              ecx, o1, o2, vt, RD(vt + 0x1c), RD(vt + 0x58), arg2);
        if (o1 && !strcmp(name, "netplayer")) {
            static int scanned;
            if (!scanned++) {
                /* look for our host address 127.0.0.1 (7f000001), LAN ip (c0a809ca) or port 25200 (6270) near the game object */
                for (long off = -0x600; off < 0x600; off += 4) {
                    DWORD v = RD(o1 + off);
                    if (v == 0x7f000001 || v == 0xc0a809ca || v == 25200 || v == 0x0100007f || v == 0xca09a8c0)
                        logf_("NETPLAYER scan o1%+ld = %08lx", off, v);
                }
                for (long off = -0xd0; off < 0x10; off += 16)
                    logf_("NETPLAYER mem o1%+04ld: %08lx %08lx %08lx %08lx", off, RD(o1 + off), RD(o1 + off + 4), RD(o1 + off + 8), RD(o1 + off + 12));
            }
        }
        if (o1) {
            DWORD b = RD(o1 - 0xc0), e = RD(o1 - 0xbc);
            logf_("NETPLAYER hostlist begin=%08lx end=%08lx count=%ld", b, e, (long)((e - b) / 4));
            for (DWORD q = b; b && q < e && q < b + 32; q += 4) {
                DWORD el = RD(q), evt = RD(el);
                logf_("NETPLAYER  el=%08lx vt=%08lx s28=%08lx +4..+30: %08lx %08lx %08lx %08lx %08lx %08lx %08lx %08lx", el, evt, RD(evt + 0x28),
                      RD(el + 4), RD(el + 8), RD(el + 0xc), RD(el + 0x10), RD(el + 0x14), RD(el + 0x18), RD(el + 0x1c), RD(el + 0x20));
            }
        }
        if (arg2) { DWORD pvt = RD(arg2); logf_("NETPLAYER p3 vt=%08lx s10=%08lx s18=%08lx s1c=%08lx", pvt, RD(pvt + 0x10), RD(pvt + 0x18), RD(pvt + 0x1c)); }
        return;
    }
    if (!strcmp(name, "menu_persona_callback")) {
        DWORD cfg = *(DWORD *)0x288041c;
        DWORD vt = 0, method = 0;
        if (ecx > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)(ecx + 0x40), 4)) {
            vt = *(DWORD *)(DWORD_PTR)(ecx + 0x40);
            if (vt > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)vt, 8))
                method = *(DWORD *)(DWORD_PTR)(vt + 4);
        }
        logf_("MENU persona callback error=%08lx result=%08lx owner40vt=%08lx method=%08lx cfg8a=%d",
              arg2, arg1, vt, method,
              cfg > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)cfg, 0x8b)
                  ? *(BYTE *)(DWORD_PTR)(cfg + 0x8a) : -1);
        return;
    }
    if (!strncmp(name, "resph", 5) && arg1 > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)arg1, 0xa0))
        for (int o = 0; o < 0xa0; o += 16)
            logf_("RESPH arg1 +%02x: %08lx %08lx %08lx %08lx", o, *(DWORD *)(DWORD_PTR)(arg1 + o), *(DWORD *)(DWORD_PTR)(arg1 + o + 4), *(DWORD *)(DWORD_PTR)(arg1 + o + 8), *(DWORD *)(DWORD_PTR)(arg1 + o + 12));
    if (!strncmp(name, "resph", 5) && arg1 > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)arg1, 0xa0))
        for (int o = 4; o < 0xa0; o += 4) {
            DWORD v = *(DWORD *)(DWORD_PTR)(arg1 + o);
            if (v > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)v, 4)) {
                const char *t = safe_str(v);
                int ok = t[0] >= 32 && t[0] < 127 && t[1] >= 32 && t[1] < 127;
                if (ok) logf_("RESPH str +%02x -> \"%s\"", o, t);
            }
        }
    if (arg1 > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)arg1, 0x34))
        logf_("TRACE %s arg1=%08lx type=%08lx code=%lu f30=%lu", name, arg1, *(DWORD *)(DWORD_PTR)(arg1 + 8), *(DWORD *)(DWORD_PTR)(arg1 + 0x28), *(DWORD *)(DWORD_PTR)(arg1 + 0x30));
    if (ecx > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)ecx, 0x20)) {
        a = *(DWORD *)(DWORD_PTR)(ecx + 0x4);
        b = *(DWORD *)(DWORD_PTR)(ecx + 0xc);
        c = *(DWORD *)(DWORD_PTR)(ecx + 0x10);
    }
    {
        DWORD st = 0, st11 = 0, p28 = 0, g = 0;
        if (ecx > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)(ecx + 0x28), 4)) {
            p28 = *(DWORD *)(DWORD_PTR)(ecx + 0x28);
            if (p28 > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)p28, 0x20)) {
                st = *(DWORD *)(DWORD_PTR)(p28 + 0xc);
                st11 = *(BYTE *)(DWORD_PTR)(p28 + 0x11);
                DWORD vt = *(DWORD *)(DWORD_PTR)p28;
                if (vt > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)(vt), 4)) g = vt;
            }
        }
        if (p28 > 0x10000 && !strncmp(name, "update", 6)) {
            static int cnt; ++cnt;
            if (cnt == 300 || cnt == 1500 || cnt == 2500)
                for (int o = 0; o < 0xA0; o += 16)
                    logf_("OBJDUMP #%d +%02x: %08lx %08lx %08lx %08lx", cnt, o, *(DWORD *)(DWORD_PTR)(p28 + o), *(DWORD *)(DWORD_PTR)(p28 + o + 4), *(DWORD *)(DWORD_PTR)(p28 + o + 8), *(DWORD *)(DWORD_PTR)(p28 + o + 12));
        }
        if (!strncmp(name, "update", 6)) {
            static DWORD last = 0xFFFFFFFF;
            if (st != last) {
                if (st == 5 || st == 0 || st == 2) { posted = 0; forced = 0; t6 = 0; t6s = 0; }
                SYSTEMTIME t; GetLocalTime(&t);
                logf_("STATE %02d:%02d:%02d.%03d  %lu -> %lu", t.wHour, t.wMinute, t.wSecond, t.wMilliseconds, last, st);
                last = st;
            }
            {
                static int dumped;
                if (st == 7 && !dumped && forced && GetTickCount() - t6 > 6000) {
                    dumped = 1;
                    DWORD X = IsBadReadPtr((void *)(DWORD_PTR)(p28 + 4), 4) ? 0 : *(DWORD *)(DWORD_PTR)(p28 + 4);
                    DWORD Y = (X && !IsBadReadPtr((void *)(DWORD_PTR)(X + 4), 4)) ? *(DWORD *)(DWORD_PTR)(X + 4) : 0;
                    DWORD V = (Y && !IsBadReadPtr((void *)(DWORD_PTR)Y, 4)) ? *(DWORD *)(DWORD_PTR)Y : 0;
                    logf_("CHAIN S=%08lx X=%08lx Y=%08lx vtable=%08lx", p28, X, Y, V);
                    if (Y && !IsBadReadPtr((void *)(DWORD_PTR)(Y + 0x7a0), 4)) {
                        DWORD O = *(DWORD *)(DWORD_PTR)(Y + 0x7a0);
                        DWORD OV = (O && !IsBadReadPtr((void *)(DWORD_PTR)O, 4)) ? *(DWORD *)(DWORD_PTR)O : 0;
                        logf_("CHAIN backendholder O=%08lx OV=%08lx fn8=%08lx", O, OV, (OV && !IsBadReadPtr((void *)(DWORD_PTR)(OV + 8), 4)) ? *(DWORD *)(DWORD_PTR)(OV + 8) : 0);
                        for (int i = 0; i < 8; ++i) {
                            DWORD bp = IsBadReadPtr((void *)(DWORD_PTR)(O + 4 + 4 * i), 4) ? 0 : *(DWORD *)(DWORD_PTR)(O + 4 + 4 * i);
                            DWORD bv = (bp > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)bp, 4)) ? *(DWORD *)(DWORD_PTR)bp : 0;
                            DWORD s4c = (bv > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)(bv + 0x4c), 4)) ? *(DWORD *)(DWORD_PTR)(bv + 0x4c) : 0;
                            logf_("CHAIN backends[%d]=%08lx vt=%08lx slot4c=%08lx", i, bp, bv, s4c);
                        }
                        {
                            int sw = GetPrivateProfileIntA("Debug", "SwitchTo", 0, g_ini);
                            if (sw > 0) {
                                typedef void (__attribute__((thiscall)) *sw_t)(void *, int, int, int);
                                logf_("SWITCH: calling holder %08lx switch(%d)", O, sw);
                                ((sw_t)0x1a2ea90)((void *)(DWORD_PTR)O, sw, -1, -1);
                                logf_("SWITCH done, holder current=%08lx", *(DWORD *)(DWORD_PTR)(O + 0x2c));
                            }
                        }
                        logf_("CHAIN holder O+0x28=%08lx +0x2c=%08lx +0x30=%08lx", *(DWORD *)(DWORD_PTR)(O + 0x28), *(DWORD *)(DWORD_PTR)(O + 0x2c), *(DWORD *)(DWORD_PTR)(O + 0x30));
                        if (OV) {
                            typedef DWORD (__attribute__((thiscall)) *get_t)(void *);
                            DWORD B = ((get_t)*(DWORD *)(DWORD_PTR)(OV + 8))((void *)(DWORD_PTR)O);
                            DWORD BV = (B && !IsBadReadPtr((void *)(DWORD_PTR)B, 4)) ? *(DWORD *)(DWORD_PTR)B : 0;
                            logf_("CHAIN backend B=%08lx BV=%08lx", B, BV);
                            if (BV && !IsBadReadPtr((void *)(DWORD_PTR)BV, 0x100))
                                for (int o = 0x40; o < 0x70; o += 4) logf_("CHAIN bvt+%02x = %08lx", o, *(DWORD *)(DWORD_PTR)(BV + o));
                        }
                    }
                    if (V && !IsBadReadPtr((void *)(DWORD_PTR)V, 0x100))
                        for (int o = 0; o < 0x100; o += 4) logf_("CHAIN vt+%02x = %08lx", o, *(DWORD *)(DWORD_PTR)(V + o));
                }
                if (st == 6 && !posted && GetPrivateProfileIntA("Debug", "Post4", 0, g_ini)) {
                    if (!t6s) t6s = GetTickCount();
                    else if (GetTickCount() - t6s > 3000) {
                        typedef void *(__attribute__((thiscall)) *mk_t)(void *, int);
                        typedef void (__attribute__((thiscall)) *post_t)(void *, void *, float);
                        void *mem = ((void *(__cdecl *)(DWORD))0x45deb0)(0x108);
                        void *msg = mem ? ((mk_t)0x7d39d0)(mem, 4) : NULL;
                        void *poster = *(void **)0x2888f54;
                        posted = 1;
                        logf_("POST4: mem=%p msg=%p poster=%p flag+5 before=%u", mem, msg, poster, *(BYTE *)(*(DWORD *)0x2881110 + 5));
                        if (msg && poster) { *(DWORD *)((BYTE *)msg + 0x30) = 0; ((post_t)0x4e6d70)(poster, msg, 0.0f); }
                        logf_("POST4 done, flag+5 now=%u", *(BYTE *)(*(DWORD *)0x2881110 + 5));
                    }
                }
            }
            {
                
                static int chk; if (posted_flag_check(&chk)) {}
                if (st == 6 && !forced && GetPrivateProfileIntA("Debug", "Force7", 0, g_ini)) {
                    if (!t6) t6 = GetTickCount();
                    else if (GetTickCount() - t6 > 4000) {
                        *(DWORD *)(DWORD_PTR)(p28 + 0xc) = 7; forced = 1;
                        logf_("FORCED state 6 -> 7 on object %08lx", p28);
                    }
                }
            }
            return;
        }
        logf_("TRACE %s (ecx=%08lx [+4]=%lx [+c]=%lx [+10]=%lx) [+28]=%lx ->state(+c)=%lu ->+11=%lu", name, ecx, a, b, c, p28, st, st11);
    }
}

static void install_trace(DWORD addr, int stolen, const char *name)
{
    BYTE *fn = (BYTE *)(DWORD_PTR)addr;
    if (stolen < 5 || stolen > 16 || IsBadReadPtr(fn, stolen)) { logf_("trace: bad target %08lx", addr); return; }
    BYTE *stub = (BYTE *)VirtualAlloc(NULL, 128, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE);
    char *nm = _strdup(name);
    int n = 0;
    stub[n++] = 0x9C;                         /* pushfd */
    stub[n++] = 0x60;                         /* pushad */
    stub[n++] = 0xFF; stub[n++] = 0x74; stub[n++] = 0x24; stub[n++] = 0x24;   /* push return address */
    for (int k = 0; k < 3; ++k) { stub[n++] = 0xFF; stub[n++] = 0x74; stub[n++] = 0x24; stub[n++] = 0x34; }  /* push arg3,arg2,arg1 */
    stub[n++] = 0x51;                         /* push ecx */
    stub[n++] = 0x68; *(DWORD *)(stub + n) = (DWORD)(DWORD_PTR)nm; n += 4;   /* push name */
    stub[n++] = 0xE8; *(DWORD *)(stub + n) = (DWORD)((BYTE *)trace_hit - (stub + n + 4)); n += 4; /* call */
    stub[n++] = 0x83; stub[n++] = 0xC4; stub[n++] = 0x18;                   /* add esp,24 */
    stub[n++] = 0x61;                         /* popad */
    stub[n++] = 0x9D;                         /* popfd */
    memcpy(stub + n, fn, stolen); n += stolen;
    stub[n++] = 0xE9; *(DWORD *)(stub + n) = (DWORD)((fn + stolen) - (stub + n + 4)); n += 4;
    DWORD old;
    VirtualProtect(fn, stolen, PAGE_EXECUTE_READWRITE, &old);
    fn[0] = 0xE9; *(DWORD *)(fn + 1) = (DWORD)(stub - (fn + 5));
    for (int i = 5; i < stolen; ++i) fn[i] = 0x90;
    FlushInstructionCache(GetCurrentProcess(), fn, stolen);
    logf_("trace: %s at %08lx", name, addr);
}

/* [Patch] MaxPlayers=N : raise the dedicated server's race player cap. ServerRaceManager::m_maxPlayers (object+0x5c8) is copied from the
 * race rule-set data (8 in the shipped data) by two functions, each ending in `mov [esi+0x5c8],edx` (89 96 C8 05 00 00). We detour
 * both stores through a stub that loads edx = N first (edx is dead afterwards). Harmless in the client process. */
static void patch_max_players(int n)
{
    static const DWORD sites[2] = { 0x13cc0d4, 0x13cc154 };
    static const BYTE want[6] = { 0x89, 0x96, 0xC8, 0x05, 0x00, 0x00 };
    for (int k = 0; k < 2; ++k) {
        BYTE *fn = (BYTE *)(DWORD_PTR)sites[k];
        if (IsBadReadPtr(fn, 6) || memcmp(fn, want, 6)) { logf_("patch MaxPlayers: unexpected bytes at %08lx", sites[k]); continue; }
        BYTE *stub = (BYTE *)VirtualAlloc(NULL, 32, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE);
        int m = 0;
        stub[m++] = 0xBA; *(DWORD *)(stub + m) = (DWORD)n; m += 4;           /* mov edx, N */
        memcpy(stub + m, want, 6); m += 6;                                     /* mov [esi+0x5c8], edx */
        stub[m++] = 0xE9; *(DWORD *)(stub + m) = (DWORD)((fn + 6) - (stub + m + 4)); m += 4;
        DWORD old; VirtualProtect(fn, 6, PAGE_EXECUTE_READWRITE, &old);
        fn[0] = 0xE9; *(DWORD *)(fn + 1) = (DWORD)(stub - (fn + 5)); fn[5] = 0x90;
        FlushInstructionCache(GetCurrentProcess(), fn, 6);
        logf_("patch MaxPlayers=%d at %08lx", n, sites[k]);
    }
}

static void load_traces(const char *ini)
{
    char key[16], val[160];
    for (int i = 0; i < 64; ++i) {
        snprintf(key, sizeof key, "T%d", i);
        GetPrivateProfileStringA("Trace", key, "", val, sizeof val, ini);
        if (!val[0]) continue;
        char *c1 = strchr(val, ':'); if (!c1) continue; *c1++ = 0;
        char *c2 = strchr(c1, ':');  if (!c2) continue; *c2++ = 0;
        install_trace((DWORD)strtoul(val, NULL, 0), atoi(c1), c2);
    }
}

/* ---- debug: dump the NfsOnline settings the online bootstrap matches against ---- */
typedef void *(__fastcall *get_cfg_t)(void *ecx, void *edx, const char *name);
static DWORD g_cfg_ms = 0;
static const char *safe_str(DWORD p)
{
    static char buf[4][96]; static int k;
    char *o = buf[k++ & 3];
    if (p < 0x10000 || IsBadReadPtr((void *)(DWORD_PTR)p, 4)) { snprintf(o, 96, "<%08lx>", p); return o; }
    int i = 0;
    for (; i < 90 && !IsBadReadPtr((char *)(DWORD_PTR)p + i, 1) && ((char *)(DWORD_PTR)p)[i]; ++i) {
        char c = ((char *)(DWORD_PTR)p)[i]; o[i] = (c >= 32 && c < 127) ? c : '.';
    }
    o[i] = 0; return o;
}

static DWORD WINAPI dump_cfg_thread(LPVOID unused)
{
    Sleep(g_cfg_ms);
    DWORD *pSettings = (DWORD *)0x0288041C;
    DWORD settings = IsBadReadPtr(pSettings, 4) ? 0 : *pSettings;
    logf_("cfg: DAT_0288041c = %08lx", settings);
    if (settings && !IsBadReadPtr((void *)(DWORD_PTR)settings, 0xA0)) {
        BYTE *b = (BYTE *)(DWORD_PTR)settings;
        logf_("cfg: settings+0x54=%s  +0x70=%s  +0x84=%u  +0x90=%u", safe_str(*(DWORD *)(b + 0x54)),
              safe_str(*(DWORD *)(b + 0x70)), b[0x84], b[0x90]);
        for (int o = 0; o < 0xA0; o += 16)
            logf_("cfg: +%02x: %08lx %08lx %08lx %08lx", o, *(DWORD *)(b + o), *(DWORD *)(b + o + 4),
                  *(DWORD *)(b + o + 8), *(DWORD *)(b + o + 12));
    }
    {
        static const DWORD gl[] = {0x02881110, 0x02881218, 0x02846C74, 0x0288041C};
        for (int k = 0; k < 4; ++k) {
            DWORD v = IsBadReadPtr((void *)(DWORD_PTR)gl[k], 4) ? 0xDEAD : *(DWORD *)(DWORD_PTR)gl[k];
            logf_("glob: [%08lx] = %08lx", gl[k], v);
            if (k < 2 && v > 0x10000 && !IsBadReadPtr((void *)(DWORD_PTR)v, 0x80))
                for (int o = 0; o < 0x80; o += 16)
                    logf_("glob:   +%02x: %08lx %08lx %08lx %08lx", o, *(DWORD *)(DWORD_PTR)(v + o), *(DWORD *)(DWORD_PTR)(v + o + 4),
                          *(DWORD *)(DWORD_PTR)(v + o + 8), *(DWORD *)(DWORD_PTR)(v + o + 12));
        }
    }
    DWORD *pMgr = (DWORD *)0x02846C74;
    if (!IsBadReadPtr(pMgr, 4) && *pMgr) {
        void *obj = ((get_cfg_t)0x004E72C0)((void *)(DWORD_PTR)*pMgr, NULL, (const char *)0x02474860);
        logf_("cfg: NfsOnline object = %p", obj);
        if (obj && !IsBadReadPtr((BYTE *)obj + 0x48, 4)) {
            BYTE *blk = (BYTE *)(DWORD_PTR)*(DWORD *)((BYTE *)obj + 0x48);
            logf_("cfg: block = %p", blk);
            if (blk && !IsBadReadPtr(blk + 0x10, 4)) {
                BYTE *arr = (BYTE *)(DWORD_PTR)*(DWORD *)(blk + 0x10);
                DWORD n = (arr && !IsBadReadPtr(arr - 4, 4)) ? *(DWORD *)(arr - 4) : 0;
                logf_("cfg: entries = %lu (array %p)", n, arr);
                for (DWORD i = 0; i < n && i < 16; ++i) {
                    BYTE *e = arr + i * 0x24;
                    if (IsBadReadPtr(e, 0x24)) break;
                    logf_("cfg: entry %lu: plat=%lu env=%lu flag1c=%u flag1d=%u | %08lx %08lx %08lx %08lx %08lx %08lx %08lx %08lx %08lx",
                          i, *(DWORD *)e, *(DWORD *)(e + 4), e[0x1c], e[0x1d],
                          *(DWORD *)e, *(DWORD *)(e + 4), *(DWORD *)(e + 8), *(DWORD *)(e + 12), *(DWORD *)(e + 16),
                          *(DWORD *)(e + 20), *(DWORD *)(e + 24), *(DWORD *)(e + 28), *(DWORD *)(e + 32));
                    for (int f = 8; f <= 0x18; f += 4) {
                        DWORD v = *(DWORD *)(e + f);
                        if (v > 0x10000) logf_("cfg:    +%02x -> \"%s\"", f, safe_str(v));
                    }
                }
            }
        }
    }
    return 0;
}

/* ---- debug: sample every thread's stack ----------------------------------- */
static DWORD g_dump_ms = 0;
static DWORD WINAPI dump_threads(LPVOID unused)
{
    for (int round = 0; round < 3; ++round) {
        Sleep(g_dump_ms);
        HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0);
        if (snap == INVALID_HANDLE_VALUE) return 0;
        THREADENTRY32 te; te.dwSize = sizeof te;
        logf_("---- thread dump round %d ----", round);
        if (Thread32First(snap, &te)) do {
            if (te.th32OwnerProcessID != GetCurrentProcessId() || te.th32ThreadID == GetCurrentThreadId()) continue;
            HANDLE th = OpenThread(THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT | THREAD_QUERY_INFORMATION, FALSE, te.th32ThreadID);
            if (!th) continue;
            if (SuspendThread(th) != (DWORD)-1) {
                CONTEXT c; c.ContextFlags = CONTEXT_FULL;
                if (GetThreadContext(th, &c)) {
                    char line[1024]; int n = snprintf(line, sizeof line, "tid %lu eip=%08lx esp=%08lx ret:", te.th32ThreadID, c.Eip, c.Esp);
                    DWORD *sp = (DWORD *)c.Esp; int hits = 0;
                    for (int i = 0; i < 600 && hits < 14 && !IsBadReadPtr(sp + i, 4); ++i)
                        if (sp[i] >= 0x401000 && sp[i] < 0x2202000 && !IsBadReadPtr((void *)(sp[i] - 5), 1) && *(BYTE *)(sp[i] - 5) == 0xE8) {
                            n += snprintf(line + n, sizeof line - n, " %08lx", sp[i]); ++hits;
                        }
                    logf_("%s", line);
                }
                ResumeThread(th);
            }
            CloseHandle(th);
        } while (Thread32Next(snap, &te));
        CloseHandle(snap);
    }
    return 0;
}

/* ---- restore the Ebisu (Origin) startup stub --------------------------------
 * EbisuStartup (0x018AEF80) calls a stub at 0x022013AF to bring up the SDK.
 * Cracked builds replace that stub with a lone `ret`, so the game never starts
 * Origin nor runs the post-start setup. Point it at the real routine
 * (0x018B1CE0, same signature) so the game's own bootstrap works unchanged.
 */
static void patch_ebisu_stub(void)
{
    BYTE *stub = (BYTE *)0x022013AF;
    BYTE *real = (BYTE *)0x018B1CE0;
    static const BYTE sig[] = { 0x55, 0x8B, 0xEC, 0x6A, 0xFF, 0x68, 0xBB, 0xE8, 0x1A, 0x02 };
    DWORD old;
    if (IsBadReadPtr(stub, 8) || IsBadReadPtr(real, sizeof sig) || memcmp(real, sig, sizeof sig)) {
        logf_("ebisu: stub/real function not readable or mismatched, not patching"); return;
    }
    if (stub[0] == 0xE9) { logf_("ebisu: stub already patched"); return; }
    if (stub[0] != 0xC3 && stub[0] != 0x00) { logf_("ebisu: unexpected stub byte %02x, not patching", stub[0]); return; }
    VirtualProtect(stub, 8, PAGE_EXECUTE_READWRITE, &old);
    stub[0] = 0xE9; *(DWORD *)(stub + 1) = (DWORD)(real - (stub + 5));
    VirtualProtect(stub, 8, old, &old);
    FlushInstructionCache(GetCurrentProcess(), stub, 8);
    logf_("ebisu: startup stub at %p now jumps to %p", stub, real);
}

/* ---- Origin (LSX) kick-start --------------------------------------------
 * The game's Origin layer (EbisuSDK) is never started on its own in this
 * build, so it never connects to 127.0.0.1:3216 and the game believes you
 * are not signed in. Once the engine is up we call its startup routine
 * ourselves. Verified against the exe's bytes first; skipped otherwise.
 *   VA 0x018B1CE0: cdecl  void *start(int flags, WORD port, DWORD *p, DWORD *out)
 */
typedef int (__cdecl *origin_start_t)(int, unsigned short, DWORD *, DWORD *);
static DWORD g_origin_delay = 20000;

static HANDLE g_origin_h;

static DWORD WINAPI watchdog_thread(LPVOID unused)
{
    Sleep(g_origin_delay + 8000);
    if (!g_origin_h) return 0;
    SuspendThread(g_origin_h);
    CONTEXT c; c.ContextFlags = CONTEXT_FULL;
    if (GetThreadContext(g_origin_h, &c)) {
        logf_("origin watchdog: eip=%08lx esp=%08lx", c.Eip, c.Esp);
        DWORD *sp = (DWORD *)c.Esp;
        for (int i = 0; i < 400 && !IsBadReadPtr(sp + i, 4); ++i)
            if (sp[i] >= 0x401000 && sp[i] < 0x2714000)
                logf_("  stack[%d] = %08lx", i, sp[i]);
    }
    ResumeThread(g_origin_h);
    return 0;
}

static DWORD WINAPI origin_thread(LPVOID unused)
{
    static const BYTE sig[] = { 0x55, 0x8B, 0xEC, 0x6A, 0xFF, 0x68, 0xBB, 0xE8, 0x1A, 0x02 };
    Sleep(g_origin_delay);
    if ((DWORD_PTR)GetModuleHandleA(NULL) != 0x400000) { logf_("origin: unexpected image base"); return 0; }
    const BYTE *fn = (const BYTE *)0x018B1CE0;
    if (IsBadReadPtr(fn, sizeof sig) || memcmp(fn, sig, sizeof sig)) { logf_("origin: signature mismatch, not calling"); return 0; }
    /* ContentId, Title, MultiplayerId, Language (the LSX ChallengeResponse fields) */
    static const char *info[8] = { "71067", "Need for Speed(TM) The Run",
                                   "Need for Speed(TM) The Run", "en_US", "", "", "", "" };
    DWORD out = 0;
    DWORD *zero = (DWORD *)info;
    logf_("origin: calling startup at %p", fn);
    {
        int r = ((origin_start_t)fn)(0, 0, zero, &out);
        logf_("origin: startup returned %d (out=%lu)", r, out);
    }
    return 0;
}

#include "lsx_embedded.h"

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID res)
{
    if (reason != DLL_PROCESS_ATTACH) return TRUE;
    DisableThreadLibraryCalls(inst);

    char path[MAX_PATH];
    GetModuleFileNameA(inst, path, MAX_PATH);
    char *dot = strrchr(path, '.');
    if (dot) strcpy(dot, ".log");
    g_log = fopen(path, "w");
    if (dot) strcpy(dot, ".ini");
    strcpy(g_ini, path);
    GetPrivateProfileStringA("Server", "Host", "127.0.0.1", g_host, sizeof g_host, path);
    logf_("NFSTR_Online loaded, server host = %s", g_host);

    HMODULE ws = LoadLibraryA("ws2_32.dll");
    real_gethostbyname = (gethostbyname_t)GetProcAddress(ws, "gethostbyname");
    real_getaddrinfo = (getaddrinfo_t)GetProcAddress(ws, "getaddrinfo");

    InitializeCriticalSection(&g_cs);
    if (real_gethostbyname) hook_install(&h_ghbn, real_gethostbyname, my_gethostbyname);
    if (real_getaddrinfo) hook_install(&h_gai, real_getaddrinfo, my_getaddrinfo);
    logf_("hooks installed (gethostbyname=%p getaddrinfo=%p)", real_gethostbyname, real_getaddrinfo);
    if (GetPrivateProfileIntA("Server", "SkipCertCheck", 1, path)) patch_ssl_check();
    load_traces(path);
    if (GetPrivateProfileIntA("Origin", "Embedded", 1, path)) lsx_embedded_start(path);   /* built-in Origin stand-in + stub; set Embedded=0 to use server/lsx.py */
    { int mp = GetPrivateProfileIntA("Patch", "MaxPlayers", 0, path); if (mp >= 2 && mp <= 64) patch_max_players(mp); }
    g_cfg_ms = GetPrivateProfileIntA("Debug", "DumpCfgMs", 0, path);
    if (g_cfg_ms) CreateThread(NULL, 0, dump_cfg_thread, NULL, 0, NULL);
    g_dump_ms = GetPrivateProfileIntA("Debug", "DumpThreadsMs", 0, path);
    if (g_dump_ms) CreateThread(NULL, 0, dump_threads, NULL, 0, NULL);
    if (GetPrivateProfileIntA("Origin", "PatchStub", 1, path)) patch_ebisu_stub();
    if (GetPrivateProfileIntA("Origin", "Enable", 0, path)) {
        g_origin_delay = GetPrivateProfileIntA("Origin", "StartDelayMs", 20000, path);
        g_origin_h = CreateThread(NULL, 0, origin_thread, NULL, 0, NULL);
        CreateThread(NULL, 0, watchdog_thread, NULL, 0, NULL);
    }
    return TRUE;
}
