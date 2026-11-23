# NetGo signed remote tickets

This optional C++17 adapter verifies the fixed Ed25519 protocol in
[`REMOTE_TICKET.zh-CN.md`](../../docs/REMOTE_TICKET.zh-CN.md). Its pure C ABI can
be passed directly to `ng_remote_options.verify`. The Go issuer lives in
`internal/remote/ticket`; both implementations use the same public test vectors.

Only OpenSSL **Crypto** is linked here, using the existing native toolchain.
`remote-core` stays independent of cryptographic libraries, networking and OS
media. This target is not linked into the VPN desktop or agent binaries. Each
platform may supply another verifier through the same gate callback, provided it
passes these vectors and enforces the same canonical format. Do not replace
signature verification with a custom cryptographic implementation.

The caller supplies the pinned site public key and reconstructs the binding from
authenticated site/user/controller/host/boot state. A successful signature does
not establish controller key possession, current authorization or one-time use.
The host coordinator must implement those checks and a replay registry before
opening any media or input channel. The adapter deliberately opens no sockets
and stores no keys, sessions or tickets. Never log a live ticket.

Build the standalone CMake project with OpenSSL development files. On Windows,
the same pinned `.tools/openssl-native` installation as the VPN agent is required
(`NETGO_OPENSSL_ROOT` can select its location); missing inputs fail without
falling back to MSYS libraries. `ctest --test-dir <build> -V`
checks Go-generated signatures, authenticated malformed fields, every-byte
mutation/truncation, binding boundaries, concurrent verification, lease/consent/
revocation behavior, and C11 linkage. `scripts/verify-remote.py --output <new-dir>`
also runs the dependency-free gate tests, Linux ASan/UBSan and Windows capture
compile checks. On Linux it uses `pkg-config libcrypto`; extracted development
packages can be selected through `PKG_CONFIG_SYSROOT_DIR` and
`PKG_CONFIG_LIBDIR` without installing them globally.

Test vector regeneration requires explicit review of the protocol and
`go test ./internal/remote/ticket -update-ticket-vectors`. The deterministic seed
in the tests is public and must never be used for deployed keys.
