#include "remote_runtime.hpp"
#include "connection.hpp"
#include "remote_transport.hpp"
#include "remote_input.hpp"
#include "netgo_remote_ticket.h"
#include "netgo_remote_client.h"
#include "netgo_remote_clock.hpp"
#include "netgo_remote_watchdog.hpp"
#include "netgo_remote_session_fence.hpp"
#include "desktop_capture.hpp"
#include "video_codec.hpp"
#include "gpu_encoder.hpp"
#include "netgo_remote_host.h"
#include "remote_files.hpp"
#include <deque>
#include <cstring>
#include "audio.hpp"
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <d3d11.h>
#include <wrl/client.h>
#include <set>
#include <mutex>
#include <algorithm>
#include <utility>

namespace netgo::desktop {
namespace {
using remote::Connection;
using remote::Key;
using remote::Kind;
using remote::Message;
using Bytes = std::vector<unsigned char>;
constexpr unsigned fps = 20;
constexpr char base[] = "/netgo/v2/remote";
Json call(const std::string &site, const std::string &method, const std::string &path,
          const Json &body = nullptr, const std::string &idem = {},
          const std::function<bool()> &active = {}) {
    return pipeCall({{"action", "remote-call"},
                     {"site", site},
                     {"method", method},
                     {"path", path},
                     {"body", body},
                     {"idempotency", idem}},
                    active);
}
Json context(const std::string &site, const std::function<bool()> &active = {}) {
    return pipeCall({{"action", "remote-context"}, {"site", site}}, active);
}

struct Input {
    std::set<unsigned> keys;
    std::set<unsigned> buttons;
    int left = 0, top = 0;
    unsigned screenWidth = 0, screenHeight = 0;
    ~Input() { release(); }
    void release() {
        for (auto k : keys) {
            INPUT in{};
            in.type = INPUT_KEYBOARD;
            in.ki.wVk = WORD(k & 255);
            in.ki.dwFlags = KEYEVENTF_KEYUP | ((k & 256) ? KEYEVENTF_EXTENDEDKEY : 0);
            SendInput(1, &in, sizeof(in));
        }
        keys.clear();
        for (auto b : buttons) {
            INPUT in{};
            in.type = INPUT_MOUSE;
            in.mi.dwFlags = b == 1 ? MOUSEEVENTF_LEFTUP : b == 2 ? MOUSEEVENTF_RIGHTUP : MOUSEEVENTF_MIDDLEUP;
            SendInput(1, &in, sizeof(in));
        }
        buttons.clear();
    }
    void apply(const Json &j) {
        INPUT in{};
        auto type = j.value("type", "");
        unsigned tracked = 0;
        bool down = false;
        if (type == "key") {
            auto code = j.at("code").get<unsigned>();
            if (code == 0 || code > 255)
                throw Error("remote_invalid_input");
            down = j.at("down").get<bool>();
            auto extended = j.value("extended", false);
            in.type = INPUT_KEYBOARD;
            in.ki.wVk = WORD(code);
            in.ki.dwFlags = (down ? 0 : KEYEVENTF_KEYUP) | (extended ? KEYEVENTF_EXTENDEDKEY : 0);
            tracked = code | (extended ? 256 : 0);
        } else if (type == "move") {
            auto x = j.at("x").get<unsigned>(), y = j.at("y").get<unsigned>();
            if (x > 65535 || y > 65535 || !screenWidth || !screenHeight)
                throw Error("remote_invalid_input");
            auto vw = GetSystemMetrics(SM_CXVIRTUALSCREEN), vh = GetSystemMetrics(SM_CYVIRTUALSCREEN);
            if (vw < 2 || vh < 2)
                throw Error("remote_invalid_display");
            auto px = left + int(std::uint64_t(x) * (screenWidth - 1) / 65535),
                 py = top + int(std::uint64_t(y) * (screenHeight - 1) / 65535);
            in.type = INPUT_MOUSE;
            in.mi.dx = LONG(std::int64_t(px - GetSystemMetrics(SM_XVIRTUALSCREEN)) * 65535 / (vw - 1));
            in.mi.dy = LONG(std::int64_t(py - GetSystemMetrics(SM_YVIRTUALSCREEN)) * 65535 / (vh - 1));
            in.mi.dwFlags = MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK;
        } else if (type == "button") {
            auto b = j.at("button").get<unsigned>();
            if (b < 1 || b > 3)
                throw Error("remote_invalid_input");
            down = j.at("down").get<bool>();
            tracked = b;
            in.type = INPUT_MOUSE;
            in.mi.dwFlags = b == 1   ? (down ? MOUSEEVENTF_LEFTDOWN : MOUSEEVENTF_LEFTUP)
                            : b == 2 ? (down ? MOUSEEVENTF_RIGHTDOWN : MOUSEEVENTF_RIGHTUP)
                                     : (down ? MOUSEEVENTF_MIDDLEDOWN : MOUSEEVENTF_MIDDLEUP);
        } else if (type == "wheel") {
            auto delta = j.at("delta").get<int>();
            if (delta < -1200 || delta > 1200)
                throw Error("remote_invalid_input");
            in.type = INPUT_MOUSE;
            in.mi.dwFlags = j.value("horizontal", false) ? MOUSEEVENTF_HWHEEL : MOUSEEVENTF_WHEEL;
            in.mi.mouseData = DWORD(delta);
        } else if (type == "text") {
            auto text = j.at("text").get<std::string>();
            if (text.empty() || text.size() > 8192 || text.find('\0') != std::string::npos)
                throw Error("remote_invalid_input");
            auto unicode = wide(text);
            std::vector<INPUT> letters;
            letters.reserve(unicode.size() * 2);
            for (auto code : unicode) {
                INPUT letter{};
                letter.type = INPUT_KEYBOARD;
                letter.ki.wScan = WORD(code);
                letter.ki.dwFlags = KEYEVENTF_UNICODE;
                letters.push_back(letter);
                letter.ki.dwFlags |= KEYEVENTF_KEYUP;
                letters.push_back(letter);
            }
            if (SendInput(UINT(letters.size()), letters.data(), sizeof(INPUT)) != letters.size()) {
                release();
                throw Error("remote_input_unavailable");
            }
            return;
        } else if (type == "release") {
            release();
            return;
        } else
            throw Error("remote_invalid_input");
        if (SendInput(1, &in, sizeof(in)) != 1)
            throw Error("remote_input_unavailable");
        if (type == "key") {
            if (down)
                keys.insert(tracked);
            else
                keys.erase(tracked);
        }
        if (type == "button") {
            if (down)
                buttons.insert(tracked);
            else
                buttons.erase(tracked);
        }
    }
};
struct Capture {
    Microsoft::WRL::ComPtr<ID3D11Texture2D> texture;
    video::Frame frame;
    Input input;
    std::unique_ptr<video::GpuEncoder> gpu;
    std::unique_ptr<video::Codec> software;
    bool selected = false;
    std::deque<remote::Message> packets;
    std::string error;
    std::function<bool()> allowed;
    std::function<void(std::string)> status;
    Clock::time_point started = Clock::now();
    static void read(void *ctx, const capture::Surface &s) noexcept {
        auto &c = *static_cast<Capture *>(ctx);
        try {
            if (!c.allowed())
                throw Error("remote_closed");
            if (s.clockwise_rotation || s.protected_content_masked)
                throw Error("remote_display_unavailable");
            auto size = video::fit_size(s.width, s.height);
            if (c.frame.width && (c.frame.width != size.width || c.frame.height != size.height))
                throw Error("remote_display_changed");
            c.frame.width = size.width;
            c.frame.height = size.height;
            auto bitrate = std::clamp(size.width * size.height * fps / 6u, 800'000u, 6'000'000u);
            if (!c.selected) {
                c.selected = true;
                try {
                    c.gpu = std::make_unique<video::GpuEncoder>(s, size.width, size.height, fps, bitrate);
                    c.status("编码：GPU 直接硬件 H.264");
                } catch (...) {
                    c.software = std::make_unique<video::Codec>(video::Codec::Mode::encode, size.width,
                                                                size.height, fps, bitrate);
                    c.status("硬件编码不可用，使用软件 H.264");
                }
            }
            auto stamp =
                std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now() - c.started).count() / 100;
            std::vector<video::Packet> output;
            if (c.gpu)
                output = c.gpu->encode(s, stamp);
            else {
                D3D11_TEXTURE2D_DESC desc{};
                s.texture->GetDesc(&desc);
                if (!c.texture) {
                    desc.BindFlags = 0;
                    desc.MiscFlags = 0;
                    desc.Usage = D3D11_USAGE_STAGING;
                    desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
                    if (FAILED(s.device->CreateTexture2D(&desc, nullptr, c.texture.GetAddressOf())))
                        throw Error("remote_capture_failed");
                }
                s.context->CopyResource(c.texture.Get(), s.texture);
                D3D11_MAPPED_SUBRESOURCE map{};
                if (FAILED(s.context->Map(c.texture.Get(), 0, D3D11_MAP_READ, 0, &map)))
                    throw Error("remote_capture_failed");
                try {
                    video::bgra_to_nv12(static_cast<const unsigned char *>(map.pData), s.width, s.height,
                                        map.RowPitch, size.width, size.height, c.frame);
                } catch (...) {
                    s.context->Unmap(c.texture.Get(), 0);
                    throw;
                }
                s.context->Unmap(c.texture.Get(), 0);
                output = c.software->encode(c.frame, stamp);
            }
            for (auto &p : output) {
                if (c.packets.size() >= 8)
                    throw Error("remote_encoder_backpressure");
                c.packets.push_back({remote::Kind::video, std::move(p.h264)});
            }
            c.input.left = s.desktop_left;
            c.input.top = s.desktop_top;
            c.input.screenWidth = s.width;
            c.input.screenHeight = s.height;
        } catch (const std::exception &e) {
            c.error = e.what();
        }
    }
};
} // namespace

Json inventory(const std::string &site, const std::function<bool()> &active) {
    return call(site, "GET", base, nullptr, {}, active);
}

std::string clipboardText(const std::optional<std::string> &value) {
    HWND owner = CreateWindowExW(0, L"STATIC", L"NetGo clipboard", 0, 0, 0, 0, 0, HWND_MESSAGE, nullptr,
                                 nullptr, nullptr);
    if (!owner)
        throw Error("remote_clipboard_unavailable");
    if (!OpenClipboard(owner)) {
        DestroyWindow(owner);
        throw Error("remote_clipboard_busy");
    }
    try {
        std::string text;
        if (value) {
            if (value->size() > 8192 || value->find('\0') != std::string::npos)
                throw Error("remote_clipboard_too_large");
            auto wideText = wide(*value);
            auto memory = GlobalAlloc(GMEM_MOVEABLE, (wideText.size() + 1) * sizeof(wchar_t));
            if (!memory)
                throw Error("remote_clipboard_unavailable");
            auto data = GlobalLock(memory);
            if (!data) {
                GlobalFree(memory);
                throw Error("remote_clipboard_unavailable");
            }
            std::memcpy(data, wideText.c_str(), (wideText.size() + 1) * sizeof(wchar_t));
            GlobalUnlock(memory);
            if (!EmptyClipboard() || !SetClipboardData(CF_UNICODETEXT, memory)) {
                GlobalFree(memory);
                throw Error("remote_clipboard_unavailable");
            }
        } else if (IsClipboardFormatAvailable(CF_UNICODETEXT)) {
            auto memory = GetClipboardData(CF_UNICODETEXT);
            auto size = GlobalSize(memory);
            if (size > 32768)
                throw Error("remote_clipboard_too_large");
            auto data = static_cast<const wchar_t *>(GlobalLock(memory));
            if (!data)
                throw Error("remote_clipboard_unavailable");
            auto count = wcsnlen(data, size / sizeof(wchar_t));
            try {
                if (count == size / sizeof(wchar_t))
                    throw Error("remote_clipboard_invalid");
                text = utf8(std::wstring(data, count));
            } catch (...) {
                GlobalUnlock(memory);
                throw;
            }
            GlobalUnlock(memory);
            if (text.size() > 8192)
                throw Error("remote_clipboard_too_large");
        }
        CloseClipboard();
        DestroyWindow(owner);
        return text;
    } catch (...) {
        CloseClipboard();
        DestroyWindow(owner);
        throw;
    }
}
void host(const std::string &site, Callbacks cb) {
    struct Host {
        std::string site;
        Callbacks cb;
        std::unique_ptr<capture::DesktopCapture> source;
        std::unique_ptr<Capture> capture;
        std::unique_ptr<audio::Stream> audio;
        Clock::time_point next;
    };
    Host h{site, std::move(cb), {}, {}, {}, {}};
    auto exchange = dataRoot() / L"Exchange";
    secureDirectory(exchange);
    auto folder = remote::path_text(exchange);
    wchar_t machine[128]{};
    DWORD count = 128;
    GetComputerNameW(machine, &count);
    auto name = utf8(machine);
    ng_remote_host_options o{};
    o.struct_size = sizeof(o);
    o.abi_version = NG_REMOTE_HOST_ABI;
    o.site_id = site.c_str();
    o.name = name.c_str();
    o.platform = "windows";
    o.exchange_directory = folder.c_str();
    o.capabilities =
        NG_REMOTE_VIEW | NG_REMOTE_INPUT | NG_REMOTE_CLIPBOARD | NG_REMOTE_UPLOAD | NG_REMOTE_DOWNLOAD;
    try {
        audio::Stream probe(true);
        o.capabilities |= NG_REMOTE_AUDIO;
    } catch (...) {
    }
    o.unattended = h.cb.unattended;
    o.context = &h;
    o.active = [](void *p) -> int32_t { return static_cast<Host *>(p)->cb.running(); };
    o.control = [](void *p, const char *method, const char *path, const char *body, const char *idem,
                   char *output, size_t capacity, size_t *length) -> int32_t {
        try {
            auto &h = *static_cast<Host *>(p);
            auto active = h.cb.running;
            if (std::string(method) == "DELETE" ||
                (std::string(method) == "PUT" && *body && !Json::parse(body).value("enabled", true))) {
                auto end = Clock::now() + std::chrono::seconds(3);
                active = [end] { return Clock::now() < end; };
            }
            auto value = std::string(method) == "CONTEXT"
                             ? context(h.site, active)
                             : call(h.site, method, path, *body ? Json::parse(body) : Json(), idem, active);
            auto text = value.dump();
            if (text.size() > capacity)
                return -1;
            std::copy(text.begin(), text.end(), output);
            *length = text.size();
            return 0;
        } catch (...) {
            return -1;
        }
    };
    o.consent = [](void *p, const char *user, uint32_t caps) -> int32_t {
        return static_cast<Host *>(p)->cb.consent(user, caps);
    };
    o.open = [](void *p, uint32_t caps, uint32_t *w, uint32_t *height, uint32_t *rate) -> int32_t {
        try {
            auto &h = *static_cast<Host *>(p);
            h.capture = std::make_unique<Capture>();
            h.capture->allowed = h.cb.running;
            h.capture->status = h.cb.status;
            h.source = std::make_unique<capture::DesktopCapture>();
            if (h.source->open() != capture::Result::frame)
                return -1;
            auto end = Clock::now() + std::chrono::seconds(3);
            while (!h.capture->frame.width && h.cb.running() && Clock::now() < end) {
                h.source->next(Capture::read, h.capture.get(), 16);
                if (!h.capture->error.empty())
                    throw Error(h.capture->error);
            }
            if (!h.capture->frame.width)
                return -1;
            if (caps & NG_REMOTE_AUDIO)
                h.audio = std::make_unique<audio::Stream>(true);
            *w = h.capture->frame.width;
            *height = h.capture->frame.height;
            *rate = fps;
            h.next = Clock::now();
            return 0;
        } catch (const std::exception &e) {
            static_cast<Host *>(p)->cb.status(e.what());
            return -1;
        }
    };
    o.poll = [](void *p, uint32_t *kind, uint8_t *output, size_t capacity, size_t *length) -> int32_t {
        try {
            auto &h = *static_cast<Host *>(p);
            *length = 0;
            if (h.capture->packets.empty() && h.audio) {
                for (auto &packet : h.audio->capture()) {
                    if (h.capture->packets.size() >= 8)
                        throw Error("remote_audio_backpressure");
                    h.capture->packets.push_back({remote::Kind::audio, std::move(packet)});
                }
            }
            if (h.capture->packets.empty()) {
                if (h.capture->gpu) {
                    for (auto &packet : h.capture->gpu->drain())
                        h.capture->packets.push_back({remote::Kind::video, std::move(packet.h264)});
                }
                if (Clock::now() >= h.next) {
                    h.next = Clock::now() + std::chrono::milliseconds(1000 / fps);
                    auto r = h.source->next(Capture::read, h.capture.get(), 5);
                    if (r != capture::Result::frame && r != capture::Result::timeout)
                        throw Error("remote_capture_lost");
                    if (!h.capture->error.empty())
                        throw Error(h.capture->error);
                }
            }
            if (!h.capture->packets.empty()) {
                auto &m = h.capture->packets.front();
                if (m.body.size() > capacity)
                    return -1;
                std::copy(m.body.begin(), m.body.end(), output);
                *length = m.body.size();
                *kind = uint32_t(m.kind);
                h.capture->packets.pop_front();
            }
            return 0;
        } catch (...) {
            return -1;
        }
    };
    o.input = [](void *p, const char *json, size_t size) -> int32_t {
        try {
            static_cast<Host *>(p)->capture->input.apply(Json::parse(json, json + size));
            return 0;
        } catch (...) {
            return -1;
        }
    };
    o.close = [](void *p) {
        auto &h = *static_cast<Host *>(p);
        h.audio.reset();
        h.capture.reset();
        h.source.reset();
    };
    o.clipboard = [](void *, int32_t set, const char *text, size_t size, char *output, size_t capacity,
                     size_t *length) -> int32_t {
        try {
            auto value =
                clipboardText(set ? std::optional<std::string>(std::string(text, size)) : std::nullopt);
            if (value.size() > capacity)
                return -1;
            std::copy(value.begin(), value.end(), output);
            *length = value.size();
            return 0;
        } catch (...) {
            return -1;
        }
    };
    o.status = [](void *p, const char *code) {
        auto &h = *static_cast<Host *>(p);
        std::string text = code;
        if (text == "remote_host_waiting")
            text = "共享已开启；每次连接需要本机确认";
        else if (text == "remote_host_unattended")
            text = "无人值守已开启；仅接受站点授权账号，停止或关闭窗口可撤销";
        else if (text == "remote_host_connected")
            text = "正在共享桌面；文件交换目录：" + remote::path_text(dataRoot() / L"Exchange");
        else if (text == "remote_host_stopped")
            text = "桌面共享已停止";
        h.cb.status(text);
    };
    ng_remote_host *raw = nullptr;
    if (ng_remote_host_create(&o, &raw) != 0)
        throw Error("remote_host_failed");
    std::unique_ptr<ng_remote_host, decltype(&ng_remote_host_destroy)> host(raw, ng_remote_host_destroy);
    ng_remote_host_run(host.get());
}

void view(const std::string &site, const std::string &hostID, uint32_t capabilities, Callbacks cb) {
    struct Bridge {
        std::string site;
        Callbacks cb;
        ng_remote_client *client = nullptr;
        std::unique_ptr<video::Codec> decoder;
        std::unique_ptr<audio::Stream> sound;
    };
    Bridge bridge{site, std::move(cb), nullptr, {}, {}};
    ng_remote_client_options options{};
    options.struct_size = sizeof(options);
    options.abi_version = NG_REMOTE_CLIENT_ABI;
    options.site_id = site.c_str();
    options.host_id = hostID.c_str();
    options.capabilities = capabilities;
    options.context = &bridge;
    options.active = [](void *p) -> int32_t { return static_cast<Bridge *>(p)->cb.running() ? 1 : 0; };
    options.control = [](void *p, const char *method, const char *path, const char *body, const char *idem,
                         char *output, size_t capacity, size_t *length) -> int32_t {
        try {
            auto &b = *static_cast<Bridge *>(p);
            auto active = b.cb.running;
            // Cancelling the viewer must still close its issued/active server session.
            // This borrows the existing VPN identity with a separate cleanup deadline.
            if (std::string(method) == "DELETE") {
                auto end = Clock::now() + std::chrono::seconds(3);
                active = [end] { return Clock::now() < end; };
            }
            auto value = std::string(method) == "CONTEXT"
                             ? context(b.site, active)
                             : call(b.site, method, path, *body ? Json::parse(body) : Json(), idem, active);
            auto text = value.dump();
            if (text.size() > capacity)
                return -1;
            std::copy(text.begin(), text.end(), output);
            *length = text.size();
            return 0;
        } catch (...) {
            return -1;
        }
    };
    options.sign = [](void *p, const uint8_t *bytes, size_t size, uint8_t output[64]) -> int32_t {
        try {
            auto &b = *static_cast<Bridge *>(p);
            auto ctx = context(b.site, b.cb.running);
            auto seed = credentialRead("device/" + ctx.at("credential_scope").get<std::string>());
            if (seed.size() != 32 || publicKey(seed) != ctx.at("public_key").get<std::string>()) {
                wipe(seed);
                return -1;
            }
            auto privateKey = std::unique_ptr<EVP_PKEY, decltype(&EVP_PKEY_free)>(
                EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr,
                                             reinterpret_cast<const uint8_t *>(seed.data()), seed.size()),
                EVP_PKEY_free);
            wipe(seed);
            auto signer =
                std::unique_ptr<EVP_MD_CTX, decltype(&EVP_MD_CTX_free)>(EVP_MD_CTX_new(), EVP_MD_CTX_free);
            size_t n = 64;
            return privateKey && signer &&
                           EVP_DigestSignInit(signer.get(), nullptr, nullptr, nullptr, privateKey.get()) ==
                               1 &&
                           EVP_DigestSign(signer.get(), output, &n, bytes, size) == 1 && n == 64
                       ? 0
                       : -1;
        } catch (...) {
            return -1;
        }
    };
    options.event = [](void *p, const char *text) {
        try {
            auto &b = *static_cast<Bridge *>(p);
            if (b.cb.event)
                b.cb.event(Json::parse(text));
        } catch (...) {
        }
    };
    options.tick = [](void *p) {
        try {
            auto &b = *static_cast<Bridge *>(p);
            if (b.cb.commands)
                for (const auto &command : b.cb.commands()) {
                    auto s = command.dump();
                    if (ng_remote_client_command(b.client, s.data(), s.size()) != 0 && b.cb.event)
                        b.cb.event({{"error", "remote_command_busy"}});
                }
        } catch (...) {
        }
    };
    options.input = [](void *p, char *output, size_t capacity, size_t *length) -> int32_t {
        try {
            auto &b = *static_cast<Bridge *>(p);
            auto text = Json(b.cb.inputs()).dump();
            if (text.size() > capacity)
                return -1;
            std::copy(text.begin(), text.end(), output);
            *length = text.size();
            return 0;
        } catch (...) {
            return -1;
        }
    };
    options.format = [](void *p, uint32_t w, uint32_t h, uint32_t rate, uint32_t caps) -> int32_t {
        try {
            auto &b = *static_cast<Bridge *>(p);
            b.decoder = std::make_unique<video::Codec>(video::Codec::Mode::decode, w, h, rate);
            if (caps & NG_REMOTE_AUDIO)
                b.sound = std::make_unique<audio::Stream>(false);
            if (b.cb.permissions)
                b.cb.permissions(caps);
            return 0;
        } catch (...) {
            return -1;
        }
    };
    options.audio = [](void *p, const uint8_t *bytes, size_t size) -> int32_t {
        try {
            static_cast<Bridge *>(p)->sound->play(bytes, size);
            return 0;
        } catch (...) {
            return -1;
        }
    };
    options.video = [](void *p, const uint8_t *bytes, size_t size, int64_t stamp) -> int32_t {
        try {
            auto &b = *static_cast<Bridge *>(p);
            for (auto &frame : b.decoder->decode({stamp, false, Bytes(bytes, bytes + size)}))
                b.cb.present({frame.width, frame.height, video::nv12_to_bgra(frame)});
            return 0;
        } catch (...) {
            return -1;
        }
    };
    options.status = [](void *p, const char *code) {
        auto &b = *static_cast<Bridge *>(p);
        try {
            std::string text = code;
            if (text == "remote_connected")
                text = "已连接；点击“停止”结束远程桌面";
            else if (text == "remote_waiting_consent")
                text = "等待对方确认…";
            else if (text == "remote_clock_invalid" || text == "remote_generation_invalid")
                text = "服务端远控信息不完整，请同步更新局域网服务端与客户端";
            else if (text == "remote_host_busy")
                text = "对方正在接受其他远程连接，请稍后刷新主机列表";
            else if (text == "remote_host_unavailable")
                text = "对方暂不可连接，请确认其 VPN 已连接并开启桌面共享";
            else if (text == "remote_view_forbidden")
                text = "当前账号未获该主机的观看授权";
            else if (text == "remote_disconnected")
                text = "远程桌面已结束";
            else if (text == "remote_decoder_unavailable")
                text = "当前系统无法解码此远程画面";
            b.cb.status(std::move(text));
        } catch (...) {
        }
    };
    ng_remote_client *raw = nullptr;
    if (ng_remote_client_create(&options, &raw) != NG_REMOTE_CLIENT_OK)
        throw Error("remote_client_failed");
    std::unique_ptr<ng_remote_client, decltype(&ng_remote_client_destroy)> client(raw,
                                                                                  ng_remote_client_destroy);
    bridge.client = raw;
    ng_remote_client_run(client.get());
}
} // namespace netgo::desktop
