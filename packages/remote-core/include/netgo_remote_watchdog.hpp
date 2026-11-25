#pragma once
#include "netgo_remote_clock.hpp"

namespace netgo::remote {
// Expiry is terminal. Delayed responses and resume from suspend cannot reopen
// media after control loss. Sample under the lock so concurrent callers cannot
// mistake an older sample for a clock rollback.
class Watchdog final {
 using Clock = std::uint64_t (*)();
 static constexpr std::uint64_t timeout_ms = 6000;
 Clock clock_;
 std::mutex mutex_;
 std::uint64_t last_, until_;
 bool check(std::uint64_t now) {
  if (!until_ || now < last_ || now >= until_) { until_ = 0; return false; }
  last_ = now; return true;
 }
public:
 explicit Watchdog(Clock clock = monotonic_time_ms) : clock_(clock), last_(clock()),
  until_(last_ <= UINT64_MAX - timeout_ms ? last_ + timeout_ms : 0) {}
 bool live() { std::lock_guard<std::mutex> lock(mutex_); return check(clock_()); }
 bool refresh() {
  std::lock_guard<std::mutex> lock(mutex_);
  auto now = clock_();
  if (!check(now)) return false;
  if (now > UINT64_MAX - timeout_ms) { until_ = 0; return false; }
  until_ = now + timeout_ms; return true;
 }
 void close() { std::lock_guard<std::mutex> lock(mutex_); until_ = 0; }
};
}
