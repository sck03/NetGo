# Windows desktop capture adapter

Independent C++17 DXGI Desktop Duplication adapter. Links only to Windows system
APIs (`d3d11`, `dxgi`, `user32`) and the C++ runtime. It is not linked into the VPN
UI or agent and is not a remote desktop product yet.

The adapter borrows one GPU BGRA surface during a synchronous, nonthrowing sink
call. It never queues frames, creates threads, copies pixels to the CPU, connects
to a network or grants access. A hardware encoder must copy into its own bounded
GPU texture pool before the sink returns. Capture threads and objects are scoped
to one user's desktop session. Use a dedicated thread; do not destroy or reenter
the object from its sink. COM pointers must not escape the callback.

`open()` selects the primary monitor or an exact DXGI display name. `next()` has a
maximum 50 ms wait; timeout is normal. Access loss, disconnect or device reset
releases all device resources and reports `unavailable`. The host must stop the
stream, revalidate authorization and reopen the **same named display**. Never
silently capture another monitor. `Surface` provides unrotated texture dimensions,
desktop origin and clockwise display rotation; the renderer/encoder must apply
the transform. The adapter rejects non-BGRA, array and oversized surfaces.

The host must check the remote-core VIEW gate before `next()` and again in its
sink, poll leases while idle, cancel encoders, and discard queued frames on
revocation. This low-level adapter is deliberately independent of the gate.

From the repository root with the existing MinGW toolchain on PATH:

```powershell
cmake -S apps/windows/remote -B .cache/capture-windows -G Ninja -DCMAKE_BUILD_TYPE=Release -DNETGO_CAPTURE_PROBE=ON
cmake --build .cache/capture-windows
& .cache/capture-windows/netgo-capture-probe.exe --check
& .cache/capture-windows/netgo-capture-probe.exe --run
```

`--check` does not capture pixels. `--run` is an explicit interactive desktop
probe: acquires a real frame, performs bounded CPU readback, closes and reopens
the same display three times. It reports dimensions/byte counts only; no image,
pixel hash or screenshot is saved. CPU readback belongs only to this test.

Still pending: cursor shape/composition, dirty/move rectangles, real hardware
encoding, viewer rendering/rotation, audio, input, signed-ticket host integration,
lock/UAC/user switch/sleep/multi-monitor/Windows 11 tests and streaming benchmarks.
No secure-desktop bypass, RDP-session compatibility or headless capability is
claimed. The smoke probe is intentionally not an unattended CTest.

API references (Microsoft Learn): Desktop Duplication API; IDXGIOutput1::DuplicateOutput;
IDXGIOutputDuplication::AcquireNextFrame and ReleaseFrame. Windows SDK/MinGW system
headers are used directly; no third-party capture code is copied.
