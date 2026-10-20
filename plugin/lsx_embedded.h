/* Embedded stand-in for the Origin client's LSX service (127.0.0.1:3216), so players do not need to run anything else.
 * Port of server/lsx.py: the game's EbisuSDK talks NUL-terminated XML to it; the challenge/response and encrypted messages use
 * AES-128-ECB with the fixed key 00 01 .. 0f (hex-encoded, PKCS padding). The player name travels to the Blaze server inside the
 * auth token (hex of the name, 16 bytes), see server/trserver.py. Also creates the Origin stub (file + registry value) the SDK looks for. */

static unsigned char lx_sbox[256], lx_inv[256], lx_rk[11][16];
static char g_lsx_name[24] = "TRPlayer", g_lsx_uid[16] = "1000000001";

static unsigned char lx_xt(unsigned char a) { return (unsigned char)((a << 1) ^ ((a & 0x80) ? 0x1B : 0)); }
static unsigned char lx_rotl(unsigned char v, int n) { return (unsigned char)((v << n) | (v >> (8 - n))); }
static unsigned char lx_mul(unsigned char a, unsigned char b) { unsigned char r = 0; while (b) { if (b & 1) r ^= a; a = lx_xt(a); b >>= 1; } return r; }

static void lx_aes_init(void)
{
    unsigned char p = 1, q = 1;
    do {
        p = (unsigned char)(p ^ (p << 1) ^ ((p & 0x80) ? 0x1B : 0));
        q ^= (unsigned char)(q << 1); q ^= (unsigned char)(q << 2); q ^= (unsigned char)(q << 4);
        if (q & 0x80) q ^= 0x09;
        lx_sbox[p] = (unsigned char)(q ^ lx_rotl(q, 1) ^ lx_rotl(q, 2) ^ lx_rotl(q, 3) ^ lx_rotl(q, 4) ^ 0x63);
    } while (p != 1);
    lx_sbox[0] = 0x63;
    for (int i = 0; i < 256; ++i) lx_inv[lx_sbox[i]] = (unsigned char)i;
    unsigned char w[44][4]; unsigned char rcon = 1;
    for (int i = 0; i < 4; ++i) for (int j = 0; j < 4; ++j) w[i][j] = (unsigned char)(i * 4 + j);   /* key = 00..0f */
    for (int i = 4; i < 44; ++i) {
        unsigned char t[4]; memcpy(t, w[i - 1], 4);
        if (i % 4 == 0) {
            unsigned char t0 = t[0]; t[0] = lx_sbox[t[1]]; t[1] = lx_sbox[t[2]]; t[2] = lx_sbox[t[3]]; t[3] = lx_sbox[t0];
            t[0] ^= rcon; rcon = lx_xt(rcon);
        }
        for (int j = 0; j < 4; ++j) w[i][j] = (unsigned char)(w[i - 4][j] ^ t[j]);
    }
    for (int r = 0; r < 11; ++r) for (int c = 0; c < 4; ++c) memcpy(&lx_rk[r][c * 4], w[r * 4 + c], 4);
}

static void lx_enc_block(unsigned char *b)
{
    unsigned char s[16], t[16];
    for (int i = 0; i < 16; ++i) s[i] = b[i] ^ lx_rk[0][i];
    for (int rnd = 1; rnd <= 10; ++rnd) {
        for (int i = 0; i < 16; ++i) t[i] = lx_sbox[s[i]];
        for (int i = 0; i < 16; ++i) s[i] = t[(i + 4 * (i % 4)) % 16];
        if (rnd != 10) {
            for (int c = 0; c < 4; ++c) {
                unsigned char *a = &s[c * 4];
                t[c * 4 + 0] = (unsigned char)(lx_xt(a[0]) ^ lx_xt(a[1]) ^ a[1] ^ a[2] ^ a[3]);
                t[c * 4 + 1] = (unsigned char)(a[0] ^ lx_xt(a[1]) ^ lx_xt(a[2]) ^ a[2] ^ a[3]);
                t[c * 4 + 2] = (unsigned char)(a[0] ^ a[1] ^ lx_xt(a[2]) ^ lx_xt(a[3]) ^ a[3]);
                t[c * 4 + 3] = (unsigned char)(lx_xt(a[0]) ^ a[0] ^ a[1] ^ a[2] ^ lx_xt(a[3]));
            }
            memcpy(s, t, 16);
        }
        for (int i = 0; i < 16; ++i) s[i] ^= lx_rk[rnd][i];
    }
    memcpy(b, s, 16);
}

