#include "netgo_remote_ticket.h"
#include <string.h>

int main(void) {
    ng_remote_ticket_verifier* verifier = NULL;
    uint8_t key[32] = {1}, wire[NG_REMOTE_TICKET_SIZE] = {0}, digest[32] = {1};
    ng_remote_claims claims = {0};
    ng_remote_ticket_binding binding = {0};
    ng_remote_options options = {0};
    /* This assignment must compile as C11, with no C++ wrappers or casts. */
    options.verify = ng_remote_ticket_verify;
    if (ng_remote_ticket_verifier_create(key, sizeof(key), &verifier) != NG_REMOTE_OK || !verifier) return 1;
    options.verify_context = verifier;
    claims.struct_size = sizeof(claims);
    claims.abi_version = NG_REMOTE_ABI;
    claims.capabilities = NG_REMOTE_ALL_CAPABILITIES;
    if (options.verify(options.verify_context, wire, sizeof(wire), &claims) != NG_REMOTE_INVALID_TICKET ||
        claims.capabilities != 0) return 2;
    binding.struct_size = sizeof(binding);
    binding.abi_version = NG_REMOTE_ABI;
    if (ng_remote_ticket_binding_digest(&binding, digest) != NG_REMOTE_INVALID_ARGUMENT || digest[0]) return 3;
    ng_remote_ticket_verifier_destroy(verifier);
    ng_remote_ticket_verifier_destroy(NULL);
    return 0;
}
