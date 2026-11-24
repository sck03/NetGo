# Private remote transport adapter

TLS 1.3 over the authenticated numeric overlay address, with an Ed25519 host-key
pin from the control plane and exporter binding for device possession. No DNS,
public relay, extra account system or media authentication fallback is added.

Each connection belongs to one worker. Frames use a 12-byte NGR1 header; media
is limited to 4 MiB and control messages to 64 KiB. receive returns false without
changing the caller's message when a fragment is incomplete. It retains partial
reads and gives the entire message one 3-second deadline across polls, so media
and input loops keep running. Writes share one deadline for header and body;
permission/cancellation checks occur at least every 100 ms while waiting.

TCP_NODELAY reduces small input-message buffering. POSIX uses poll rather than
fixed-size fd_set. Linux suppresses newly generated SIGPIPE only within each
thread's TLS operation and restores the caller's signal mask. Apple uses the
socket's SO_NOSIGPIPE option. A peer disconnect reports a transport error.

The authorization timer includes suspend time: GetTickCount64 on Windows,
CLOCK_BOOTTIME on Linux/Android, mach_continuous_time on Apple. The platform
implementation is confined to this adapter; the gate's C ABI exposes no SDKs.

Real TLS tests cover pinning, exporter binding, cancellation, incomplete headers
and bodies, empty/back-to-back messages, slow fragments, a closed peer and POSIX
descriptors above 1024. Shared client tests add possession, view-only dispatch,
server clock offsets and cleanup. A protocol fixture is not a media benchmark.
