#pragma once
#include <atomic>
#include <chrono>
#include <cstdint>
#include <condition_variable>
#include <deque>
#include <functional>
#include <mutex>
#include <stdexcept>
#include <thread>
#include <utility>
#include <nlohmann/json.hpp>
#ifdef _WIN32
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#endif

namespace netgo::remote {
// Started on first use, one bounded worker owns file/clipboard IO. Cancellation
// invalidates both the executing command and queued work without losing replies.
class CommandWorker {
    using Json = nlohmann::json;
    using Active = std::function<bool()>;
    std::function<Json(const Json&, const Active&)> execute_;
    std::function<void()> cleanup_;
    std::mutex mutex_;
    std::condition_variable wake_;
    struct Request { Json value; uint64_t generation; };
    std::deque<Request> requests_;
    std::deque<Json> results_;
    std::atomic_bool stopped_{false}, finished_{true};
    std::atomic_uint64_t generation_{0};
#ifdef _WIN32
    std::atomic<HANDLE> ioThread_{nullptr};
#endif
    bool cancel_ = false;
    size_t outstanding_ = 0;
    bool cancellationOutstanding_ = false;
    std::thread thread_;
    void cancelIO() {
#ifdef _WIN32
        if (auto handle = ioThread_.load()) CancelSynchronousIo(handle);
#endif
    }
    void run() {
#ifdef _WIN32
        ioThread_ = OpenThread(THREAD_TERMINATE, FALSE, GetCurrentThreadId());
#endif
        for (;;) {
            Request work;
            bool cancel;
            {
                std::unique_lock lock(mutex_);
                wake_.wait(lock, [&] { return stopped_ || cancel_ || !requests_.empty(); });
                if (stopped_) break;
                cancel = std::exchange(cancel_, false);
                if (!requests_.empty()) { work = std::move(requests_.front()); requests_.pop_front(); }
            }
            if (cancel) cleanup_();
            const auto& request = work.value;
            if (request.is_null()) continue;
            Json response = {{"id", request.at("id")}, {"_action", ""}};
            try {
                response["_action"] = request.value("action", "");
                auto active = [&] { return !stopped_ && generation_ == work.generation; };
                if (!active()) throw std::runtime_error("remote_command_cancelled");
                response["result"] = execute_(request, active);
            }
            catch (const std::exception& e) { cleanup_(); response["error"] = e.what(); }
            std::lock_guard lock(mutex_);
            if (!stopped_) results_.push_back(std::move(response));
        }
        cleanup_();
        { std::lock_guard lock(mutex_); finished_ = true; }
        wake_.notify_all();
    }
public:
    CommandWorker(std::function<Json(const Json&, const Active&)> execute, std::function<void()> cleanup)
        : execute_(std::move(execute)), cleanup_(std::move(cleanup)) {}
    ~CommandWorker() {
        { std::lock_guard lock(mutex_); stopped_ = true; requests_.clear(); }
        wake_.notify_one();
        if (!thread_.joinable()) return;
        while (!finished_) {
            cancelIO();
            std::unique_lock lock(mutex_);
#ifdef _WIN32
            // Repeat to cover the race between cancellation and entering IO.
            wake_.wait_for(lock, std::chrono::milliseconds(20), [&] { return finished_.load(); });
#else
            wake_.wait(lock, [&] { return finished_.load(); });
#endif
        }
        thread_.join();
#ifdef _WIN32
        if (auto handle = ioThread_.exchange(nullptr)) CloseHandle(handle);
#endif
    }
    bool submit(Json request) {
        if (!request.is_object() || !request.contains("id") || !request.at("id").is_number_unsigned())
            return false;
        std::lock_guard lock(mutex_);
        const auto action = request.find("action");
        const bool cancellation = action != request.end() && action->is_string() && *action == "file.cancel";
        // One reserved cancellation slot lets a timed-out full transfer window
        // drain without treating disk backpressure as a media protocol failure.
        if (stopped_ || (cancellation ? cancellationOutstanding_ || outstanding_ >= 9
                                     : outstanding_ >= 8)) return false;
        if (!thread_.joinable()) {
            finished_ = false;
            try { thread_ = std::thread([this] { run(); }); }
            catch (...) { finished_ = true; throw; }
        }
        if (cancellation) { cancellationOutstanding_ = true; ++generation_; cancelIO(); }
        requests_.push_back({std::move(request), generation_.load()});
        ++outstanding_;
        wake_.notify_one();
        return true;
    }
    Json take() {
        std::lock_guard lock(mutex_);
        if (results_.empty()) return {};
        auto result = std::move(results_.front()); results_.pop_front(); --outstanding_;
        if (result.value("_action", "") == "file.cancel") cancellationOutstanding_ = false;
        return result;
    }
    void cancel() {
        std::lock_guard lock(mutex_);
        cancel_ = true;
        ++generation_;
        cancelIO();
        wake_.notify_one();
    }
};
}
