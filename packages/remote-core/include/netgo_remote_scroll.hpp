#pragma once
#include <cstdint>

namespace netgo::remote {
// X11 exposes discrete wheel buttons. Keep sub-step input until it accumulates;
// the two axes and direction changes must not lose precision.
class WheelAccumulator {
    int vertical_ = 0, horizontal_ = 0;
public:
    int consume(int delta, bool horizontal) {
        auto& remainder = horizontal ? horizontal_ : vertical_;
        const auto total = std::int64_t(remainder) + delta;
        remainder = int(total % 120);
        return int(total / 120);
    }
    void reset() { vertical_ = horizontal_ = 0; }
};
}
