#ifndef NFSRUN_GATEWAY_H
#define NFSRUN_GATEWAY_H

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <winsock2.h>
#include <ws2tcpip.h>
#include <openssl/ssl.h>
#include <openssl/err.h>

#define NFSRUN_MAX_HOST 256
#define NFSRUN_MAX_PATH 512
#define NFSRUN_MAX_CIPHERS 512

typedef struct gateway_route_s {
    char name[32];
    int enabled;
    char listen_host[NFSRUN_MAX_HOST];
    unsigned short listen_port;
    char backend_host[NFSRUN_MAX_HOST];
    unsigned short backend_port;
    SOCKET listener;
    HANDLE thread;
} gateway_route_t;

typedef struct gateway_config_s {
    char cert_file[NFSRUN_MAX_PATH];
    char key_file[NFSRUN_MAX_PATH];
    char cipher_list[NFSRUN_MAX_CIPHERS];
    char log_file[NFSRUN_MAX_PATH];
    gateway_route_t redirector;
    gateway_route_t blaze;
} gateway_config_t;

int gateway_load_config(const char *path, gateway_config_t *cfg);
int gateway_run(gateway_config_t *cfg);
void gateway_log(const char *fmt, ...);

#endif
