#include "desktop_capture.hpp"

#include <windows.h>
#include <d3d11.h>
#include <wrl/client.h>
#include <chrono>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <string>

using netgo::capture::DesktopCapture;
using netgo::capture::Result;
using netgo::capture::Surface;
using Microsoft::WRL::ComPtr;

namespace {
struct Probe {
    bool readback_ok{};
    std::uint32_t width{}, height{}, rotation{};
    std::uint64_t bytes{};
    HRESULT error{S_OK};
};

void readback(void* context, const Surface& frame) noexcept {
    auto& probe = *static_cast<Probe*>(context);
    probe.width = frame.width;
    probe.height = frame.height;
    probe.rotation = frame.clockwise_rotation;
    probe.bytes = static_cast<std::uint64_t>(frame.width) * frame.height * 4;
    if (probe.bytes > 64 * 1024 * 1024) { probe.error = E_OUTOFMEMORY; return; }
    D3D11_TEXTURE2D_DESC descriptor{};
    frame.texture->GetDesc(&descriptor);
    descriptor.BindFlags = 0;
    descriptor.MiscFlags = 0;
    descriptor.Usage = D3D11_USAGE_STAGING;
    descriptor.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
    ComPtr<ID3D11Texture2D> staging;
    probe.error = frame.device->CreateTexture2D(&descriptor, nullptr, staging.GetAddressOf());
    if (FAILED(probe.error)) return;
    frame.context->CopyResource(staging.Get(), frame.texture);
    D3D11_MAPPED_SUBRESOURCE mapped{};
    probe.error = frame.context->Map(staging.Get(), 0, D3D11_MAP_READ, 0, &mapped);
    if (FAILED(probe.error)) return;
    probe.readback_ok = mapped.pData != nullptr && mapped.RowPitch >= frame.width * 4;
    // Touch each row to verify actual CPU readback. Never log, retain or write
    // pixel content; production hardware encoders consume GPU surfaces instead.
    if (probe.readback_ok) {
        const auto* pixels = static_cast<const std::uint8_t*>(mapped.pData);
        volatile std::uint8_t sample{};
        for (std::uint32_t y = 0; y < frame.height; ++y) sample = pixels[static_cast<std::size_t>(y) * mapped.RowPitch];
        (void)sample;
    }
    frame.context->Unmap(staging.Get(), 0);
}

bool boundaries() {
    DesktopCapture capture;
    Probe probe;
    if (capture.next(nullptr, nullptr) != Result::invalid || capture.next(readback, &probe, 51) != Result::invalid ||
        capture.next(readback, &probe, 0) != Result::unavailable) return false;
    capture.close();
    capture.close();
    if (capture.open(L"NetGo-nonexistent-display") != Result::invalid) return false;
    return !probe.readback_ok;
}
} // namespace

int main(int argc, char** argv) {
    const bool run = argc == 2 && std::strcmp(argv[1], "--run") == 0;
    if (!run && !(argc == 2 && std::strcmp(argv[1], "--check") == 0)) {
        std::cerr << "Usage: netgo-capture-probe --check | --run (interactive capture; metadata only)\n";
        return 2;
    }
    try {
        if (!boundaries()) { std::cerr << "FAIL boundary checks\n"; return 1; }
        std::cout << "PASS closed/invalid/deadline/missing-display/idempotent-close boundaries\n";
        if (!run) return 0;
        DesktopCapture capture;
        std::wstring selected;
        for (int cycle = 0; cycle < 3; ++cycle) {
            if (capture.open(selected) != Result::frame) {
                std::cerr << "UNAVAILABLE desktop HRESULT=0x" << std::hex << static_cast<std::uint32_t>(capture.last_error()) << '\n';
                return 3;
            }
            selected = capture.display_name();
            Probe probe;
            const auto start = std::chrono::steady_clock::now();
            Result result{};
            do {
                result = capture.next(readback, &probe, 50);
            } while (result == Result::timeout && std::chrono::steady_clock::now() - start < std::chrono::seconds(5));
            if (result != Result::frame || !probe.readback_ok) {
                std::cerr << "FAIL readback HRESULT=0x" << std::hex << static_cast<std::uint32_t>(capture.last_error())
                          << " map=0x" << static_cast<std::uint32_t>(probe.error) << '\n';
                return 1;
            }
            capture.close();
            std::cout << "PASS capture-close-reopen cycle=" << cycle + 1 << " width=" << probe.width << " height=" << probe.height
                      << " clockwise_rotation=" << probe.rotation << " readback_bytes=" << probe.bytes << '\n';
        }
        std::cout << "PASS GPU capture with bounded CPU readback; no media/input/network/session authorization tested\n";
        return 0;
    } catch (const std::exception&) {
        std::cerr << "FAIL capture probe exception\n";
        return 1;
    }
}
