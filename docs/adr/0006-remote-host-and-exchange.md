# 0006 — Shared remote hosts, GPU media and explicit desktop exchange

Date: 2026-11-26. Status: implemented; physical-device acceptance pending.

The existing VPN identity, signed short-lived ticket, TLS pin and exporter possession proof remain the authority. A shared host executor owns registration, consent, consumption, renewals and cancellation. Native adapters own OS resources, and never receive the device private key. A fresh transport key is registered on each host start. The viewer ABI is 2; the host ABI is 1. Old registration payloads without transport_key are rejected.

GPU encoding follows the surface-based design used by mature streaming systems. Windows passes D3D11 NV12 surfaces to an asynchronous hardware MFT; macOS passes ScreenCaptureKit pixel buffers to VideoToolbox. Resource queues have hard limits and preserve reference-frame ordering. Linux delegates codec negotiation to installed GStreamer plugins; zero-copy negotiation across every Linux driver is not claimed.

Unattended access is an explicit local setting and fresh authenticated login in a logged-in user session. Local opt-in does not replace administrator grants or system consent. The implementation does not impersonate a login-screen service or evade TCC/portal restrictions. Linux portal selection runs before host registration and uses the portal's rotating restore token when persistence is allowed.

Clipboard uses bounded, explicit text operations. File exchange uses a dedicated directory, basename-only paths, native no-follow handles, sequential acknowledged chunks and checked publication without overwrite. All platforms use the same protocol and filesystem module. A single outstanding chunk provides simple backpressure; the tradeoff is lower throughput on high-RTT links. Clipboard and transfer contents are not logged.

## References checked

Public documentation and architecture were examined; no RustDesk, Sunshine, TeamViewer or proprietary implementation source was incorporated. Reference licenses do not become NetGo dependencies merely through design study. Actual linked/distributed dependencies remain listed in THIRD_PARTY_NOTICES.md.

- RustDesk: [source](https://github.com/rustdesk/rustdesk), [AGPL license](https://github.com/rustdesk/rustdesk/blob/master/LICENCE). Reference for separating remote identity, session permissions and platform adapters.
- Sunshine: [source](https://github.com/SunshineStream/Sunshine), [Windows VRAM capture backend](https://github.com/SunshineStream/Sunshine/blob/master/src/platform/windows/display_vram.cpp). Reference for GPU surface paths and platform-specific encoders; GPL implementation was not copied.
- TeamViewer: [unattended remote support](https://www.teamviewer.com/en/global/support/knowledge-base/teamviewer-remote/remote-control/provide-unattended-remote-support/). Product reference for explicit unattended setup and account-controlled access.
- Microsoft: [hardware MFTs](https://learn.microsoft.com/en-us/windows/win32/medfound/hardware-mfts), [Direct3D 11 video](https://learn.microsoft.com/en-us/windows/win32/medfound/supporting-direct3d-11-video-decoding-in-media-foundation). Native implementation contracts.
- Apple: [ScreenCaptureKit](https://developer.apple.com/documentation/screencapturekit), [VideoToolbox](https://developer.apple.com/documentation/videotoolbox), [SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice). System APIs and permission ownership.
- freedesktop: [RemoteDesktop portal specification](https://github.com/flatpak/xdg-desktop-portal/blob/main/data/org.freedesktop.portal.RemoteDesktop.xml). Session persistence is exclusively managed by RemoteDesktop.SelectDevices for combined capture/control sessions. A restore token is consumed once and replaced by Start.

## Deferred and measurable limits

No pre-login/UAC secure-desktop host, iOS host, arbitrary filesystem access, rich clipboard, independent transfer QoS, multi-monitor switching or automatic adaptive bitrate is claimed. Linux/macOS audio hosting remains disabled. Tests inject media to verify the real protocol; actual hardware and desktop interaction require the acceptance matrix in REMOTE_HOST_FEATURES.zh-CN.md.
