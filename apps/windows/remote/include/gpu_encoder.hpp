#pragma once
#include "video_codec.hpp"
#include "desktop_capture.hpp"
namespace netgo::video {
class GpuEncoder {
  public:
    GpuEncoder(const capture::Surface &, uint32_t width, uint32_t height, uint32_t fps, uint32_t bitrate);
    ~GpuEncoder();
    GpuEncoder(const GpuEncoder &) = delete;
    std::vector<Packet> encode(const capture::Surface &, int64_t timestamp);
    std::vector<Packet> drain();

  private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};
} // namespace netgo::video
