#pragma once
#include <atomic>
#include <condition_variable>
#include <deque>
#include <functional>
#include <mutex>
#include <thread>
#include <utility>
#include <nlohmann/json.hpp>

namespace netgo::remote {
// One bounded worker owns file/clipboard commands; media and authorization never
// wait for disk IO. Destruction cancels queued work and joins before its owners die.
class CommandWorker {
    using Json = nlohmann::json;
    using Active = std::function<bool()>;
    std::function<Json(const Json&, const Active&)> execute_;
    std::function<void()> cleanup_;
    std::mutex mutex_;
    std::condition_variable wake_;
    std::deque<Json> requests_, results_;
    std::atomic_bool stopped_{false};
    bool cancel_ = false;
    size_t outstanding_ = 0;
    std::thread thread_;
    void run() {
        for (;;) {
            Json request;
            bool cancel;
            {
                std::unique_lock lock(mutex_);
                wake_.wait(lock, [&] { return stopped_ || cancel_ || !requests_.empty(); });
                if (stopped_) break;
                cancel = std::exchange(cancel_, false);
                if (!requests_.empty()) { request = std::move(requests_.front()); requests_.pop_front(); }
            }
            if (cancel) cleanup_();
            if (request.is_null()) continue;
            Json response = {{"id", request.at("id")}, {"_action", ""}};
            try {
                response["_action"] = request.value("action", "");
                response["result"] = execute_(request, [&] { return !stopped_; });
            }
            catch (const std::exception& e) { cleanup_(); response["error"] = e.what(); }
            std::lock_guard lock(mutex_);
            if (!stopped_) results_.push_back(std::move(response));
        }
        cleanup_();
    }
public:
    CommandWorker(std::function<Json(const Json&, const Active&)> execute, std::function<void()> cleanup)
        : execute_(std::move(execute)), cleanup_(std::move(cleanup)), thread_([this] { run(); }) {}
    ~CommandWorker() {
        { std::lock_guard lock(mutex_); stopped_ = true; requests_.clear(); }
        wake_.notify_one();
        thread_.join();
    }
    bool submit(Json request) {
        if (!request.is_object() || !request.contains("id") || !request.at("id").is_number_unsigned())
            return false;
        std::lock_guard lock(mutex_);
        if (stopped_ || outstanding_ >= 8) return false;
        requests_.push_back(std::move(request));
        ++outstanding_;
        wake_.notify_one();
        return true;
    }
    Json take() {
        std::lock_guard lock(mutex_);
        if (results_.empty()) return {};
        auto result = std::move(results_.front()); results_.pop_front(); --outstanding_;
        return result;
    }
    void cancel() {
        std::lock_guard lock(mutex_);
        cancel_ = true;
        wake_.notify_one();
    }
};
}
