#pragma once
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
void *ng_apple_host_media_create(void);
void ng_apple_host_media_destroy(void *);
int32_t ng_apple_host_permissions(void);
int32_t ng_apple_host_media_open(void *, uint32_t, uint32_t *, uint32_t *, uint32_t *);
int32_t ng_apple_host_media_poll(void *, uint32_t *, uint8_t *, size_t, size_t *);
int32_t ng_apple_host_media_input(void *, const char *, size_t);
void ng_apple_host_media_close(void *);
int32_t ng_apple_host_clipboard(void *, int32_t, const char *, size_t, char *, size_t, size_t *);
#ifdef __cplusplus
}
#endif
