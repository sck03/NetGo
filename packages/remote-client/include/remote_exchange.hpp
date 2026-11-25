#pragma once
#include "remote_files.hpp"
#include <deque>
#include <functional>
#include <mutex>
#include <chrono>
namespace netgo::remote {
// Only the viewer worker owns file handles and protocol state. UI producers
// submit bounded commands; each chunk is acknowledged before the next is sent.
class Exchange {
  public:
    using Json = nlohmann::json;
    explicit Exchange(std::function<void(const Json &)> event) : event_(std::move(event)) {}
    bool push(const Json &command);
    Json next(uint32_t caps);
    void receive(const Json &reply);
    void reset() noexcept;

  private:
    void finish(const std::string &error = {});
    Json send(Json command);
    void advanceUpload();
    std::mutex mutex_;
    std::deque<Json> queue_;
    bool cancel_ = false, cancelling_ = false;
    std::function<void(const Json &)> event_;
    Json active_, pending_, ready_, chunk_;
    std::unique_ptr<Files> local_;
    std::string localToken_, remoteToken_, sha_;
    std::uint64_t id_ = 0, offset_ = 0, size_ = 0;
    std::chrono::steady_clock::time_point deadline_, progress_;
};
} // namespace netgo::remote
