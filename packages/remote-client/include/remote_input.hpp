#pragma once
#include "netgo_remote_clock.hpp"
#include <nlohmann/json.hpp>
#include <deque>
#include <mutex>

namespace netgo::remote {
using InputEvent = nlohmann::json;
inline constexpr std::size_t input_batch_limit = 60 * 1024;
inline constexpr std::size_t input_event_limit = 128;
inline constexpr std::uint64_t input_max_age_ms = 1000;

inline bool valid_input(const InputEvent& event) {
 if (!event.is_object() || !event.contains("type") || !event["type"].is_string()) return false;
 const auto integer = [&](const char* key, int low, int high) {
  auto value = event.find(key);
  return value != event.end() && value->is_number_integer() && *value >= low && *value <= high;
 };
 const auto boolean = [&](const char* key, bool optional = false) {
  auto value = event.find(key);
  return value == event.end() ? optional : value->is_boolean();
 };
 const auto type = event["type"].get<std::string>();
 if (type == "release") return event.size() == 1;
 if (type == "move") return event.size() == 3 && integer("x", 0, 65535) && integer("y", 0, 65535);
 if (type == "button") return event.size() == 3 && integer("button", 1, 3) && boolean("down");
 if (type == "key") return event.size() == 3 + event.count("extended") && integer("code", 1, 255) && boolean("down") && boolean("extended", true);
 if (type == "wheel") return event.size() == 2 + event.count("horizontal") && integer("delta", -1200, 1200) && boolean("horizontal", true);
 if (type != "text" || event.size() != 2 || !event.contains("text") || !event["text"].is_string()) return false;
 const auto& text = event["text"].get_ref<const std::string&>();
 return !text.empty() && text.size() <= 8192 && text.find('\0') == std::string::npos;
}

inline bool valid_input_batch(const InputEvent& batch) {
 if (!batch.is_array() || batch.empty() || batch.size() > input_event_limit) return false;
 for (const auto& event : batch) if (!valid_input(event)) return false;
 return batch.dump().size() <= input_batch_limit;
}

// Shared by every viewer. Focus loss, overload and stale backlogs discard
// pending actions and release held input before accepting fresh actions.
class InputQueue {
 struct Entry { InputEvent event; std::size_t bytes; std::uint64_t time; };
 std::mutex mutex_;
 std::deque<Entry> entries_;
 std::size_t bytes_ = 2;
 void reset() { entries_.clear(); bytes_ = 2; }
 void append(InputEvent event, std::uint64_t now) {
  auto bytes = event.dump().size() + 1;
  bytes_ += bytes; entries_.push_back({std::move(event), bytes, now});
 }
 void release(std::uint64_t now) { reset(); append({{"type", "release"}}, now); }
 bool expired(std::uint64_t now) const {
  return !entries_.empty() && (now < entries_.front().time || now - entries_.front().time >= input_max_age_ms);
 }
public:
 bool push(InputEvent event, std::uint64_t now = monotonic_time_ms()) {
  if (!valid_input(event)) return false;
  auto bytes = event.dump().size() + 1;
  std::lock_guard lock(mutex_);
  if (event["type"] == "release") { release(now); return true; }
  if (expired(now)) release(now);
  if (event["type"] == "move" && !entries_.empty() && entries_.back().event["type"] == "move") {
   bytes_ -= entries_.back().bytes; entries_.pop_back();
  }
  if (entries_.size() >= input_event_limit || bytes_ + bytes > input_batch_limit) release(now);
  append(std::move(event), now); return true;
 }
 InputEvent take(std::uint64_t now = monotonic_time_ms()) {
  std::lock_guard lock(mutex_);
  if (expired(now)) release(now);
  auto batch = InputEvent::array();
  for (auto& entry : entries_) batch.push_back(std::move(entry.event));
  reset(); return batch;
 }
 void clear() { std::lock_guard lock(mutex_); reset(); }
};
}
