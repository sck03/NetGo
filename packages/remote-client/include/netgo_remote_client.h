#ifndef NETGO_REMOTE_CLIENT_H
#define NETGO_REMOTE_CLIENT_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
#define NG_REMOTE_CLIENT_ABI 1u
typedef struct ng_remote_client ng_remote_client;
/* Control and active may run on the monitoring thread. Control calls are
 * serialized. Other callbacks run on the thread calling run; borrowed data
 * expires when the callback returns. Callbacks must not throw across this ABI.
 * control("CONTEXT", "", ...) returns the authenticated site/device snapshot.
 * sign signs the exact possession message using the existing device identity.
 * input writes a JSON array of events, or [] when none are pending.
 * All IO callbacks must honor cancellation and a bounded deadline. */
typedef struct ng_remote_client_options {
 uint32_t struct_size, abi_version;
 const char *site_id, *host_id;
 uint32_t capabilities;
 void *context;
 int32_t (*active)(void *context);
 int32_t (*control)(void *context, const char *method, const char *path,
                    const char *body, const char *idempotency,
                    char *output, size_t capacity, size_t *length);
 int32_t (*sign)(void *context, const uint8_t *message, size_t size, uint8_t signature[64]);
 int32_t (*input)(void *context, char *output, size_t capacity, size_t *length);
 int32_t (*format)(void *context, uint32_t width, uint32_t height, uint32_t fps, uint32_t capabilities);
 int32_t (*video)(void *context, const uint8_t *h264, size_t size, int64_t time_100ns);
 int32_t (*audio)(void *context, const uint8_t *pcm, size_t size);
 void (*status)(void *context, const char *code);
} ng_remote_client_options;
enum { NG_REMOTE_CLIENT_OK=0, NG_REMOTE_CLIENT_INVALID=-1, NG_REMOTE_CLIENT_FAILED=-2,
       NG_REMOTE_CLIENT_BUSY=-3 };
int32_t ng_remote_client_create(const ng_remote_client_options *options, ng_remote_client **output);
/* Run once, on a worker thread. Cancel is thread safe and idempotent. Destroy
 * only after run has returned and the caller has joined its worker. */
int32_t ng_remote_client_run(ng_remote_client *client);
void ng_remote_client_cancel(ng_remote_client *client);
void ng_remote_client_destroy(ng_remote_client *client);
#ifdef __cplusplus
}
#endif
#endif
