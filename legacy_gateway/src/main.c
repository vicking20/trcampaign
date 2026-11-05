#include "gateway.h"
#include <stdio.h>

int main(int argc, char **argv)
{
    gateway_config_t cfg;
    const char *config_path = "config\\gateway.ini";

    if (argc >= 2) config_path = argv[1];

    printf("============================================================\n");
    printf(" NFS The Run - Legacy SSLv3 Gateway\n");
    printf("============================================================\n\n");
    printf("Config: %s\n\n", config_path);

    if (!gateway_load_config(config_path, &cfg)) return 1;
    return gateway_run(&cfg) ? 0 : 1;
}
