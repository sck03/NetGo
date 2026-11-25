# Shared native remote viewer

`netgo_remote_client.h` is a pure C interface for Windows/GTK and the
Android/Apple bridges. The application supplies authenticated control IO,
its existing device signature, cancellation, input and platform decoders.
No private key, token, platform window or C++ container crosses the ABI.

The shared worker checks the user/host capability intersection, reconstructs
the signed binding, pins the target Ed25519 TLS key, proves possession with
the TLS exporter, and enforces a suspend-aware monotonic ticket deadline. It
anchors only to server_unix_ms in the authenticated authorization response and
charges the entire control-request duration. It never derives trusted time
from the media peer, ticket claims, or the local wall clock. next_generation in
the inventory supplies the next controller generation, including closed sessions.
Fresh authenticated renewals recalibrate server time without resetting an expired
monotonic deadline. The LAN server and clients must be updated together for these response fields.
The monitor
rechecks authorization every three seconds; failed control IO stops the
connection. Queues and wire messages are bounded. Cancel, join the caller's
worker, then destroy; callbacks cannot outlive run. Final DELETE cleanup uses a
separate bounded deadline after viewer cancellation, while the existing VPN
identity remains available.

Video payloads are Annex B H.264. Audio kind 10 carries at most 960 frames of
48 kHz stereo PCM S16LE (3840 bytes). Audio is requested only when both peers
and the grant support it. Input JSON supports key/button/move/wheel/release
and UTF-8 text. Clipboard and files are not implemented by this viewer.

The TLS test fixture exercises real sockets, key pinning and possession; its
six-byte video marker tests protocol dispatch and is not a media benchmark.

Input uses the shared thread-safe C/C++ queue (also used by Swift). It bounds both serialized bytes and event count, coalesces pointer movement, and discards pending actions on focus release, overload or a 1-second local backlog. Each NGR2 input message contains one validated event array; the host checks authorization before every event. Local expiry does not measure time already spent on the network. NGR1 is rejected; update hosts and viewers together. Cancellation closes its server session and reports normal disconnection.
