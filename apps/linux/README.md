# Linux native client

GTK 4.10+ UI, the per-user Go connection service, shared C ABI host/viewer and
bounded clipboard/file exchange. Device keys and login secrets remain in the
connection service; user IPC uses an owner-checked mode 0600 Unix socket.

Host capture uses X11/ximagesrc with XTest, or a combined Wayland RemoteDesktop /
ScreenCast portal session and PipeWire. System selection happens before host
registration. Input capability reflects the actual portal grant. Persistent
restore tokens are used only for explicit unattended opt-in; compositor policy
still controls restoration. No arbitrary Wayland input or permission bypass is
attempted. Linux audio hosting is not advertised. Viewer audio playback is optional and uses the existing GStreamer stack; it is negotiated only when the remote host supports AUDIO. The appsrc queue is capped at 19,200 bytes and playback errors stop the session. Disable the playback checkbox if the desktop has no working audio sink.

H.264 encoding tries GStreamer VA, NVIDIA and then x264. Distribution plugins,
GPU drivers and actual negotiated buffer memory decide hardware availability;
this implementation does not claim Linux end-to-end zero-copy. Decoding uses
FFmpeg. All frame, encoded-packet and file-command queues are bounded.

Build with scripts/build-linux-client.sh (or build-linux-native.sh for native
code only). In addition to CMake/Ninja/C++20, GTK4, OpenSSL, curl and FFmpeg,
install GStreamer core/base development headers, gio-unix, X11 and XTest.
For Ubuntu 24.04 the runtime host dependencies include:

    sudo apt install libgtk-4-1 libcurl4 libavcodec60 libavutil58 libswscale7 \
      libx11-6 libxtst6 gstreamer1.0-plugins-base gstreamer1.0-plugins-good gstreamer1.0-plugins-bad \
      gstreamer1.0-plugins-ugly gstreamer1.0-pipewire

Install the desktop's matching xdg-desktop-portal backend for Wayland. Hardware
codec availability can be inspected with gst-inspect-1.0 vah264enc / nvh264enc;
x264enc is the software fallback. Package names vary by distribution.

For unattended login recovery, opt in before a successful VPN login. The existing
vault stores the authenticated credentials in mode 0600 files under the user
configuration directory. XDG autostart restores the application at user login;
a fresh authenticated VPN session then starts sharing. Disable the option to
clear the stored login and remove autostart. Explicit stop/disconnect pauses
automatic recovery until a later deliberate action or app restart.

The file exchange root is the user NetGo/Exchange directory. Uploaded files do
not overwrite existing files and are only published after integrity verification.

xvfb-run -a netgo-desktop --smoke verifies window construction and teardown only.
It does not validate a physical desktop, portal, hardware codec or remote VPN.
See docs/REMOTE_HOST_FEATURES.zh-CN.md for exact feature and device-test scope.
