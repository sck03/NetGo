#include "gpu_encoder.hpp"
#include "video_codec.hpp"
#include <windows.h>
#include <d3d11.h>
#include <wrl/client.h>
#include <thread>
#include <chrono>
#include <iostream>

using Microsoft::WRL::ComPtr;

int main() {
    try {
        ComPtr<ID3D11Device> device;
        ComPtr<ID3D11DeviceContext> context;
        D3D_FEATURE_LEVEL level;
        auto hr = D3D11CreateDevice(
            nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr,
            D3D11_CREATE_DEVICE_VIDEO_SUPPORT | D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            nullptr, 0, D3D11_SDK_VERSION, &device, &level, &context);
        if (FAILED(hr)) {
            std::cout << "UNAVAILABLE hardware D3D11 device " << std::hex << hr << "\n";
            return 2;
        }

        std::vector<uint32_t> pixels(1280 * 720);
        for (size_t i = 0; i < pixels.size(); ++i)
            pixels[i] = 0xff000000 | uint32_t(i % 256) << 16 | 0x7f;
        D3D11_TEXTURE2D_DESC d{};
        d.Width = 1280;
        d.Height = 720;
        d.MipLevels = d.ArraySize = 1;
        d.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        d.SampleDesc.Count = 1;
        d.Usage = D3D11_USAGE_DEFAULT;
        d.BindFlags = D3D11_BIND_RENDER_TARGET;
        D3D11_SUBRESOURCE_DATA data{pixels.data(), 1280 * 4, 0};
        ComPtr<ID3D11Texture2D> texture;
        if (FAILED(device->CreateTexture2D(&d, &data, &texture)))
            throw std::runtime_error("synthetic surface");

        netgo::capture::Surface source{};
        source.device = device.Get();
        source.context = context.Get();
        source.texture = texture.Get();
        source.width = 1280;
        source.height = 720;
        netgo::video::GpuEncoder encoder(source, 1280, 720, 20, 3000000);
        netgo::video::Codec decoder(netgo::video::Codec::Mode::decode, 1280, 720, 20);
        size_t packets = 0, frames = 0, bytes = 0;
        auto decode = [&](std::vector<netgo::video::Packet> values) {
            for (auto& p : values) {
                ++packets;
                bytes += p.h264.size();
                frames += decoder.decode(p).size();
            }
        };
        for (int i = 0; i < 40; ++i) {
            decode(encoder.encode(source, i * 500000ll));
            std::this_thread::sleep_for(std::chrono::milliseconds(50));
        }
        for (int i = 0; i < 50; ++i) {
            decode(encoder.drain());
            std::this_thread::sleep_for(std::chrono::milliseconds(5));
        }
        std::cout << "Synthetic GPU BGRA -> NV12 -> hardware H264 -> decoder: packets="
                  << packets << " frames=" << frames << " bytes=" << bytes
                  << "; no screen capture\n";
        return frames >= 30 ? 0 : 1;
    } catch (const std::exception& e) {
        std::cerr << "GPU probe: " << e.what() << "\n";
        return 1;
    }
}
