#include "gateway.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void copy_ini_string(const char *section, const char *key, const char *def,
                            char *out, DWORD out_size, const char *path)
{
    GetPrivateProfileStringA(section, key, def, out, out_size, path);
}

static void load_route(const char *section, const char *default_name,
                       unsigned short default_listen_port,
                       unsigned short default_backend_port,
                       const char *path, gateway_route_t *route)
{
    char temp[64];
    ZeroMemory(route, sizeof(*route));
    strncpy_s(route->name, sizeof(route->name), default_name, _TRUNCATE);
    route->enabled = GetPrivateProfileIntA(section, "enabled", 1, path) != 0;
    copy_ini_string(section, "listen_host", "127.0.0.1", route->listen_host,
                    (DWORD)sizeof(route->listen_host), path);
    route->listen_port = (unsigned short)GetPrivateProfileIntA(section, "listen_port",
                                                                default_listen_port, path);
    copy_ini_string(section, "backend_host", "127.0.0.1", route->backend_host,
                    (DWORD)sizeof(route->backend_host), path);
    route->backend_port = (unsigned short)GetPrivateProfileIntA(section, "backend_port",
                                                                 default_backend_port, path);
    route->listener = INVALID_SOCKET;
    route->thread = NULL;

    copy_ini_string(section, "name", default_name, temp, (DWORD)sizeof(temp), path);
    strncpy_s(route->name, sizeof(route->name), temp, _TRUNCATE);
}

int gateway_load_config(const char *path, gateway_config_t *cfg)
{
    DWORD attrs;
    if (!path || !cfg) return 0;
    attrs = GetFileAttributesA(path);
    if (attrs == INVALID_FILE_ATTRIBUTES || (attrs & FILE_ATTRIBUTE_DIRECTORY)) {
        fprintf(stderr, "[ERROR] Config file not found: %s\n", path);
        return 0;
    }

    ZeroMemory(cfg, sizeof(*cfg));
    copy_ini_string("gateway", "cert_file", "certs\\protossl_redirector.crt", cfg->cert_file,
                    (DWORD)sizeof(cfg->cert_file), path);
    copy_ini_string("gateway", "key_file", "certs\\protossl_redirector.key", cfg->key_file,
                    (DWORD)sizeof(cfg->key_file), path);
    copy_ini_string("gateway", "cipher_list",
                    "RC4-SHA:RC4-MD5:AES128-SHA:AES256-SHA:DES-CBC3-SHA",
                    cfg->cipher_list, (DWORD)sizeof(cfg->cipher_list), path);
    copy_ini_string("gateway", "log_file", "logs\\gateway.log", cfg->log_file,
                    (DWORD)sizeof(cfg->log_file), path);

    load_route("redirector", "REDIRECTOR", 42129, 42128, path, &cfg->redirector);
    load_route("blaze", "WEBAUTOLOG", 18443, 18080, path, &cfg->blaze);
    return 1;
}
