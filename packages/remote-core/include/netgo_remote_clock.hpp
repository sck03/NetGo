#pragma once
#include "netgo_remote.h"
#include <cstdint>
#include <mutex>
#include <stdexcept>

namespace netgo::remote {
// Implemented by the platform transport adapter; includes suspend duration.
std::uint64_t monotonic_time_ms() noexcept;
// Anchor only to the authenticated control response, never a media peer or the
// local wall clock. Charge the entire request duration, conservatively reducing
// the remaining lease instead of granting extra time to a delayed response.
class ServerClock final {
public:
 ServerClock(std::uint64_t unix_ms,std::uint64_t requested):unix_(unix_ms),anchor_(requested){
  if(!unix_ms||unix_ms>std::uint64_t(INT64_MAX))throw std::runtime_error("remote_clock_invalid");
 }
 void configure(ng_remote_options& options){
  options.clock_context=this;
  options.unix_ms=[](void* p)->uint64_t{return static_cast<ServerClock*>(p)->now();};
  options.monotonic_ms=[](void*)->uint64_t{return monotonic_time_ms();};
 }
 // A fresh authenticated renewal may recalibrate UTC; the gate's existing
 // monotonic deadline still fences an expired lease before accepting renewal.
 void observe(std::uint64_t unix_ms,std::uint64_t requested){
  if(!unix_ms||unix_ms>std::uint64_t(INT64_MAX)||requested>monotonic_time_ms())throw std::runtime_error("remote_clock_invalid");
  std::lock_guard<std::mutex> lock(mutex_);unix_=unix_ms;anchor_=requested;
 }
 std::uint64_t now()const{
  std::lock_guard<std::mutex> lock(mutex_);
  auto current=monotonic_time_ms();if(current<anchor_||current-anchor_>UINT64_MAX-unix_)return UINT64_MAX;
  return unix_+(current-anchor_);
 }
private:
 std::uint64_t unix_,anchor_;mutable std::mutex mutex_;
};
}
