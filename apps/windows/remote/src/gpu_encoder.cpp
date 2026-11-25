#include "gpu_encoder.hpp"
#include <windows.h>
#include <d3d11.h>
#include <d3d10.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mferror.h>
#include <mftransform.h>
#include <wrl/client.h>
#include <stdexcept>
#include <cstdio>
#include <chrono>
#include <thread>
using Microsoft::WRL::ComPtr;
namespace netgo::video {
namespace {
void check(HRESULT hr, const char *op) {
    if (FAILED(hr)) {
        char text[160];
        std::snprintf(text, sizeof(text), "%s HRESULT=0x%08lx", op, static_cast<unsigned long>(hr));
        throw std::runtime_error(text);
    }
}
ComPtr<IMFMediaType> media(GUID format, uint32_t w, uint32_t h, uint32_t fps) {
    ComPtr<IMFMediaType> t;
    check(MFCreateMediaType(&t), "gpu type");
    check(t->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video), "gpu major");
    check(t->SetGUID(MF_MT_SUBTYPE, format), "gpu subtype");
    t->SetUINT64(MF_MT_FRAME_SIZE, (UINT64(w) << 32) | h);
    t->SetUINT64(MF_MT_FRAME_RATE, (UINT64(fps) << 32) | 1);
    t->SetUINT64(MF_MT_PIXEL_ASPECT_RATIO, (UINT64(1) << 32) | 1);
    t->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
    return t;
}
} // namespace
struct GpuEncoder::Impl {
    uint32_t width, height, fps, sourceWidth, sourceHeight;
    unsigned need = 0, outstanding = 0;
    bool com = false, mf = false;
    ComPtr<ID3D11Device> device;
    ComPtr<ID3D11VideoDevice> video;
    ComPtr<ID3D11VideoContext> context;
    ComPtr<ID3D11VideoProcessorEnumerator> enumerator;
    ComPtr<ID3D11VideoProcessor> processor;
    ComPtr<IMFDXGIDeviceManager> manager;
    ComPtr<IMFTransform> encoder;
    ComPtr<IMFMediaEventGenerator> events;
    Impl(const capture::Surface &s, uint32_t w, uint32_t h, uint32_t rate, uint32_t bitrate)
        : width(w), height(h), fps(rate), sourceWidth(s.width), sourceHeight(s.height), device(s.device) {
        check(CoInitializeEx(nullptr, COINIT_MULTITHREADED), "gpu COM");
        com = true;
        try {
            check(MFStartup(MF_VERSION), "gpu MF startup");
            mf = true;
            ComPtr<ID3D10Multithread> multithread;
            check(s.context->QueryInterface(IID_PPV_ARGS(&multithread)), "gpu multithread protection");
            multithread->SetMultithreadProtected(TRUE);
            check(device.As(&video), "gpu video device");
            check(s.context->QueryInterface(IID_PPV_ARGS(&context)), "gpu video context");
            D3D11_VIDEO_PROCESSOR_CONTENT_DESC d{};
            d.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
            d.InputWidth = s.width;
            d.InputHeight = s.height;
            d.OutputWidth = w;
            d.OutputHeight = h;
            d.InputFrameRate = {rate, 1};
            d.OutputFrameRate = {rate, 1};
            d.Usage = D3D11_VIDEO_USAGE_PLAYBACK_NORMAL;
            check(video->CreateVideoProcessorEnumerator(&d, &enumerator), "gpu processor enumeration");
            check(video->CreateVideoProcessor(enumerator.Get(), 0, &processor), "gpu processor");
            UINT token = 0;
            check(MFCreateDXGIDeviceManager(&token, &manager), "gpu device manager");
            check(manager->ResetDevice(device.Get(), token), "gpu bind device");
            MFT_REGISTER_TYPE_INFO in{MFMediaType_Video, MFVideoFormat_NV12},
                out{MFMediaType_Video, MFVideoFormat_H264};
            IMFActivate **choices = nullptr;
            UINT32 count = 0;
            check(MFTEnumEx(MFT_CATEGORY_VIDEO_ENCODER, MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER,
                            &in, &out, &choices, &count),
                  "hardware codec discovery");
            auto releaseChoices = [count](IMFActivate **list) {
                for (UINT32 i = 0; i < count; ++i)
                    list[i]->Release();
                CoTaskMemFree(list);
            };
            std::unique_ptr<IMFActivate *, decltype(releaseChoices)> choiceOwner(choices, releaseChoices);
            for (UINT32 i = 0; i < count; ++i) {
                if (!encoder) {
                    ComPtr<IMFTransform> candidate;
                    if (SUCCEEDED(choices[i]->ActivateObject(IID_PPV_ARGS(&candidate)))) {
                        ComPtr<IMFAttributes> attrs;
                        UINT32 aware = 0, async = 0;
                        if (SUCCEEDED(candidate->GetAttributes(&attrs)) &&
                            SUCCEEDED(attrs->GetUINT32(MF_SA_D3D11_AWARE, &aware)) && aware &&
                            SUCCEEDED(attrs->GetUINT32(MF_TRANSFORM_ASYNC, &async)) && async &&
                            SUCCEEDED(attrs->SetUINT32(MF_TRANSFORM_ASYNC_UNLOCK, TRUE)) &&
                            SUCCEEDED(candidate->ProcessMessage(
                                MFT_MESSAGE_SET_D3D_MANAGER, reinterpret_cast<ULONG_PTR>(manager.Get())))) {
                            auto compressed = media(MFVideoFormat_H264, w, h, rate);
                            compressed->SetUINT32(MF_MT_AVG_BITRATE, bitrate);
                            compressed->SetUINT32(MF_MT_MPEG2_PROFILE, 66);
                            auto raw = media(MFVideoFormat_NV12, w, h, rate);
                            raw->SetUINT32(MF_MT_DEFAULT_STRIDE, w);
                            raw->SetUINT32(MF_MT_SAMPLE_SIZE, w * h * 3 / 2);
                            attrs->SetUINT32(MF_LOW_LATENCY, TRUE);
                            if (SUCCEEDED(candidate->SetOutputType(0, compressed.Get(), 0)) &&
                                SUCCEEDED(candidate->SetInputType(0, raw.Get(), 0)) &&
                                SUCCEEDED(candidate.As(&events)))
                                encoder = candidate;
                        }
                    }
                }
            }
            if (!encoder)
                throw std::runtime_error("gpu_h264_encoder_unavailable");
            check(encoder->ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0), "gpu begin");
            check(encoder->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0), "gpu stream");
            // Desktop Duplication may not produce another surface for a static screen.
            // Wait boundedly for the first input slot instead of losing that first frame.
            auto deadline = std::chrono::steady_clock::now() + std::chrono::milliseconds(500);
            while (!need && std::chrono::steady_clock::now() < deadline) {
                drain();
                if (!need)
                    std::this_thread::sleep_for(std::chrono::milliseconds(1));
            }
            if (!need)
                throw std::runtime_error("gpu_encoder_not_ready");
        } catch (...) {
            cleanup();
            throw;
        }
    }
    ~Impl() { cleanup(); }
    void cleanup() {
        if (encoder) {
            encoder->ProcessMessage(MFT_MESSAGE_COMMAND_FLUSH, 0);
            encoder->ProcessMessage(MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
        }
        events.Reset();
        encoder.Reset();
        manager.Reset();
        processor.Reset();
        enumerator.Reset();
        context.Reset();
        video.Reset();
        device.Reset();
        if (mf) {
            MFShutdown();
            mf = false;
        }
        if (com) {
            CoUninitialize();
            com = false;
        }
    }
    std::vector<Packet> drain() {
        std::vector<Packet> result;
        for (unsigned i = 0; i < 32; ++i) {
            ComPtr<IMFMediaEvent> event;
            auto hr = events->GetEvent(MF_EVENT_FLAG_NO_WAIT, &event);
            if (hr == MF_E_NO_EVENTS_AVAILABLE)
                break;
            check(hr, "gpu event");
            HRESULT status = S_OK;
            check(event->GetStatus(&status), "gpu event status");
            check(status, "gpu encoder failure");
            MediaEventType kind{};
            check(event->GetType(&kind), "gpu event type");
            if (kind == METransformNeedInput) {
                need = std::min(need + 1, 4u);
                continue;
            }
            if (kind != METransformHaveOutput)
                continue;
            MFT_OUTPUT_STREAM_INFO info{};
            check(encoder->GetOutputStreamInfo(0, &info), "gpu output info");
            MFT_OUTPUT_DATA_BUFFER output{};
            ComPtr<IMFSample> sample;
            if (!(info.dwFlags & MFT_OUTPUT_STREAM_PROVIDES_SAMPLES)) {
                check(MFCreateSample(&sample), "gpu output sample");
                ComPtr<IMFMediaBuffer> memory;
                auto capacity = std::max<DWORD>(info.cbSize, 4 * 1024 * 1024);
                if (capacity > 16 * 1024 * 1024)
                    throw std::runtime_error("gpu_output_too_large");
                check(MFCreateMemoryBuffer(capacity, &memory), "gpu output buffer");
                sample->AddBuffer(memory.Get());
                output.pSample = sample.Get();
            }
            DWORD flags = 0;
            hr = encoder->ProcessOutput(0, 1, &output, &flags);
            if (output.pEvents)
                output.pEvents->Release();
            if (output.pSample && output.pSample != sample.Get())
                sample.Attach(output.pSample);
            check(hr, "gpu output");
            if (!sample)
                throw std::runtime_error("gpu_empty_output");
            ComPtr<IMFMediaBuffer> buffer;
            check(sample->ConvertToContiguousBuffer(&buffer), "gpu bitstream");
            BYTE *data = nullptr;
            DWORD size = 0;
            check(buffer->Lock(&data, nullptr, &size), "gpu output lock");
            if (size > 4 * 1024 * 1024) {
                buffer->Unlock();
                throw std::runtime_error("gpu_output_too_large");
            }
            Packet packet;
            try {
                packet.h264.assign(data, data + size);
            } catch (...) {
                buffer->Unlock();
                throw;
            }
            buffer->Unlock();
            LONGLONG stamp = 0;
            sample->GetSampleTime(&stamp);
            packet.timestamp = stamp;
            UINT32 clean = 0;
            sample->GetUINT32(MFSampleExtension_CleanPoint, &clean);
            packet.keyframe = clean != 0;
            result.push_back(std::move(packet));
            if (outstanding)
                --outstanding;
        }
        return result;
    }
    std::vector<Packet> encode(const capture::Surface &s, int64_t stamp) {
        auto result = drain();
        if (!need || outstanding >= 4)
            return result;
        if (s.device != device.Get() || s.width != sourceWidth || s.height != sourceHeight)
            throw std::runtime_error("gpu_display_changed");
        // A fresh GPU surface is owned by each IMF sample until the asynchronous MFT
        // releases it. No texture is overwritten while the encoder may still read it.
        D3D11_TEXTURE2D_DESC desc{};
        desc.Width = width;
        desc.Height = height;
        desc.MipLevels = desc.ArraySize = 1;
        desc.Format = DXGI_FORMAT_NV12;
        desc.SampleDesc.Count = 1;
        desc.Usage = D3D11_USAGE_DEFAULT;
        desc.BindFlags = D3D11_BIND_RENDER_TARGET;
        ComPtr<ID3D11Texture2D> target;
        check(device->CreateTexture2D(&desc, nullptr, &target), "gpu NV12 surface");
        D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC id{};
        id.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
        ComPtr<ID3D11VideoProcessorInputView> input;
        check(video->CreateVideoProcessorInputView(s.texture, enumerator.Get(), &id, &input),
              "gpu input view");
        D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC od{};
        od.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
        ComPtr<ID3D11VideoProcessorOutputView> output;
        check(video->CreateVideoProcessorOutputView(target.Get(), enumerator.Get(), &od, &output),
              "gpu output view");
        RECT source{0, 0, LONG(s.width), LONG(s.height)}, dest{0, 0, LONG(width), LONG(height)};
        context->VideoProcessorSetStreamSourceRect(processor.Get(), 0, TRUE, &source);
        context->VideoProcessorSetStreamDestRect(processor.Get(), 0, TRUE, &dest);
        context->VideoProcessorSetOutputTargetRect(processor.Get(), TRUE, &dest);
        D3D11_VIDEO_PROCESSOR_STREAM stream{};
        stream.Enable = TRUE;
        stream.pInputSurface = input.Get();
        check(context->VideoProcessorBlt(processor.Get(), output.Get(), 0, 1, &stream),
              "gpu color conversion");
        ComPtr<IMFMediaBuffer> buffer;
        check(MFCreateDXGISurfaceBuffer(__uuidof(ID3D11Texture2D), target.Get(), 0, FALSE, &buffer),
              "gpu sample surface");
        ComPtr<IMFSample> sample;
        check(MFCreateSample(&sample), "gpu input sample");
        sample->AddBuffer(buffer.Get());
        sample->SetSampleTime(stamp);
        sample->SetSampleDuration(10000000 / fps);
        check(encoder->ProcessInput(0, sample.Get(), 0), "gpu encode");
        --need;
        ++outstanding;
        return result;
    }
};
GpuEncoder::GpuEncoder(const capture::Surface &s, uint32_t w, uint32_t h, uint32_t fps, uint32_t rate)
    : impl_(std::make_unique<Impl>(s, w, h, fps, rate)) {}
GpuEncoder::~GpuEncoder() = default;
std::vector<Packet> GpuEncoder::encode(const capture::Surface &s, int64_t stamp) {
    return impl_->encode(s, stamp);
}
std::vector<Packet> GpuEncoder::drain() { return impl_->drain(); }
} // namespace netgo::video
