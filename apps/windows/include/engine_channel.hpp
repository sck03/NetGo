#pragma once
#include "common.hpp"

namespace netgo {
// The parent end is overlapped; the inherited child end remains ordinary stdin/stdout.
class EngineChannel {
    Handle parent_, child_;
public:
    explicit EngineChannel(bool parentWrites);
    HANDLE child() const { return child_; }
    void closeChild();
    DWORD transfer(bool write, void* bytes, DWORD size, Clock::time_point deadline,
                   const std::atomic_bool& cancel);
};
}
