#pragma once
#include <cstdint>
#include <memory>
#include <vector>
#include <functional>
#include <span>

namespace netgo::video {
// One worker thread owns each codec. Bounded samples; platform handles remain
// private. This synchronous MF adapter is the explicit software fallback.
struct Frame { std::uint32_t width{},height{};std::vector<std::uint8_t> nv12; };
// Borrowed NV12 planes; padding is described by stride, never copied into a frame.
struct FrameView {
 std::uint32_t width{}, height{}, stride{};
 std::span<const std::uint8_t> y, uv;
};
struct Packet { std::int64_t timestamp{}; bool keyframe{};std::vector<std::uint8_t> h264; };
struct Size {std::uint32_t width{},height{};};
// Keep the native aspect ratio, round to NV12's even dimensions, never upscale.
Size fit_size(std::uint32_t width,std::uint32_t height,std::uint32_t max_width=1280,std::uint32_t max_height=720);
class Codec final {
public:
 enum class Mode { encode,decode };
 Codec(Mode,std::uint32_t width,std::uint32_t height,std::uint32_t fps=30,std::uint32_t bitrate=4'000'000);
 ~Codec();
 Codec(const Codec&)=delete;Codec& operator=(const Codec&)=delete;
 std::vector<Packet> encode(const Frame&,std::int64_t timestamp);
 // Decoder storage stays locked only until this synchronous callback returns.
 void decode(const std::uint8_t*, std::size_t, std::int64_t, const std::function<void(const FrameView&)>&);
 void reset();
private:
 struct Impl;std::unique_ptr<Impl> impl_;
};
void bgra_to_nv12(const std::uint8_t* bgra,std::uint32_t width,std::uint32_t height,std::uint32_t stride,std::uint32_t output_width,std::uint32_t output_height,Frame& output);
Frame bgra_to_nv12(const std::uint8_t* bgra,std::uint32_t width,std::uint32_t height,std::uint32_t stride,std::uint32_t output_width,std::uint32_t output_height);
void nv12_to_bgra(const FrameView&, std::vector<std::uint8_t>& output);
void nv12_to_bgra(const Frame&, std::vector<std::uint8_t>& output);
}
