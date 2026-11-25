# Windows remote desktop modules

The single NetGo.exe links the interactive host and viewer, shared authorization
and TLS transport, DXGI capture, Media Foundation H.264 and WASAPI audio.
The platform adapter only owns capture, encoding, input, clipboard and UI.
The shared host in packages/remote-client owns authorization, consent, revocation,
file exchange and the listener bound to the authenticated overlay address.

The primary pipeline is a borrowed Desktop Duplication BGRA surface, D3D11 video
processor conversion to a fresh NV12 GPU surface, then a D3D11-aware hardware MFT.
At most four submitted frames remain pending. Each sample owns its texture until
the asynchronous encoder releases it. Hardware initialization failure selects an
explicit software fallback with a reused CPU staging texture. Runtime codec or
capture failure closes the session; it never silently drops reference frames.
The current profile preserves aspect ratio up to 1280x720 at 20 fps.

Per-session local consent is the default. Explicit unattended mode uses saved
Credential Manager credentials and a Windows login startup entry, performs a fresh
VPN login, then reopens sharing. It requires a logged-in interactive desktop.
Stop or closing the remote window disables automatic sharing; minimizing keeps
it active. Pre-login/UAC/secure-desktop control is not implemented.

Clipboard is explicit UTF-8 text, at most 8192 bytes. File exchange uses the local
Exchange directory, a basename-only protocol, 32 KiB acknowledged chunks, checked
temporary-file publication and no overwrite. The same files/exchange modules serve
all native clients. View-only never grants clipboard or files implicitly; each
capability must be independently granted by the administrator.

The viewer preserves server host status, supports view-only/audio selection,
F11 fullscreen and letterboxed input mapping. File/clipboard operations are in
the toolbar menu. An inactive viewer's file menu opens the local exchange folder.

Run ctest --test-dir .cache/native-build --output-on-failure after a native build.
The synthetic codec test and remote-host TLS integration do not capture a real
desktop. netgo-video-probe --run explicitly captures the current desktop and must
be run deliberately for device acceptance. No real-GPU throughput claim follows
from compilation or the software codec probe.

See docs/REMOTE_HOST_FEATURES.zh-CN.md for scope and acceptance limits.
