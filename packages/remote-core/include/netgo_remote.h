#ifndef NETGO_REMOTE_H
#define NETGO_REMOTE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Pure C ABI. No platform handles, C++ objects, allocator or media SDK crosses it. */
#define NG_REMOTE_ABI 1u
#define NG_REMOTE_MAX_TICKET_BYTES 16384u
#define NG_REMOTE_MAX_LEASE_MS 30000u

enum ng_remote_capability {
    NG_REMOTE_VIEW = 1u << 0,
    NG_REMOTE_INPUT = 1u << 1,
    NG_REMOTE_AUDIO = 1u << 2,
    NG_REMOTE_CLIPBOARD = 1u << 3,
    NG_REMOTE_UPLOAD = 1u << 4,
    NG_REMOTE_DOWNLOAD = 1u << 5,
    NG_REMOTE_ALL_CAPABILITIES = (1u << 6) - 1u
};

enum ng_remote_result {
    NG_REMOTE_OK = 0,
    NG_REMOTE_INVALID_ARGUMENT = 1,
    NG_REMOTE_ABI_MISMATCH = 2,
    NG_REMOTE_INVALID_TICKET = 3,
    NG_REMOTE_DENIED = 4,
    NG_REMOTE_CONSENT_REQUIRED = 5,
    NG_REMOTE_REPLAY = 6,
    NG_REMOTE_EXPIRED = 7,
    NG_REMOTE_CLOSED = 8,
    NG_REMOTE_CLOCK_ERROR = 9,
    NG_REMOTE_INTERNAL_ERROR = 10
};

enum ng_remote_state {
    NG_REMOTE_NEW = 0,
    NG_REMOTE_AWAITING_CONSENT = 1,
    NG_REMOTE_ACTIVE = 2,
    NG_REMOTE_TERMINATED = 3
};

enum ng_remote_consent_mode {
    NG_REMOTE_LOCAL_CONSENT = 0,
    NG_REMOTE_ALLOW_UNATTENDED = 1
};

/* Filled ONLY by a trusted signature/issuer/algorithm verifier. The verifier
 * must authenticate every field, reject unknown fields and enforce canonical
 * encoding. The gate checks binding, lifetime, sequence and permissions again.
 * binding_digest identifies site, user/controller key, host key and boot instance.
 * Canonical ticket encoding and its concrete crypto adapter belong to API v2. */
typedef struct ng_remote_claims {
    uint32_t struct_size;
    uint32_t abi_version;
    uint8_t binding_digest[32];
    uint8_t session_id[16];
    uint64_t generation;
    uint64_t policy_revision;
    uint64_t sequence;
    uint64_t issued_unix_ms;
    uint64_t expires_unix_ms;
    uint32_t capabilities;
    uint32_t consent_required;
} ng_remote_claims;

typedef int32_t (*ng_remote_verify_fn)(void* context, const uint8_t* ticket,
                                      size_t ticket_size, ng_remote_claims* claims);
typedef uint64_t (*ng_remote_clock_fn)(void* context);

typedef struct ng_remote_options {
    uint32_t struct_size;
    uint32_t abi_version;
    uint8_t binding_digest[32];
    uint32_t allowed_capabilities;
    uint32_t consent_mode;
    ng_remote_verify_fn verify;
    void* verify_context;
    /* Both NULL selects system wall/steady clocks. Custom clocks must be paired,
     * thread safe, nonthrowing and are intended for the host adapter and tests. */
    ng_remote_clock_fn unix_ms;
    ng_remote_clock_fn monotonic_ms;
    void* clock_context;
} ng_remote_options;

typedef struct ng_remote_snapshot {
    uint32_t struct_size;
    uint32_t abi_version;
    uint32_t state;
    uint32_t capabilities;
    int32_t terminal_reason;
    uint32_t reserved;
    uint64_t remaining_ms;
    uint64_t sequence;
    uint64_t policy_revision;
} ng_remote_snapshot;

typedef struct ng_remote_gate ng_remote_gate;

uint32_t ng_remote_abi_version(void);
int32_t ng_remote_gate_create(const ng_remote_options* options, ng_remote_gate** output);
/* Destruction requires the caller to join all concurrent calls first. */
void ng_remote_gate_destroy(ng_remote_gate* gate);
/* A gate handles one session only. After termination it cannot be reopened.
 * The host registry MUST atomically consume initial session IDs/ticket nonces
 * across gate objects and restarts; this object prevents replay only within its
 * own lifetime. API v2 supplies that registry and authenticated ticket decoder. */
int32_t ng_remote_gate_begin(ng_remote_gate* gate, const uint8_t* ticket, size_t size);
int32_t ng_remote_gate_consent(ng_remote_gate* gate, uint32_t allow);
int32_t ng_remote_gate_renew(ng_remote_gate* gate, const uint8_t* ticket, size_t size);
int32_t ng_remote_gate_revoke(ng_remote_gate* gate);
/* Check EVERY sensitive action. All bits in required_capabilities are required. */
int32_t ng_remote_gate_check(ng_remote_gate* gate, uint32_t required_capabilities);
/* The host must poll at <=100 ms even when idle, stop media/input on termination,
 * and release held keys on expiry/revocation or INPUT capability removal.
 * The library deliberately creates no background thread or network socket. */
int32_t ng_remote_gate_poll(ng_remote_gate* gate, ng_remote_snapshot* snapshot);

#ifdef __cplusplus
}
#endif
#endif
