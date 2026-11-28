#pragma once
#include "remote_files.hpp"
#include <deque>
#include <functional>
#include <mutex>
#include <chrono>
namespace netgo::remote {
// Only the viewer worker owns file handles and protocol state. UI producers
// submit bounded commands. Eight ordered chunks can be in flight; commits and
// cancellation wait for their acknowledgements and never publish partial files.
class Exchange {
  public:
    using Json = nlohmann::json;
    explicit Exchange(std::function<void(const Json &)> event, std::function<bool()> active = {})
        : event_(std::move(event)), allowed_(std::move(active)) {}
    bool push(const Json &command);
    Json next(uint32_t caps, std::chrono::steady_clock::time_point now = std::chrono::steady_clock::now());
    void receive(const Json &reply);
    // Worker-only: next protocol deadline, or max() while idle.
    std::chrono::steady_clock::time_point wakeAt();
    void reset() noexcept;

  private:
    void finish(const std::string &error = {});
    void discard(std::deque<Json> commands, const std::string &error);
    Json send(Json command);
    void advanceUpload();
    std::mutex mutex_;
    std::deque<Json> queue_, discarded_;
    bool cancel_ = false, cancelling_ = false;
    std::function<void(const Json &)> event_;
    std::function<bool()> allowed_;
    struct Pending { Json request; std::chrono::steady_clock::time_point deadline; };
    std::deque<Pending> pending_;
    Json active_, ready_;
    std::unique_ptr<Files> local_;
    std::string localToken_, remoteToken_, sha_, failure_;
    std::uint64_t id_ = 0, retired_ = 0, blockedCancel_ = 0, offset_ = 0, sent_ = 0, size_ = 0;
    bool dataDone_ = false;
    std::chrono::steady_clock::time_point progress_;
};
} // namespace netgo::remote