static void lx_dec_block(unsigned char *b)
{
    unsigned char s[16], t[16];
    for (int i = 0; i < 16; ++i) s[i] = b[i] ^ lx_rk[10][i];
    for (int rnd = 9; rnd >= 0; --rnd) {
        for (int i = 0; i < 16; ++i) t[i] = s[(i - 4 * (i % 4) + 16) % 16];
        for (int i = 0; i < 16; ++i) s[i] = lx_inv[t[i]] ^ lx_rk[rnd][i];
        if (rnd) {
            for (int c = 0; c < 4; ++c) {
                unsigned char *a = &s[c * 4];
                t[c * 4 + 0] = (unsigned char)(lx_mul(a[0], 14) ^ lx_mul(a[1], 11) ^ lx_mul(a[2], 13) ^ lx_mul(a[3], 9));
                t[c * 4 + 1] = (unsigned char)(lx_mul(a[0], 9) ^ lx_mul(a[1], 14) ^ lx_mul(a[2], 11) ^ lx_mul(a[3], 13));
                t[c * 4 + 2] = (unsigned char)(lx_mul(a[0], 13) ^ lx_mul(a[1], 9) ^ lx_mul(a[2], 14) ^ lx_mul(a[3], 11));
                t[c * 4 + 3] = (unsigned char)(lx_mul(a[0], 11) ^ lx_mul(a[1], 13) ^ lx_mul(a[2], 9) ^ lx_mul(a[3], 14));
            }
            memcpy(s, t, 16);
        }
    }
    memcpy(b, s, 16);
}

/* text -> hex of AES(text + padding); returns length */
static int lx_encrypt_hex(const char *text, char *out, int outsz)
{
    int n = (int)strlen(text), pad = 16 - n % 16, tot = n + pad;
    if (tot * 2 + 1 > outsz) return 0;
    unsigned char buf[4096]; if (tot > (int)sizeof buf) return 0;
    memcpy(buf, text, n); memset(buf + n, pad, pad);
    for (int i = 0; i < tot; i += 16) lx_enc_block(buf + i);
    for (int i = 0; i < tot; ++i) sprintf(out + i * 2, "%02x", buf[i]);
    return tot * 2;
}

static int lx_decrypt_hex(const char *hex, char *out, int outsz)
{
    int hl = (int)strlen(hex); while (hl && (hex[hl - 1] == '\r' || hex[hl - 1] == '\n' || hex[hl - 1] == ' ')) --hl;
    int n = hl / 2; if (n == 0 || n % 16 || n >= outsz || n > 4096) return 0;
    for (int i = 0; i < n; ++i) { unsigned v; if (sscanf(hex + i * 2, "%2x", &v) != 1) return 0; out[i] = (char)v; }
    for (int i = 0; i < n; i += 16) lx_dec_block((unsigned char *)out + i);
    int pad = (unsigned char)out[n - 1]; if (pad < 1 || pad > 16) pad = 0;
    out[n - pad] = 0; return n - pad;
}

/* <LSX><kind sender=".." id="..">inner</kind></LSX> , optionally encrypted, NUL terminated */
static void lx_send(SOCKET c, const char *kind, const char *sender, const char *rid, const char *inner, int enc)
{
    char xml[3000], out[8192];
    if (rid && *rid) snprintf(xml, sizeof xml, "<LSX><%s sender=\"%s\" id=\"%s\">%s</%s></LSX>", kind, sender, rid, inner, kind);
    else snprintf(xml, sizeof xml, "<LSX><%s sender=\"%s\">%s</%s></LSX>", kind, sender, inner, kind);
    int n;
    if (enc) { n = lx_encrypt_hex(xml, out, sizeof out - 1); } else { n = (int)strlen(xml); memcpy(out, xml, n); }
    out[n] = 0; send(c, out, n + 1, 0);
}

static int lx_attr(const char *tag, const char *name, char *out, int outsz)
{
    char pat[64]; snprintf(pat, sizeof pat, " %s=\"", name);
    const char *end = strchr(tag, '>'); const char *p = strstr(tag, pat);
    if (!p || (end && p > end)) { out[0] = 0; return 0; }
    p += strlen(pat); const char *q = strchr(p, '"'); if (!q) { out[0] = 0; return 0; }
    int n = (int)(q - p); if (n >= outsz) n = outsz - 1; memcpy(out, p, n); out[n] = 0; return 1;
}

