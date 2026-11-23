#include "netgo_remote.h"
#include <string.h>

int main(void) {
    ng_remote_gate* gate = NULL;
    ng_remote_options options;
    memset(&options, 0, sizeof(options));
    options.struct_size = sizeof(options);
    options.abi_version = NG_REMOTE_ABI;
    /* A missing authenticated verifier must fail closed, even from a C caller. */
    if (ng_remote_gate_create(&options, &gate) != NG_REMOTE_INVALID_ARGUMENT || gate)
        return 1;
    options.abi_version++;
    if (ng_remote_gate_create(&options, &gate) != NG_REMOTE_ABI_MISMATCH || gate)
        return 2;
    ng_remote_gate_destroy(NULL);
    return ng_remote_abi_version() == NG_REMOTE_ABI ? 0 : 3;
}
