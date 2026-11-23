#pragma once
#include <cstdint>
#include <memory>
#include <vector>
namespace netgo::audio {
/* Shared wire format: signed 16-bit little endian, 48 kHz, stereo interleaved.
 * Packets contain at most 20 ms; playback retains no application-side queue. */
class Stream {
public:
 explicit Stream(bool capture);~Stream();Stream(const Stream&)=delete;
 std::vector<std::vector<uint8_t>> capture();
 void play(const uint8_t* bytes,size_t size);
private:
 struct Impl;std::unique_ptr<Impl> impl;
};
}
