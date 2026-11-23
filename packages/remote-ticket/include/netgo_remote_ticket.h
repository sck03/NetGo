#ifndef NETGO_REMOTE_TICKET_H
#define NETGO_REMOTE_TICKET_H

#include "netgo_remote.h"

#ifdef __cplusplus
extern "C" {
#endif

#define NG_REMOTE_TICKET_MESSAGE_SIZE 104u
#define NG_REMOTE_TICKET_SIZE 168u

typedef struct ng_remote_ticket_verifier ng_remote_ticket_verifier;

/* The key must come from the authenticated site configuration, never the peer
 * or ticket itself. Immutable verifier; concurrent verification is supported. */
int32_t ng_remote_ticket_verifier_create(const uint8_t* site_public_key, size_t key_size,
                                         ng_remote_ticket_verifier** output);
/* Join all verify calls and destroy dependent gates before destroying this. */
void ng_remote_ticket_verifier_destroy(ng_remote_ticket_verifier* verifier);
/* Directly compatible with ng_remote_options.verify. Caller initializes claims
 * size/version. Once that header is valid, failures clear all claim fields;
 * an ABI mismatch leaves memory untouched. No replay store,
 * current-policy check or controller proof of possession is provided here. */
int32_t ng_remote_ticket_verify(void* verifier, const uint8_t* ticket, size_t size,
                                ng_remote_claims* claims);

typedef struct ng_remote_ticket_id {
    const uint8_t* data;
    size_t size;
} ng_remote_ticket_id;

typedef struct ng_remote_ticket_binding {
    uint32_t struct_size;
    uint32_t abi_version;
    ng_remote_ticket_id site_id;
    ng_remote_ticket_id user_id;
    ng_remote_ticket_id controller_id;
    ng_remote_ticket_id host_id;
    uint8_t site_key[32];
    uint8_t controller_key[32];
    uint8_t host_key[32];
    uint8_t host_boot[16];
} ng_remote_ticket_binding;

/* All IDs and keys must be reconstructed from authenticated host/session state.
 * See docs/REMOTE_TICKET.zh-CN.md for exact length-prefixed canonical encoding. */
int32_t ng_remote_ticket_binding_digest(const ng_remote_ticket_binding* binding,
                                        uint8_t output[32]);

/* Host proof verification MUST use its own fresh random challenge and the
 * exporter from the negotiated TLS session, never values echoed by the peer.
 * Ticket authentication, current authorization and atomic consumption are
 * separate mandatory checks. All array arguments have exactly the stated size. */
int32_t ng_remote_possession_verify(const uint8_t controller_key[32],
                                   const uint8_t* ticket, size_t ticket_size,
                                   const uint8_t challenge[32],
                                   const uint8_t tls_exporter[32],
                                   const uint8_t signature[64]);

#ifdef __cplusplus
}
#endif
#endif
