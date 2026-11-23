#pragma once

#include <cstdint>
#include <memory>
#include <string_view>

struct ID3D11Device;
struct ID3D11DeviceContext;
struct ID3D11Texture2D;

namespace netgo::capture {

enum class Result { frame, timeout, unavailable, unsupported, invalid, failed };

// Windows platform boundary, never exposed through the shared C ABI. All COM
// pointers are borrowed for the synchronous sink call, on the capture thread.
// An asynchronous encoder must copy into its own bounded GPU texture pool.
struct Surface {
    ID3D11Device* device{};
    ID3D11DeviceContext* context{};
    ID3D11Texture2D* texture{};
    std::uint32_t width{}, height{}; // unrotated BGRA texture dimensions
    std::int32_t desktop_left{}, desktop_top{};
    std::uint32_t clockwise_rotation{}; // 0, 90, 180, 270; apply when presenting
    std::int64_t present_counter{}; // QueryPerformanceCounter units, not UTC
    bool protected_content_masked{};
};

using Sink = void (*)(void*, const Surface&) noexcept;

// One instance and all its callbacks belong to one desktop/session thread.
// No background threads, sockets, pixel readback, cursor injection or input.
class DesktopCapture final {
public:
    DesktopCapture();
    ~DesktopCapture();
    DesktopCapture(const DesktopCapture&) = delete;
    DesktopCapture& operator=(const DesktopCapture&) = delete;

    // Empty selects the primary display. Reopening after access loss MUST use
    // the previously selected display_name, never silently change the target.
    Result open(std::wstring_view display_name = {}) noexcept;
    void close() noexcept;
    // timeout <=50 ms lets the host recheck its authorization lease frequently.
    // The host must check VIEW before acquisition AND inside the sink, and discard
    // queued frames when permission is revoked. This primitive is not a gate.
    Result next(Sink sink, void* context, std::uint32_t timeout_ms = 16) noexcept;
    std::int32_t last_error() const noexcept;
    std::wstring_view display_name() const noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

} // namespace netgo::capture
