# Linux native client

GTK 4.10+ interface with the existing per-user Go connection service and a
shared C ABI remote viewer. H.264 decoding uses dynamically linked FFmpeg;
control requests use only an owner-checked, mode 0600 Unix socket. Device
private keys and bearer tokens remain in the connection service.

From the repository root, run `scripts/build-linux-native.sh` with the pinned
nlohmann/json headers in `.tools/native-json`. Build prerequisites are CMake,
Ninja, a C++20 compiler, OpenSSL, GTK4, libcurl, libavcodec, libavutil and
libswscale development packages. Place `netgo-client-service` next to the UI;
the restricted NetBird daemon must already be installed for that user.

`xvfb-run -a netgo-desktop --smoke` verifies window creation and teardown.
It does not validate a physical desktop, VPN, Wayland portals, audio, or a
remote target. The Linux host adapter and system installer are still pending.