static void lx_answer(SOCKET c, const char *tagname, const char *tagtext, const char *rid, const char *rcp)
{
    char inner[1024], key[128], enc[1024];
    const char *sender = (rcp && *rcp) ? rcp : "EALS";
    if (!strcmp(tagname, "ChallengeResponse")) {
        lx_attr(tagtext, "key", key, sizeof key);
        lx_encrypt_hex(key, enc, sizeof enc);
        snprintf(inner, sizeof inner, "<ChallengeAccepted response=\"%s\"/>", enc);
        lx_send(c, "Response", sender, rid, inner, 0);
        lx_send(c, "Event", "EALS", NULL, "<Login version=\"2\" IsLoggedIn=\"1\"/>", 1);
        lx_send(c, "Event", "EbisuSDK", NULL, "<Login version=\"2\" IsLoggedIn=\"1\"/>", 1);
        return;
    }
    if (!strcmp(tagname, "GetConfig")) snprintf(inner, sizeof inner, "<GetConfigResponse version=\"2\"/>");
    else if (!strcmp(tagname, "GetProfile")) snprintf(inner, sizeof inner, "<GetProfileResponse version=\"2\" UserId=\"%s\" Persona=\"%s\" AvatarId=\"0\"/>", g_lsx_uid, g_lsx_name);
    else if (!strcmp(tagname, "GetInternetConnectedState")) snprintf(inner, sizeof inner, "<InternetConnectedState version=\"2\" connected=\"1\"/>");
    else if (!strcmp(tagname, "GetAuthToken")) {
        char tok[40] = ""; unsigned char raw[16] = {0}; strncpy((char *)raw, g_lsx_name, 16);
        for (int i = 0; i < 16; ++i) sprintf(tok + i * 2, "%02x", raw[i]);
        snprintf(inner, sizeof inner, "<AuthToken version=\"2\" value=\"%s\"/>", tok);
    }
    else if (!strcmp(tagname, "CheckPermission")) snprintf(inner, sizeof inner, "<CheckPermissionResponse version=\"2\" Access=\"GRANTED\"/>");
    else if (!strcmp(tagname, "GetSetting")) snprintf(inner, sizeof inner, "<GetSettingResponse version=\"2\" Setting=\"\"/>");
    else if (!strcmp(tagname, "QueryFriends")) snprintf(inner, sizeof inner, "<QueryFriendsResponse version=\"2\"/>");
    else if (!strcmp(tagname, "GetBlockList")) snprintf(inner, sizeof inner, "<GetBlockListResponse version=\"2\"/>");
    else if (!strcmp(tagname, "GetPresence") || !strcmp(tagname, "QueryPresence"))
        snprintf(inner, sizeof inner, "<GetPresenceResponse version=\"2\" UserId=\"%s\" Presence=\"ONLINE\" Title=\"\" TitleId=\"\" RichPresence=\"\" GamePresence=\"\" SessionId=\"\"/>", g_lsx_uid);
    else snprintf(inner, sizeof inner, "<ErrorSuccess version=\"2\" Code=\"0\" Description=\"\"/>");
    lx_send(c, "Response", sender, rid, inner, 1);
    if (!strcmp(tagname, "GetProfile")) lx_send(c, "Event", "EbisuSDK", NULL, "<Login version=\"2\" IsLoggedIn=\"1\"/>", 1);
}

static void lx_message(SOCKET c, char *txt)
{
    char plain[4200];
    if (*txt && *txt != '<') { if (!lx_decrypt_hex(txt, plain, sizeof plain)) return; txt = plain; }
    char *req = strstr(txt, "<Request"); if (!req) return;
    char rid[32], rcp[32]; lx_attr(req, "id", rid, sizeof rid); lx_attr(req, "recipient", rcp, sizeof rcp);
    char *p = strchr(req, '>'); if (!p) return;
    while ((p = strchr(p, '<')) != NULL) {
        if (p[1] == '/' || p[1] == '?') { if (!strncmp(p, "</Request", 9)) break; p++; continue; }
        char name[48]; int n = 0; const char *q = p + 1;
        while (*q && *q != ' ' && *q != '/' && *q != '>' && n < 47) name[n++] = *q++;
        name[n] = 0; if (n) lx_answer(c, name, p, rid, rcp);
        p++;
    }
}

static DWORD WINAPI lx_client(LPVOID arg)
{
    SOCKET c = (SOCKET)(DWORD_PTR)arg; char key[24], inner[160], buf[16384]; int len = 0;
    for (int i = 0; i < 16; ++i) key[i] = "0123456789abcdef"[rand() & 15];
    key[16] = 0; snprintf(inner, sizeof inner, "<Challenge version=\"3\" key=\"%s\"/>", key);
    lx_send(c, "Event", "EALS", NULL, inner, 0);
    for (;;) {
        int n = recv(c, buf + len, (int)sizeof buf - 1 - len, 0);
        if (n <= 0) break;
        len += n; buf[len] = 0;
        char *start = buf, *nul;
        while ((nul = memchr(start, 0, buf + len - start)) != NULL) { lx_message(c, start); start = nul + 1; }
        len = (int)(buf + len - start); memmove(buf, start, len);
        if (len >= (int)sizeof buf - 1) len = 0;
    }
    closesocket(c); return 0;
}

