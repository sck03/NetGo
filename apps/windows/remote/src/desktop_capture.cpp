#include "desktop_capture.hpp"

#include <windows.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <wrl/client.h>
#include <string>

namespace netgo::capture {
using Microsoft::WRL::ComPtr;

namespace {
Result classify(HRESULT error) noexcept {
    if (error == DXGI_ERROR_WAIT_TIMEOUT)
        return Result::timeout;
    if (error == E_INVALIDARG || error == DXGI_ERROR_NOT_FOUND)
        return Result::invalid;
    if (error == DXGI_ERROR_UNSUPPORTED || error == E_NOINTERFACE || error == E_NOTIMPL)
        return Result::unsupported;
    if (error == E_ACCESSDENIED || error == DXGI_ERROR_ACCESS_LOST ||
        error == DXGI_ERROR_SESSION_DISCONNECTED || error == DXGI_ERROR_NOT_CURRENTLY_AVAILABLE ||
        error == DXGI_ERROR_DEVICE_REMOVED || error == DXGI_ERROR_DEVICE_RESET)
        return Result::unavailable;
    return Result::failed;
}

std::uint32_t rotation(DXGI_MODE_ROTATION value) noexcept {
    switch (value) {
    case DXGI_MODE_ROTATION_ROTATE90:
        return 90;
    case DXGI_MODE_ROTATION_ROTATE180:
        return 180;
    case DXGI_MODE_ROTATION_ROTATE270:
        return 270;
    default:
        return 0;
    }
}
} // namespace

struct DesktopCapture::Impl {
    ComPtr<ID3D11Device> device;
    ComPtr<ID3D11DeviceContext> context;
    ComPtr<IDXGIOutputDuplication> duplication;
    DXGI_OUTPUT_DESC output{};
    DXGI_OUTDUPL_DESC description{};
    std::wstring name;
    HRESULT error{S_OK};
    bool in_callback{};

