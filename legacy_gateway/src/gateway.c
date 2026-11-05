#include "gateway.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <errno.h>

extern void gateway_log_init(const char *path);
extern void gateway_log_shutdown(void);

static SSL_CTX *g_ssl_ctx = NULL;
static CRITICAL_SECTION *g_ssl_locks = NULL;
static int g_ssl_lock_count = 0;
static volatile LONG g_running = 1;

static void openssl_locking_callback(int mode, int n, const char *file, int line)
{
    (void)file;
    (void)line;
    if (n < 0 || n >= g_ssl_lock_count || !g_ssl_locks) return;
    if (mode & CRYPTO_LOCK) EnterCriticalSection(&g_ssl_locks[n]);
    else LeaveCriticalSection(&g_ssl_locks[n]);
}

static unsigned long openssl_thread_id(void)
{
    return (unsigned long)GetCurrentThreadId();
}

static int openssl_threading_init(void)
{
    int i;
    g_ssl_lock_count = CRYPTO_num_locks();
    if (g_ssl_lock_count <= 0) return 1;
    g_ssl_locks = (CRITICAL_SECTION *)calloc((size_t)g_ssl_lock_count, sizeof(CRITICAL_SECTION));
    if (!g_ssl_locks) return 0;
    for (i = 0; i < g_ssl_lock_count; ++i) InitializeCriticalSection(&g_ssl_locks[i]);
    CRYPTO_set_locking_callback(openssl_locking_callback);
    CRYPTO_set_id_callback(openssl_thread_id);
    return 1;
}

static void openssl_threading_shutdown(void)
{
    int i;
    CRYPTO_set_locking_callback(NULL);
    CRYPTO_set_id_callback(NULL);
    if (g_ssl_locks) {
        for (i = 0; i < g_ssl_lock_count; ++i) DeleteCriticalSection(&g_ssl_locks[i]);
        free(g_ssl_locks);
        g_ssl_locks = NULL;
    }
    g_ssl_lock_count = 0;
}

static void log_ssl_error_stack(const char *prefix)
{
    unsigned long err;
    char buf[256];
    while ((err = ERR_get_error()) != 0) {
        ERR_error_string_n(err, buf, sizeof(buf));
        gateway_log("%s%s", prefix ? prefix : "[SSL] ", buf);
    }
}

