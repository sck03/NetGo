#pragma once
#include "remote_exchange.hpp"
#include <atomic>
#include <condition_variable>
#include <thread>

namespace netgo::remote {
// Media only moves bounded messages. A single worker owns ordered file IO and
// protocol state; events are delivered by the media caller, never from the IO worker.
class AsyncExchange {
    using Json = nlohmann::json;
    std::function<void(const Json&)> event_;
    std::mutex mutex_;
    std::condition_variable wake_;
    std::deque<Json> requests_, replies_, events_;
    std::atomic_bool stopped_{true}, finished_{true};
    std::atomic_bool cancelled_{false}, submitted_{false};
    uint64_t wakeVersion_ = 0;
    std::atomic_uint32_t caps_{0};
    std::atomic<void*> ioThread_{nullptr};
    std::atomic<int64_t> ioStarted_{0};
    std::string failure_;
    Exchange exchange_;
    std::thread worker_;
    void run() noexcept;
    void events();
    void cancelIO();
    void notify();
public:
    explicit AsyncExchange(std::function<void(const Json&)> event);
    ~AsyncExchange();
    bool push(const Json& command);
    Json next(uint32_t caps);
    void receive(Json reply);
    void cancel();
    void reset() noexcept;
};
}
