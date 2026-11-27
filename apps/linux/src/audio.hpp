#pragma once
#include <cstddef>
#include <cstdint>
#include <memory>

namespace netgo::desktop_linux {
// Bounded 48 kHz stereo S16LE viewer playback; host capture is independent.
class AudioPlayer {
public:
    AudioPlayer();
    ~AudioPlayer();
    AudioPlayer(const AudioPlayer&) = delete;
    AudioPlayer& operator=(const AudioPlayer&) = delete;
    bool push(const uint8_t* bytes, size_t size);
private:
    struct Impl;
    std::unique_ptr<Impl> impl;
};
}