    void release() noexcept {
        duplication.Reset();
        context.Reset();
        device.Reset();
        output = {};
        description = {};
    }
    Result fail(HRESULT value) noexcept {
        error = value;
        release();
        return classify(value);
    }
};

DesktopCapture::DesktopCapture() : impl_(std::make_unique<Impl>()) {}
DesktopCapture::~DesktopCapture() = default;

Result DesktopCapture::open(std::wstring_view name) noexcept {
    auto &state = *impl_;
    if (state.in_callback)
        return Result::invalid;
    try {
        // Copy first: callers may pass the display_name() view from this object.
        const std::wstring wanted(name);
        state.release();
        state.name.clear();
        ComPtr<IDXGIFactory1> factory;
        HRESULT hr =
            CreateDXGIFactory1(__uuidof(IDXGIFactory1), reinterpret_cast<void **>(factory.GetAddressOf()));
        if (FAILED(hr))
            return state.fail(hr);
        const HMONITOR primary = MonitorFromPoint(POINT{0, 0}, MONITOR_DEFAULTTOPRIMARY);
        for (UINT adapter_index = 0;; ++adapter_index) {
            ComPtr<IDXGIAdapter1> adapter;
            hr = factory->EnumAdapters1(adapter_index, adapter.GetAddressOf());
            if (hr == DXGI_ERROR_NOT_FOUND)
                break;
            if (FAILED(hr))
                return state.fail(hr);
            for (UINT output_index = 0;; ++output_index) {
                ComPtr<IDXGIOutput> output;
                hr = adapter->EnumOutputs(output_index, output.GetAddressOf());
                if (hr == DXGI_ERROR_NOT_FOUND)
                    break;
                if (FAILED(hr))
                    return state.fail(hr);
                DXGI_OUTPUT_DESC description{};
                hr = output->GetDesc(&description);
                if (FAILED(hr))
                    return state.fail(hr);
                if (!description.AttachedToDesktop ||
                    (wanted.empty() ? description.Monitor != primary : wanted != description.DeviceName))
                    continue;
                state.name = description.DeviceName;
                state.output = description;
                constexpr D3D_FEATURE_LEVEL levels[] = {D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1,
                                                        D3D_FEATURE_LEVEL_10_0};
                hr = D3D11CreateDevice(adapter.Get(), D3D_DRIVER_TYPE_UNKNOWN, nullptr,
                                       D3D11_CREATE_DEVICE_BGRA_SUPPORT | D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                                       levels, ARRAYSIZE(levels), D3D11_SDK_VERSION,
                                       state.device.GetAddressOf(), nullptr, state.context.GetAddressOf());
                if (FAILED(hr))
                    return state.fail(hr);
                ComPtr<IDXGIOutput1> desktop;
                hr = output.As(&desktop);
                if (FAILED(hr))
                    return state.fail(hr);
                hr = desktop->DuplicateOutput(state.device.Get(), state.duplication.GetAddressOf());
                if (FAILED(hr))
                    return state.fail(hr);
                state.duplication->GetDesc(&state.description);
                if (state.description.ModeDesc.Format != DXGI_FORMAT_B8G8R8A8_UNORM)
                    return state.fail(DXGI_ERROR_UNSUPPORTED);
                state.error = S_OK;
                return Result::frame;
            }
        }
        return state.fail(DXGI_ERROR_NOT_FOUND);
    } catch (...) {
        return state.fail(E_OUTOFMEMORY);
    }
}

void DesktopCapture::close() noexcept {
    if (!impl_->in_callback)
        impl_->release();
}

Result DesktopCapture::next(Sink sink, void *context, std::uint32_t timeout_ms) noexcept {
    auto &state = *impl_;
    if (!sink || timeout_ms > 50 || state.in_callback) {
        state.error = E_INVALIDARG;
        return Result::invalid;
    }
    if (!state.duplication) {
        state.error = DXGI_ERROR_ACCESS_LOST;
        return Result::unavailable;
    }
    DXGI_OUTDUPL_FRAME_INFO info{};
    ComPtr<IDXGIResource> resource;
    HRESULT hr = state.duplication->AcquireNextFrame(timeout_ms, &info, resource.GetAddressOf());
    if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
        state.error = hr;
        return Result::timeout;
    }
    if (FAILED(hr))
        return state.fail(hr);
    // Every acquired frame has exactly one ReleaseFrame, including conversion
    // failures. The sink is noexcept and may not destroy or reenter this object.
    ComPtr<ID3D11Texture2D> texture;
    hr = resource.As(&texture);
    if (SUCCEEDED(hr)) {
        D3D11_TEXTURE2D_DESC descriptor{};
        texture->GetDesc(&descriptor);
        if (!descriptor.Width || !descriptor.Height || descriptor.Width > 8192 || descriptor.Height > 8192 ||
            descriptor.Format != DXGI_FORMAT_B8G8R8A8_UNORM || descriptor.SampleDesc.Count != 1 ||
            descriptor.ArraySize != 1) {
            hr = DXGI_ERROR_UNSUPPORTED;
        } else {
            Surface frame{state.device.Get(),
                          state.context.Get(),
                          texture.Get(),
                          descriptor.Width,
                          descriptor.Height,
                          state.output.DesktopCoordinates.left,
                          state.output.DesktopCoordinates.top,
                          rotation(state.description.Rotation),
                          info.LastPresentTime.QuadPart,
                          info.ProtectedContentMaskedOut != FALSE};
            state.in_callback = true;
            sink(context, frame);
            state.in_callback = false;
        }
    }
    texture.Reset();
    resource.Reset();
    const HRESULT released = state.duplication->ReleaseFrame();
    if (FAILED(hr))
        return state.fail(hr);
    if (FAILED(released))
        return state.fail(released);
    state.error = S_OK;
    return Result::frame;
}

std::int32_t DesktopCapture::last_error() const noexcept { return impl_->error; }
std::wstring_view DesktopCapture::display_name() const noexcept { return impl_->name; }

} // namespace netgo::capture
