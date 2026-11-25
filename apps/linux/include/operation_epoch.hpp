#pragma once
#include <atomic>
#include <cstdint>

namespace netgo::desktop_linux {
// A cancellation lane is independent of the ordinary single-job UI executor.
class OperationEpoch {
 std::atomic<uint64_t> value_{0};
public:
 uint64_t advance() { return value_.fetch_add(1)+1; }
 uint64_t current() const { return value_.load(); }
 bool matches(uint64_t value) const { return value_.load()==value; }
};
}
