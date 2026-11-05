#include "gateway.h"
#include <stdio.h>
#include <stdarg.h>
#include <time.h>

static CRITICAL_SECTION g_log_lock;
static int g_log_lock_ready = 0;
static FILE *g_log_file = NULL;

void gateway_log_init(const char *path)
{
    InitializeCriticalSection(&g_log_lock);
    g_log_lock_ready = 1;
    if (path && *path) {
        fopen_s(&g_log_file, path, "a");
    }
}

void gateway_log_shutdown(void)
{
    if (g_log_lock_ready) EnterCriticalSection(&g_log_lock);
    if (g_log_file) {
        fclose(g_log_file);
        g_log_file = NULL;
    }
    if (g_log_lock_ready) {
        LeaveCriticalSection(&g_log_lock);
        DeleteCriticalSection(&g_log_lock);
        g_log_lock_ready = 0;
    }
}

void gateway_log(const char *fmt, ...)
{
    SYSTEMTIME st;
    char message[2048];
    va_list ap;

    GetLocalTime(&st);
    va_start(ap, fmt);
    vsnprintf_s(message, sizeof(message), _TRUNCATE, fmt, ap);
    va_end(ap);

    if (g_log_lock_ready) EnterCriticalSection(&g_log_lock);
    fprintf(stdout, "%04u/%02u/%02u %02u:%02u:%02u.%03u %s\n",
            st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond,
            st.wMilliseconds, message);
    fflush(stdout);
    if (g_log_file) {
        fprintf(g_log_file, "%04u/%02u/%02u %02u:%02u:%02u.%03u %s\n",
                st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond,
                st.wMilliseconds, message);
        fflush(g_log_file);
    }
    if (g_log_lock_ready) LeaveCriticalSection(&g_log_lock);
}
