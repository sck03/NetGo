#pragma once
#include <array>
#include <cstdint>
#include <memory>
#include <vector>

namespace netgo::desktop_linux {
// Only the decoder writes. GTK retains a shared owner until its texture retires.
// When all slots are displayed, skip presentation after decoding the reference.
class FramePool {
    std::array<std::shared_ptr<std::vector<uint8_t>>, 3> slots_{};
public:
    std::shared_ptr<std::vector<uint8_t>> acquire(size_t size) {
        for (auto& slot : slots_) {
            if (slot && slot.use_count() != 1) continue;
            if (!slot) slot = std::make_shared<std::vector<uint8_t>>();
            slot->resize(size);
            return slot;
        }
        return {};
    }
};
}
