# Shared native remote viewer

`netgo_remote_client.h` is a pure C interface for the Windows/GTK viewers and
future JNI/ObjC bridges. The application supplies authenticated control IO,
its existing device signature, cancellation, input and platform decoders.
No private key, token, platform window or C++ container crosses the ABI.

The shared worker checks the user/host capability intersection, reconstructs
the signed binding, pins the target Ed25519 TLS key, proves possession with
the TLS exporter, and enforces a local monotonic ticket deadline. The monitor
rechecks authorization every three seconds; failed control IO stops the
connection. Queues and wire messages are bounded. Cancel, join the caller's
worker, then destroy; callbacks cannot outlive run.

Video payloads are Annex B H.264. Audio kind 10 carries at most 960 frames of
48 kHz stereo PCM S16LE (3840 bytes). Audio is requested only when both peers
and the grant support it. Input JSON supports key/button/move/wheel/release
and UTF-8 text. Clipboard and files are not implemented by this viewer.

The TLS test fixture exercises real sockets, key pinning and possession; its
six-byte video marker tests protocol dispatch and is not a media benchmark.
