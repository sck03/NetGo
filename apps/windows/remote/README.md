# Windows remote desktop modules

The single NetGo.exe links the interactive host and viewer, shared authorization
and TLS transport, DXGI capture, Media Foundation H.264 and WASAPI audio. Users
open NetGo.exe; these modules are libraries, not separate daily programs.

The capture adapter remains independent of authentication and networking. It
borrows a GPU BGRA surface only during its synchronous callback. The host checks
VIEW authorization before capture and before encoding/sending, keeps one CPU
staging texture, and releases pressed keys/buttons when a session ends. Only the
authenticated overlay address is listened on. Each connection requires local
consent; a 15-second unanswered prompt rejects the connection.

Video keeps the desktop's aspect ratio, rounds to even NV12 dimensions and never
upscales. The software profile is at most 1280×720, 20 fps, with a 3 Mbps target
bitrate. Capture and codec initialization must succeed before the host sends the
accepted format. The viewer uses letterboxing and maps input only to that image.
Black bars and toolbar wheel events are excluded. Stop ends sharing/viewing
without closing the window; only-view permissions preserve local shortcuts.

The authenticated response supplies server_unix_ms. Ticket deadlines use that
time plus a suspend-aware monotonic timer; media peers and the local wall clock
cannot supply or extend the time. Server next_generation fences each new
controller session, including reconnects after old sessions close. Update the
LAN server and clients together for these added control-response fields.

From the repository root after a native build:

```powershell
ctest --test-dir .cache/native-build --output-on-failure
& .cache/native-build/remote/netgo-video-probe.exe --check
& .cache/native-build/remote/netgo-video-probe.exe --run
```

--check verifies synthetic 720p input through the actual system H.264 codec.
--run explicitly captures the current desktop and verifies its encode/decode
path. Neither saves a screen image. These tests do not prove cross-device VPN
media, audio, input, UAC, lock screen or hardware performance.

Access loss, protected/rotated displays and changed stream dimensions stop the
session. No automatic switch to another monitor, secure-desktop bypass, file
transfer or clipboard capability is advertised. Cursor composition, hardware
encoding, multi-monitor/rotation, sleep, Win11 and full device tests remain in
the remote desktop plan. Public DXGI/MF/WASAPI system APIs are used; no new
third-party media engine is bundled.
