/* dinput8.dll proxy: forwards DirectInput8Create to the real system DLL and
 * loads every Scripts\*.asi on startup (a minimal ASI loader). */
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <unknwn.h>
#include <string.h>
#include <stdio.h>

typedef HRESULT (WINAPI *create_t)(HINSTANCE, DWORD, REFIID, LPVOID *, LPUNKNOWN);
static create_t real_create;

__declspec(dllexport) HRESULT WINAPI DirectInput8Create(HINSTANCE h, DWORD v, REFIID r, LPVOID *out, LPUNKNOWN u)
{
    if (!real_create) {
        char p[MAX_PATH];
        /* chain: if the player renamed another dinput8 wrapper (ReShade,
         * ASI loader, ...) to dinput8_chain.dll, forward to it first */
        HMODULE m = LoadLibraryA("dinput8_chain.dll");
        if (!m) {
            GetSystemDirectoryA(p, MAX_PATH);
            lstrcatA(p, "\\dinput8.dll");
            m = LoadLibraryA(p);
        }
        if (m) real_create = (create_t)GetProcAddress(m, "DirectInput8Create");
    }
    return real_create ? real_create(h, v, r, out, u) : E_FAIL;
}

static char g_dir[MAX_PATH];
static char g_logp_veh[MAX_PATH];
static LONG g_veh_logged;

static LONG CALLBACK veh(EXCEPTION_POINTERS *ep)
{
    if (ep->ExceptionRecord->ExceptionCode != 0xC0000005 || InterlockedExchange(&g_veh_logged, 1)) return EXCEPTION_CONTINUE_SEARCH;
    char buf[2048]; int n = 0;
    CONTEXT *c = ep->ContextRecord;
    n += wsprintfA(buf + n, "VEH AV eip=%08lx esp=%08lx eax=%08lx ebx=%08lx ecx=%08lx\n", c->Eip, c->Esp, c->Eax, c->Ebx, c->Ecx);
    DWORD *sp = (DWORD *)c->Esp;
    MEMORY_BASIC_INFORMATION mbi;
    for (int i = 0; i < 400 && n < 1800; ++i) {
        if (IsBadReadPtr(sp + i, 4)) break;
        DWORD v = sp[i];
        if (VirtualQuery((void *)(DWORD_PTR)v, &mbi, sizeof mbi) && mbi.Type == MEM_IMAGE && v > 0x10000)
            n += wsprintfA(buf + n, "  stack[%d]=%08lx base=%p\n", i, v, mbi.AllocationBase);
    }
    FILE *lg = fopen(g_logp_veh, "a");
    if (lg) { fwrite(buf, 1, n, lg); fclose(lg); }
    return EXCEPTION_CONTINUE_SEARCH;
}
static char g_logp[MAX_PATH];

static void note(const char *fmt, const char *a, void *m, DWORD err)
{
    FILE *lg = fopen(g_logp, "a");
    if (!lg) return;
    fprintf(lg, fmt, a, m, err);
    fclose(lg);
}

/* load every Scripts\*.asi that is not loaded yet; returns how many still failed */
static int load_all(int quiet)
{
    char pat[MAX_PATH];
    WIN32_FIND_DATAA fd;
    int failed = 0;
    lstrcpyA(pat, g_dir); lstrcatA(pat, "\\Scripts\\*.asi");
    HANDLE f = FindFirstFileA(pat, &fd);
    if (f == INVALID_HANDLE_VALUE) return 0;
    do {
        char full[MAX_PATH];
        lstrcpyA(full, g_dir); lstrcatA(full, "\\Scripts\\"); lstrcatA(full, fd.cFileName);
        if (GetModuleHandleA(full)) continue;               /* already loaded */
        HMODULE m = LoadLibraryA(full);
        if (!m) ++failed;
        note("LoadLibrary %s -> %p (err %lu)\n", fd.cFileName, m, m ? 0 : GetLastError());
    } while (FindNextFileA(f, &fd));
    FindClose(f);
    return failed;
}

static DWORD WINAPI retry_thread(LPVOID unused)
{
    for (int i = 0; i < 20; ++i) {
        Sleep(750);
        note("retry %s %p %lu\n", "pass", NULL, i + 1);
        if (load_all(1) == 0) { note("retry %s %p %lu\n", "all loaded", NULL, i + 1); break; }
    }
    return 0;
}

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID res)
{
    if (reason == DLL_PROCESS_ATTACH) {
        char exe[MAX_PATH];
        DisableThreadLibraryCalls(inst);
        GetModuleFileNameA(NULL, g_dir, MAX_PATH);
        lstrcpyA(exe, g_dir);
        char *s = strrchr(g_dir, '\\');
        if (s) *s = 0;
        char *b = strrchr(exe, '\\'); b = b ? b + 1 : exe;
        lstrcpyA(g_logp, g_dir); lstrcatA(g_logp, "\\Scripts\\loader_"); lstrcatA(g_logp, b); lstrcatA(g_logp, ".log");
        { FILE *lg = fopen(g_logp, "w"); if (lg) { fprintf(lg, "loader attached, pid %lu\n", GetCurrentProcessId()); fclose(lg); } }
        lstrcpyA(g_logp_veh, g_logp);
        AddVectoredExceptionHandler(1, veh);
        if (load_all(0) > 0)
            CreateThread(NULL, 0, retry_thread, NULL, 0, NULL);
    }
    return TRUE;
}