static DWORD WINAPI lx_server(LPVOID unused)
{
    SOCKET s = socket(AF_INET, SOCK_STREAM, 0); int one = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, (const char *)&one, sizeof one);
    struct sockaddr_in a; memset(&a, 0, sizeof a); a.sin_family = AF_INET; a.sin_port = htons(3216); a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (bind(s, (struct sockaddr *)&a, sizeof a) || listen(s, 8)) { logf_("lsx: port 3216 busy (an external stand-in is running?), embedded one not started"); closesocket(s); return 0; }
    logf_("lsx: embedded Origin stand-in listening on 127.0.0.1:3216 as %s", g_lsx_name);
    for (;;) { SOCKET c = accept(s, NULL, NULL); if (c == INVALID_SOCKET) break; CreateThread(NULL, 0, lx_client, (LPVOID)(DWORD_PTR)c, 0, NULL); }
    return 0;
}


/* ---- Origin install lookup, faked inside this process only (default; nothing on the system is changed) ----
 * The SDK opens HKLM\SOFTWARE\Origin (32-bit view) and reads "ClientPath", then checks that file exists. When the real key is absent we answer
 * that lookup ourselves with the path of an empty stub file next to the plugin. A real Origin install is never overridden. */
static hook_t h_rok, h_rqv, h_rck;
static LONG (WINAPI *real_RegOpenKeyExW)(HKEY, LPCWSTR, DWORD, REGSAM, PHKEY);
static LONG (WINAPI *real_RegQueryValueExW)(HKEY, LPCWSTR, LPDWORD, LPDWORD, LPBYTE, LPDWORD);
static LONG (WINAPI *real_RegCloseKey)(HKEY);
static wchar_t g_stub_path[MAX_PATH];
#define FAKE_ORIGIN_KEY ((HKEY)(DWORD_PTR)0x0F0D0001)

static int lx_is_origin_key(LPCWSTR sub)
{
    return sub && (!_wcsicmp(sub, L"SOFTWARE\\Origin") || !_wcsicmp(sub, L"SOFTWARE\\Wow6432Node\\Origin"));
}
static LONG WINAPI my_RegOpenKeyExW(HKEY k, LPCWSTR sub, DWORD opt, REGSAM sam, PHKEY out)
{
    hook_off(&h_rok); LONG r = real_RegOpenKeyExW(k, sub, opt, sam, out); hook_on(&h_rok);
    if (r != ERROR_SUCCESS && k == HKEY_LOCAL_MACHINE && lx_is_origin_key(sub) && out) { *out = FAKE_ORIGIN_KEY; return ERROR_SUCCESS; }
    return r;
}
static LONG WINAPI my_RegQueryValueExW(HKEY k, LPCWSTR name, LPDWORD res, LPDWORD type, LPBYTE data, LPDWORD cb)
{
    if (k == FAKE_ORIGIN_KEY) {
        if (!name || _wcsicmp(name, L"ClientPath")) return ERROR_FILE_NOT_FOUND;
        DWORD need = (DWORD)((wcslen(g_stub_path) + 1) * sizeof(wchar_t));
        if (type) *type = REG_SZ;
        if (!data) { if (cb) *cb = need; return ERROR_SUCCESS; }
        if (!cb || *cb < need) { if (cb) *cb = need; return ERROR_MORE_DATA; }
        memcpy(data, g_stub_path, need); *cb = need; return ERROR_SUCCESS;
    }
    hook_off(&h_rqv); LONG r = real_RegQueryValueExW(k, name, res, type, data, cb); hook_on(&h_rqv);
    return r;
}
static LONG WINAPI my_RegCloseKey(HKEY k)
{
    if (k == FAKE_ORIGIN_KEY) return ERROR_SUCCESS;
    hook_off(&h_rck); LONG r = real_RegCloseKey(k); hook_on(&h_rck);
    return r;
}
static void origin_memory_stub(void)
{
    HMODULE self = NULL; GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, (LPCSTR)origin_memory_stub, &self);
    GetModuleFileNameW(self, g_stub_path, MAX_PATH);
    wchar_t *sl = wcsrchr(g_stub_path, L'\\'); if (sl) *(sl + 1) = 0;
    wcscat(g_stub_path, L"NFSTR_OriginStub.exe");
    HANDLE f = CreateFileW(g_stub_path, GENERIC_WRITE, FILE_SHARE_READ, NULL, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, NULL);   /* empty file in the plugin's own folder */
    if (f != INVALID_HANDLE_VALUE) CloseHandle(f);
    HMODULE adv = LoadLibraryA("advapi32.dll");
    real_RegOpenKeyExW = (void *)GetProcAddress(adv, "RegOpenKeyExW");
    real_RegQueryValueExW = (void *)GetProcAddress(adv, "RegQueryValueExW");
    real_RegCloseKey = (void *)GetProcAddress(adv, "RegCloseKey");
    if (real_RegOpenKeyExW && real_RegQueryValueExW && real_RegCloseKey) {
        hook_install(&h_rok, real_RegOpenKeyExW, my_RegOpenKeyExW);
        hook_install(&h_rqv, real_RegQueryValueExW, my_RegQueryValueExW);
        hook_install(&h_rck, real_RegCloseKey, my_RegCloseKey);
        logf_("origin stub (in memory): registry lookup answered inside this process only, stub file %ls", g_stub_path);
    } else logf_("origin stub (in memory): could not find the registry functions");
}

