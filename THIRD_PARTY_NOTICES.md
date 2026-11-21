# Third-party components

Windows product UI and controller are C++20 / Win32. Wails is no longer a product dependency. Exact inputs are in upstream/versions.lock.json, go.sum and package-lock.json.

| Component | Use | License/material |
| --- | --- | --- |
| NetBird 0.83.2 | Separate Windows network engine and Linux server components | BSD-3-Clause for client; AGPL-3.0 for management/signal/relay/combined; preserve directory licenses and patches |
| NetBird Dex fork | Linux embedded identity | Apache-2.0; pinned source and patch |
| OpenSSL 4.1.0 | Native TLS, hashing, Ed25519 | Apache-2.0; statically linked, source revision/build flags provided |
| nlohmann/json 3.12.0 | Native JSON | MIT; fixed header SHA-256 |
| Wintun 0.14.1 | Windows tunnel interface used by NetBird | Official signed binary; accompanying upstream LICENSE.txt and README |
| GCC runtime | Static C++ runtime | GPL with GCC Runtime Library Exception; Windows UCRT is supplied by Windows 11 |
| Vue/Vite/TypeScript | Linux administration UI build | MIT; exact npm dependencies in lock file |
| frp/Caddy | Optional control forwarding / TLS gateway | Apache-2.0 |
| HAProxy | Optional Linux TCP 443 multiplexing | Upstream component licenses; installed package version must be recorded |
| Go modules | Product server and upstream engine build | Individual module licenses; versions/checksums in Go lock files |
| Python cryptography | Tests only | Apache-2.0 / BSD-3-Clause; not shipped in the runtime package |

ocserv-openwrt is GPL-3.0-or-later. Its public interaction patterns were inspected; its OpenConnect implementation was not copied. This notice does not relicense upstream source. Wintun source is GPL-licensed; its official signed prebuilt DLL has a separate license permitting distribution with software using the documented API, which is preserved alongside the DLL. A public server distribution must include matching source and dependency materials. Network release acceptance remains pending.
