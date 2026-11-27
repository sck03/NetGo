#ifndef NETGO_REMOTE_HOST_H
#define NETGO_REMOTE_HOST_H
#include "netgo_remote_client.h"
#ifdef __cplusplus
extern "C" {
#endif
#define NG_REMOTE_HOST_ABI 1u
typedef struct ng_remote_host ng_remote_host;
/* The caller owns the OS permission UI and persistent unattended preference.
 * A host is visible, cancellable, tied to an authenticated VPN and has one
 * bounded session. No device private key is exported to platform adapters. */
typedef struct ng_remote_host_options {
    uint32_t struct_size, abi_version;
    const char *site_id, *name, *platform, *exchange_directory;
    uint32_t capabilities, unattended;
    void *context;
    /* Queried by the run, control and command threads; must be thread-safe. */
    int32_t (*active)(void *);
    int32_t (*control)(void *, const char *, const char *, const char *, const char *, char *, size_t,
                       size_t *);
    int32_t (*consent)(void *, const char *, uint32_t);
    int32_t (*open)(void *, uint32_t, uint32_t *, uint32_t *, uint32_t *);
    /* poll returns one H.264 Annex B or audio packet; zero bytes means no packet.
     * It must return in <=100 ms. kind 5=video, 10=PCM audio. */
    int32_t (*poll)(void *, uint32_t *, uint8_t *, size_t, size_t *);
    int32_t (*input)(void *, const char *, size_t);
    void (*close)(void *);
    /* Runs on the bounded command worker, concurrently with media callbacks.
     * UI work must be marshalled to the platform UI thread. */
    int32_t (*clipboard)(void *, int32_t, const char *, size_t, char *, size_t, size_t *);
    void (*status)(void *, const char *);
} ng_remote_host_options;
int32_t ng_remote_host_create(const ng_remote_host_options *, ng_remote_host **);
/* run joins all workers before returning. cancel requests shutdown; destroy
 * must be called only after run returns and no callbacks remain in flight. */
int32_t ng_remote_host_run(ng_remote_host *);
void ng_remote_host_cancel(ng_remote_host *);
void ng_remote_host_destroy(ng_remote_host *);
#ifdef __cplusplus
}
#endif
#endif