static SOCKET connect_tcp(const char *host, unsigned short port)
{
    struct addrinfo hints;
    struct addrinfo *result = NULL, *ptr;
    char portbuf[16];
    SOCKET s = INVALID_SOCKET;
    int rc;

    ZeroMemory(&hints, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    _snprintf_s(portbuf, sizeof(portbuf), _TRUNCATE, "%u", (unsigned)port);

    rc = getaddrinfo(host, portbuf, &hints, &result);
    if (rc != 0) return INVALID_SOCKET;

    for (ptr = result; ptr; ptr = ptr->ai_next) {
        s = socket(ptr->ai_family, ptr->ai_socktype, ptr->ai_protocol);
        if (s == INVALID_SOCKET) continue;
        if (connect(s, ptr->ai_addr, (int)ptr->ai_addrlen) == 0) break;
        closesocket(s);
        s = INVALID_SOCKET;
    }
    freeaddrinfo(result);
    return s;
}

static SOCKET create_listener(const char *host, unsigned short port)
{
    struct addrinfo hints;
    struct addrinfo *result = NULL, *ptr;
    char portbuf[16];
    SOCKET s = INVALID_SOCKET;
    BOOL reuse = TRUE;
    int rc;

    ZeroMemory(&hints, sizeof(hints));
    /* The public endpoints in this project are IPv4.  Do NOT turn
       0.0.0.0 into NULL with AF_UNSPEC: on Windows that can bind an
       IPv6 wildcard socket first while the status log still says
       0.0.0.0, leaving IPv4 clients unable to reach the gateway. */
    if (host && strcmp(host, "0.0.0.0") == 0) {
        hints.ai_family = AF_INET;
    } else {
        hints.ai_family = AF_UNSPEC;
    }
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    hints.ai_flags = AI_PASSIVE;
    _snprintf_s(portbuf, sizeof(portbuf), _TRUNCATE, "%u", (unsigned)port);

    rc = getaddrinfo((host && *host) ? host : NULL,
                     portbuf, &hints, &result);
    if (rc != 0) return INVALID_SOCKET;

    for (ptr = result; ptr; ptr = ptr->ai_next) {
        s = socket(ptr->ai_family, ptr->ai_socktype, ptr->ai_protocol);
        if (s == INVALID_SOCKET) continue;
        setsockopt(s, SOL_SOCKET, SO_REUSEADDR, (const char *)&reuse, sizeof(reuse));
        if (bind(s, ptr->ai_addr, (int)ptr->ai_addrlen) == 0 && listen(s, SOMAXCONN) == 0) break;
        closesocket(s);
        s = INVALID_SOCKET;
    }
    freeaddrinfo(result);
    return s;
}

typedef struct client_ctx_s {
    SOCKET client;
    gateway_route_t route;
    char peer[128];
} client_ctx_t;


static const char *content_type_name(int content_type)
{
    switch (content_type) {
    case 20: return "change_cipher_spec";
    case 21: return "alert";
    case 22: return "handshake";
    case 23: return "application_data";
    default: return "other";
    }
}

static const char *handshake_type_name(unsigned char t)
{
    switch (t) {
    case 0: return "hello_request";
    case 1: return "client_hello";
    case 2: return "server_hello";
    case 11: return "certificate";
    case 12: return "server_key_exchange";
    case 13: return "certificate_request";
    case 14: return "server_hello_done";
    case 15: return "certificate_verify";
    case 16: return "client_key_exchange";
    case 20: return "finished";
    default: return "unknown";
    }
}

static void format_hex(const unsigned char *buf, int len, char *out, size_t out_size)
{
    int i, limit = len;
    size_t used = 0;
    if (limit > 128) limit = 128;
    if (out_size == 0) return;
    out[0] = 0;
    for (i = 0; i < limit; ++i) {
        int wrote = _snprintf_s(out + used, out_size - used, _TRUNCATE,
                                "%s%02X", i ? " " : "", (unsigned)buf[i]);
        if (wrote < 0) break;
        used += (size_t)wrote;
        if (used + 4 >= out_size) break;
    }
    if (len > limit && used + 5 < out_size)
        _snprintf_s(out + used, out_size - used, _TRUNCATE, " ...");
}

static void log_client_hello_peek(const gateway_route_t *route, SOCKET s, const char *peer)
{
    unsigned char b[2048];
    char hex[1024];
    int n = recv(s, (char *)b, (int)sizeof(b), MSG_PEEK);
    if (n == SOCKET_ERROR) {
        gateway_log("[%s] %s pre-SSL MSG_PEEK failed WSA=%d", route->name, peer, WSAGetLastError());
        return;
    }
    if (n == 0) {
        gateway_log("[%s] %s peer closed before ClientHello", route->name, peer);
        return;
    }

    format_hex(b, n, hex, sizeof(hex));
    gateway_log("[%s] %s pre-SSL peek bytes=%d: %s", route->name, peer, n, hex);

    /* Native SSLv3/TLS record framing. */
    if (n >= 5 && b[0] >= 20 && b[0] <= 23 && b[1] == 3) {
        unsigned rec_len = ((unsigned)b[3] << 8) | b[4];
        gateway_log("[%s] %s record content=%u(%s) version=0x%02X%02X len=%u",
                    route->name, peer, (unsigned)b[0], content_type_name(b[0]),
                    (unsigned)b[1], (unsigned)b[2], rec_len);
        if (b[0] == 22 && n >= 11 && b[5] == 1) {
            unsigned hs_len = ((unsigned)b[6] << 16) | ((unsigned)b[7] << 8) | b[8];
            gateway_log("[%s] %s ClientHello handshake_len=%u client_version=0x%02X%02X",
                        route->name, peer, hs_len, (unsigned)b[9], (unsigned)b[10]);
            if (n >= 46) {
                int sid_len = b[43];
                int pos = 44 + sid_len;
                if (pos + 2 <= n) {
                    int cs_len = ((int)b[pos] << 8) | b[pos + 1];
                    int end, i;
                    char suites[1024];
                    size_t used = 0;
                    pos += 2;
                    end = pos + cs_len;
                    if (end > n) end = n;
                    suites[0] = 0;
                    for (i = pos; i + 1 < end && used + 10 < sizeof(suites); i += 2) {
                        unsigned id = ((unsigned)b[i] << 8) | b[i + 1];
                        int wrote = _snprintf_s(suites + used, sizeof(suites) - used, _TRUNCATE,
                                                "%s0x%04X", used ? "," : "", id);
                        if (wrote < 0) break;
                        used += (size_t)wrote;
                    }
                    gateway_log("[%s] %s offered cipher suites (%d bytes): %s",
                                route->name, peer, cs_len, suites[0] ? suites : "<none/partial>");
                }
            }
        }
        return;
    }

    /* SSLv2-compatible ClientHello framing (sometimes used by old SSLv3 clients). */
    if (n >= 11 && (b[0] & 0x80) != 0) {
        unsigned rec_len = ((unsigned)(b[0] & 0x7F) << 8) | b[1];
        unsigned msg_type = b[2];
        unsigned ver = ((unsigned)b[3] << 8) | b[4];
        unsigned cs_len = ((unsigned)b[5] << 8) | b[6];
        gateway_log("[%s] %s SSLv2-compatible hello record_len=%u msg=%u version=0x%04X cipher_bytes=%u",
                    route->name, peer, rec_len, msg_type, ver, cs_len);
        return;
    }

    gateway_log("[%s] %s first bytes are not recognized as native SSLv3/TLS or SSLv2-compatible ClientHello",
                route->name, peer);
}

static void ssl_info_callback(const SSL *ssl, int where, int ret)
{
    if (where & SSL_CB_LOOP) {
        gateway_log("[SSL-STATE] %s", SSL_state_string_long(ssl));
    } else if (where & SSL_CB_ALERT) {
        gateway_log("[SSL-ALERT] %s %s:%s",
                    (where & SSL_CB_READ) ? "read" : "write",
                    SSL_alert_type_string_long(ret), SSL_alert_desc_string_long(ret));
    } else if (where & SSL_CB_EXIT) {
        if (ret == 0)
            gateway_log("[SSL-STATE] failed in %s", SSL_state_string_long(ssl));
        else if (ret < 0)
            gateway_log("[SSL-STATE] error in %s", SSL_state_string_long(ssl));
    }
}

static void ssl_msg_callback(int write_p, int version, int content_type,
                             const void *buf, size_t len, SSL *ssl, void *arg)
{
    const unsigned char *p = (const unsigned char *)buf;
    const client_ctx_t *ctx = (const client_ctx_t *)arg;
    const char *route = ctx ? ctx->route.name : "SSL";
    const char *peer = ctx ? ctx->peer : "unknown";
    (void)ssl;

    if (content_type == 22 && len > 0) {
        gateway_log("[%s] %s SSL-MSG %s version=0x%04X handshake=%u(%s) len=%u",
                    route, peer, write_p ? "TX" : "RX", version,
                    (unsigned)p[0], handshake_type_name(p[0]), (unsigned)len);
    } else if (content_type == 21 && len >= 2) {
        gateway_log("[%s] %s SSL-MSG %s version=0x%04X alert level=%u desc=%u len=%u",
                    route, peer, write_p ? "TX" : "RX", version,
                    (unsigned)p[0], (unsigned)p[1], (unsigned)len);
    } else {
        gateway_log("[%s] %s SSL-MSG %s version=0x%04X content=%d(%s) len=%u",
                    route, peer, write_p ? "TX" : "RX", version,
                    content_type, content_type_name(content_type), (unsigned)len);
    }
}

static void format_peer(SOCKET s, char *out, size_t out_size)
{
    struct sockaddr_storage addr;
    int addrlen = sizeof(addr);
    char host[NI_MAXHOST];
    char serv[NI_MAXSERV];
    if (getpeername(s, (struct sockaddr *)&addr, &addrlen) == 0 &&
        getnameinfo((struct sockaddr *)&addr, addrlen, host, sizeof(host), serv, sizeof(serv),
                    NI_NUMERICHOST | NI_NUMERICSERV) == 0) {
        _snprintf_s(out, out_size, _TRUNCATE, "%s:%s", host, serv);
    } else {
        strncpy_s(out, out_size, "unknown", _TRUNCATE);
    }
}

static int relay_loop(const gateway_route_t *route, SSL *ssl, SOCKET client, SOCKET backend, const char *peer)
{
    unsigned char buf[16384];
    int client_fd = (int)client;
    int backend_fd = (int)backend;
    int maxfd = client_fd > backend_fd ? client_fd : backend_fd;

    for (;;) {
        fd_set reads;
        struct timeval tv;
        int rc;

        FD_ZERO(&reads);
        FD_SET(client, &reads);
        FD_SET(backend, &reads);
        tv.tv_sec = 30;
        tv.tv_usec = 0;

        /* Winsock ignores nfds, but keep a sensible value for clarity. */
        rc = select(maxfd + 1, &reads, NULL, NULL, &tv);
        if (rc == SOCKET_ERROR) return 0;
        if (rc == 0) continue;

        if (FD_ISSET(client, &reads) || SSL_pending(ssl) > 0) {
            int n = SSL_read(ssl, buf, (int)sizeof(buf));
            if (n <= 0) {
                int e = SSL_get_error(ssl, n);
                if (e != SSL_ERROR_ZERO_RETURN && e != SSL_ERROR_SYSCALL)
                    gateway_log("[%s] %s SSL_read ended: ssl_error=%d", route->name, peer, e);
                return 1;
            }
            {
                int sent = 0;
                while (sent < n) {
                    int w = send(backend, (const char *)buf + sent, n - sent, 0);
                    if (w <= 0) return 1;
                    sent += w;
                }
            }
        }

        if (FD_ISSET(backend, &reads)) {
            int n = recv(backend, (char *)buf, (int)sizeof(buf), 0);
            if (n <= 0) return 1;
            {
                int sent = 0;
                while (sent < n) {
                    int w = SSL_write(ssl, buf + sent, n - sent);
                    if (w <= 0) {
                        int e = SSL_get_error(ssl, w);
                        gateway_log("[%s] %s SSL_write ended: ssl_error=%d", route->name, peer, e);
                        return 1;
                    }
                    sent += w;
                }
            }
        }
    }
}

static DWORD WINAPI client_thread(LPVOID param)
{
    client_ctx_t *ctx = (client_ctx_t *)param;
    SSL *ssl = NULL;
    SOCKET backend = INVALID_SOCKET;
    const SSL_CIPHER *cipher;
    int rc;
    int handshake_ok = 0;

    gateway_log("[%s] connect %s", ctx->route.name, ctx->peer);
    log_client_hello_peek(&ctx->route, ctx->client, ctx->peer);

    ssl = SSL_new(g_ssl_ctx);
    if (!ssl) {
        gateway_log("[%s] %s SSL_new failed", ctx->route.name, ctx->peer);
        log_ssl_error_stack("[SSL] ");
        goto done;
    }
    SSL_set_fd(ssl, (int)ctx->client);
    SSL_set_msg_callback(ssl, ssl_msg_callback);
    SSL_set_msg_callback_arg(ssl, ctx);

    ERR_clear_error();
    WSASetLastError(0);
    errno = 0;
    rc = SSL_accept(ssl);
    if (rc != 1) {
        int e = SSL_get_error(ssl, rc);
        int wsa = WSAGetLastError();
        int crt_errno = errno;
        unsigned long queued = ERR_peek_error();
        gateway_log("[%s] %s SSLv3 handshake failed: rc=%d ssl_error=%d WSA=%d errno=%d state=%s err_queue=%s",
                    ctx->route.name, ctx->peer, rc, e, wsa, crt_errno,
                    SSL_state_string_long(ssl), queued ? "nonempty" : "empty");
        log_ssl_error_stack("[SSL] ");
        goto done;
    }

    handshake_ok = 1;
    cipher = SSL_get_current_cipher(ssl);
    gateway_log("[%s] %s SSL OK version=%s cipher=%s",
                ctx->route.name, ctx->peer,
                SSL_get_version(ssl), cipher ? SSL_CIPHER_get_name(cipher) : "unknown");

    backend = connect_tcp(ctx->route.backend_host, ctx->route.backend_port);
    if (backend == INVALID_SOCKET) {
        gateway_log("[%s] %s backend unavailable %s:%u",
                    ctx->route.name, ctx->peer, ctx->route.backend_host,
                    (unsigned)ctx->route.backend_port);
        goto done;
    }

    gateway_log("[%s] %s -> backend %s:%u",
                ctx->route.name, ctx->peer, ctx->route.backend_host,
                (unsigned)ctx->route.backend_port);
    relay_loop(&ctx->route, ssl, ctx->client, backend, ctx->peer);

done:
    if (backend != INVALID_SOCKET) {
        shutdown(backend, SD_BOTH);
        closesocket(backend);
    }
    if (ssl) {
        if (handshake_ok) SSL_shutdown(ssl);
        SSL_free(ssl);
    }
    shutdown(ctx->client, SD_BOTH);
    closesocket(ctx->client);
    gateway_log("[%s] %s closed", ctx->route.name, ctx->peer);
    free(ctx);
    return 0;
}

static DWORD WINAPI listener_thread(LPVOID param)
{
    gateway_route_t *route = (gateway_route_t *)param;
    route->listener = create_listener(route->listen_host, route->listen_port);
    if (route->listener == INVALID_SOCKET) {
        gateway_log("[%s] FAILED to listen on %s:%u (WSA=%d)", route->name,
                    route->listen_host, (unsigned)route->listen_port, WSAGetLastError());
        return 1;
    }

    gateway_log("[%s] listening SSLv3 on %s:%u -> %s:%u", route->name,
                route->listen_host, (unsigned)route->listen_port,
                route->backend_host, (unsigned)route->backend_port);

    while (InterlockedCompareExchange(&g_running, 1, 1) != 0) {
        SOCKET client = accept(route->listener, NULL, NULL);
        client_ctx_t *ctx;
        HANDLE h;
        if (client == INVALID_SOCKET) {
            if (InterlockedCompareExchange(&g_running, 1, 1) == 0) break;
            Sleep(10);
            continue;
        }
        ctx = (client_ctx_t *)calloc(1, sizeof(*ctx));
        if (!ctx) {
            closesocket(client);
            continue;
        }
        ctx->client = client;
        ctx->route = *route;
        format_peer(client, ctx->peer, sizeof(ctx->peer));
        /* Log immediately after accept, before SSL/OpenSSL is touched. */
        gateway_log("[%s] TCP ACCEPT %s", route->name, ctx->peer);
        h = CreateThread(NULL, 0, client_thread, ctx, 0, NULL);
        if (!h) {
            closesocket(client);
            free(ctx);
        } else {
            CloseHandle(h);
        }
    }
    return 0;
}

static BOOL WINAPI console_handler(DWORD type)
{
    if (type == CTRL_C_EVENT || type == CTRL_BREAK_EVENT || type == CTRL_CLOSE_EVENT ||
        type == CTRL_SHUTDOWN_EVENT) {
        InterlockedExchange(&g_running, 0);
        return TRUE;
    }
    return FALSE;
}

static int init_ssl(const gateway_config_t *cfg)
{
    const SSL_METHOD *method;

    SSL_library_init();
    SSL_load_error_strings();
    OpenSSL_add_all_algorithms();

    if (!openssl_threading_init()) {
        gateway_log("[SSL] failed to initialize OpenSSL thread locks");
        return 0;
    }

    /* Use the version-flexible parser, then restrict negotiation to SSLv3.
       This also accepts legacy SSLv2-compatible ClientHello framing used by
       some old SSLv3 clients while never allowing TLS 1.x to be negotiated. */
    method = SSLv23_server_method();
    if (!method) {
        gateway_log("[SSL] SSLv23_server_method() unavailable");
        log_ssl_error_stack("[SSL] ");
        return 0;
    }

    g_ssl_ctx = SSL_CTX_new(method);
    if (!g_ssl_ctx) {
        gateway_log("[SSL] SSL_CTX_new failed");
        log_ssl_error_stack("[SSL] ");
        return 0;
    }

    {
        long ssl_options = SSL_OP_ALL | SSL_OP_NO_SSLv2 | SSL_OP_NO_TLSv1;
#ifdef SSL_OP_NO_TLSv1_1
        ssl_options |= SSL_OP_NO_TLSv1_1;
#endif
#ifdef SSL_OP_NO_TLSv1_2
        ssl_options |= SSL_OP_NO_TLSv1_2;
#endif
        SSL_CTX_set_options(g_ssl_ctx, ssl_options);
    }
    SSL_CTX_set_session_cache_mode(g_ssl_ctx, SSL_SESS_CACHE_SERVER);
    SSL_CTX_set_info_callback(g_ssl_ctx, ssl_info_callback);

    if (SSL_CTX_set_cipher_list(g_ssl_ctx, cfg->cipher_list) != 1) {
        gateway_log("[SSL] cipher list rejected: %s", cfg->cipher_list);
        log_ssl_error_stack("[SSL] ");
        return 0;
    }

    if (SSL_CTX_use_certificate_file(g_ssl_ctx, cfg->cert_file, SSL_FILETYPE_PEM) != 1) {
        gateway_log("[SSL] failed loading certificate: %s", cfg->cert_file);
        log_ssl_error_stack("[SSL] ");
        return 0;
    }
    if (SSL_CTX_use_PrivateKey_file(g_ssl_ctx, cfg->key_file, SSL_FILETYPE_PEM) != 1) {
        gateway_log("[SSL] failed loading key: %s", cfg->key_file);
        log_ssl_error_stack("[SSL] ");
        return 0;
    }
    if (SSL_CTX_check_private_key(g_ssl_ctx) != 1) {
        gateway_log("[SSL] certificate/private key mismatch");
        log_ssl_error_stack("[SSL] ");
        return 0;
    }

    gateway_log("[SSL] OpenSSL=%s", SSLeay_version(SSLEAY_VERSION));
    gateway_log("[SSL] parser=SSLv23_server_method negotiation=SSLv3-only requested_ciphers=%s", cfg->cipher_list);
    {
        SSL *probe_ssl = SSL_new(g_ssl_ctx);
        STACK_OF(SSL_CIPHER) *ciphers = probe_ssl ? SSL_get_ciphers(probe_ssl) : NULL;
        int i, count = ciphers ? sk_SSL_CIPHER_num(ciphers) : 0;
        gateway_log("[SSL] enabled cipher count=%d", count);
        for (i = 0; i < count; ++i) {
            const SSL_CIPHER *c = sk_SSL_CIPHER_value(ciphers, i);
            gateway_log("[SSL] enabled cipher[%d]=%s", i, SSL_CIPHER_get_name(c));
        }
        if (probe_ssl) {
            SSL_free(probe_ssl);
        }
    }
    return 1;
}

static void shutdown_ssl(void)
{
    if (g_ssl_ctx) {
        SSL_CTX_free(g_ssl_ctx);
        g_ssl_ctx = NULL;
    }
    EVP_cleanup();
    ERR_free_strings();
    openssl_threading_shutdown();
}

int gateway_run(gateway_config_t *cfg)
{
    WSADATA wsa;
    HANDLE threads[2];
    DWORD count = 0;

    CreateDirectoryA("logs", NULL);
    gateway_log_init(cfg->log_file);

    if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
        gateway_log("[ERROR] WSAStartup failed");
        gateway_log_shutdown();
        return 0;
    }

    SetConsoleCtrlHandler(console_handler, TRUE);
    if (!init_ssl(cfg)) {
        WSACleanup();
        gateway_log_shutdown();
        return 0;
    }

    gateway_log("NFS The Run Legacy SSL Gateway starting");

    if (cfg->redirector.enabled) {
        cfg->redirector.thread = CreateThread(NULL, 0, listener_thread, &cfg->redirector, 0, NULL);
        if (cfg->redirector.thread) threads[count++] = cfg->redirector.thread;
    }
    if (cfg->blaze.enabled) {
        cfg->blaze.thread = CreateThread(NULL, 0, listener_thread, &cfg->blaze, 0, NULL);
        if (cfg->blaze.thread) threads[count++] = cfg->blaze.thread;
    }

    if (count == 0) {
        gateway_log("[ERROR] no listeners enabled");
        shutdown_ssl();
        WSACleanup();
        gateway_log_shutdown();
        return 0;
    }

    gateway_log("Press Ctrl+C to stop.");
    while (InterlockedCompareExchange(&g_running, 1, 1) != 0) Sleep(250);

    if (cfg->redirector.listener != INVALID_SOCKET) closesocket(cfg->redirector.listener);
    if (cfg->blaze.listener != INVALID_SOCKET) closesocket(cfg->blaze.listener);
    WaitForMultipleObjects(count, threads, TRUE, 3000);
    if (cfg->redirector.thread) CloseHandle(cfg->redirector.thread);
    if (cfg->blaze.thread) CloseHandle(cfg->blaze.thread);

    shutdown_ssl();
    WSACleanup();
    gateway_log("Gateway stopped.");
    gateway_log_shutdown();
    return 1;
}