/* name from [Player] Name= (3-16 of A-Z a-z 0-9 _ -), start the server thread, and create the Origin stub the SDK checks for */
static void lsx_embedded_start(const char *ini)
{
    char raw[64] = "", name[24] = ""; int n = 0;
    GetPrivateProfileStringA("Player", "Name", "TRPlayer", raw, sizeof raw, ini);
    for (const char *p = raw; *p && n < 16; ++p)
        if ((*p >= 'A' && *p <= 'Z') || (*p >= 'a' && *p <= 'z') || (*p >= '0' && *p <= '9') || *p == '_' || *p == '-') name[n++] = *p;
    name[n] = 0; if (n < 3) strcpy(name, "TRPlayer");
    strcpy(g_lsx_name, name);
    if (!strcmp(name, "TRPlayer")) strcpy(g_lsx_uid, "1000000001");
    else { unsigned sum = 0; for (const char *p = name; *p; ++p) sum += (unsigned char)*p; snprintf(g_lsx_uid, sizeof g_lsx_uid, "%u", 2000000001u + sum % 1000); }
    lx_aes_init();
    CreateThread(NULL, 0, lx_server, NULL, 0, NULL);
    char mode[16] = "memory"; GetPrivateProfileStringA("Origin", "Stub", "memory", mode, sizeof mode, ini);
    if (!strcmp(mode, "memory")) origin_memory_stub();                       /* default: no system change */
    else if (!strcmp(mode, "system")) {                                      /* old behaviour: real file + registry value, only if absent */
        /* Only ever ADDS things: an existing Origin.exe or ClientPath value (a real Origin install) is left alone. */
        HKEY k = NULL; static const char path[] = "C:\\Program Files (x86)\\Origin\\Origin.exe";
        if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, "Software\\Wow6432Node\\Origin", 0, KEY_QUERY_VALUE | KEY_WOW64_64KEY, &k) == ERROR_SUCCESS &&
            RegQueryValueExA(k, "ClientPath", NULL, NULL, NULL, NULL) == ERROR_SUCCESS) {
            RegCloseKey(k); logf_("origin stub: ClientPath already present, left unchanged");
        } else {
            if (k) { RegCloseKey(k); k = NULL; }
            CreateDirectoryA("C:\\Program Files (x86)", NULL); CreateDirectoryA("C:\\Program Files (x86)\\Origin", NULL);
            HANDLE f = CreateFileA(path, GENERIC_WRITE, FILE_SHARE_READ, NULL, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, NULL);   /* never touches an existing file */
            if (f != INVALID_HANDLE_VALUE) CloseHandle(f);
            if (RegCreateKeyExA(HKEY_LOCAL_MACHINE, "Software\\Wow6432Node\\Origin", 0, NULL, 0, KEY_SET_VALUE | KEY_WOW64_64KEY, NULL, &k, NULL) == ERROR_SUCCESS) {
                RegSetValueExA(k, "ClientPath", 0, REG_SZ, (const BYTE *)path, (DWORD)sizeof path); RegCloseKey(k);
                logf_("origin stub: created Origin.exe stub and ClientPath registry value");
            } else logf_("origin stub: could not write HKLM (run once as administrator or add the value by hand)");
        }
    }
}
