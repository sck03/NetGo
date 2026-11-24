#ifndef NETGO_APPLE_H
#define NETGO_APPLE_H
#include "netgo_remote_client.h"
#ifdef __cplusplus
extern "C" {
#endif
/* Only a protected socket owned by the current user is accepted. */
int32_t ng_apple_ipc(const char *socket, const char *method, const char *path,
                     const char *body, char *output, size_t capacity, size_t *length);
int32_t ng_apple_tunnel_descriptor(char *name, size_t capacity);
#ifdef __cplusplus
}
#endif
#endif
