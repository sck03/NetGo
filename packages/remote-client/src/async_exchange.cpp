#include "remote_async_exchange.hpp"
#include "netgo_remote.h"
#ifdef _WIN32
#include <windows.h>
#endif

namespace netgo::remote {
namespace {
int64_t monotonicMS() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now().time_since_epoch()).count();
}
uint32_t commandCapability(const nlohmann::json& request) {
    auto action = request.value("action", "");
    if (action == "file.begin" || action == "file.write" || action == "file.commit") return NG_REMOTE_UPLOAD;
    if (action == "file.open" || action == "file.read" || action == "file.list") return NG_REMOTE_DOWNLOAD;
    if (action == "file.cancel") return NG_REMOTE_VIEW;
    return NG_REMOTE_CLIPBOARD;
}
}
AsyncExchange::AsyncExchange(std::function<void(const Json&)> event)
    : event_(std::move(event)), exchange_([this](const Json& value) {
        std::lock_guard lock(mutex_);
        // Coalesce progress only. Completion events release platform staging resources.
        if (!value.value("done", false) && !events_.empty() && !events_.back().value("done", false))
            events_.back() = value;
        else events_.push_back(value);
    }, [this] { return !cancelled_ && !stopped_ && (caps_.load() & NG_REMOTE_DOWNLOAD); }) {}

AsyncExchange::~AsyncExchange() { reset(); }
bool AsyncExchange::push(const Json& command) {
    if (cancelled_) return false;
    auto accepted = exchange_.push(command);
    if (accepted) {
        if (command.value("action", "") == "cancel") cancelIO();
        wake_.notify_one();
    }
    return accepted;
}
void AsyncExchange::cancelIO() {
#ifdef _WIN32
    std::lock_guard lock(mutex_);
    if (auto thread = ioThread_.load()) CancelSynchronousIo(thread);
#endif
}
void AsyncExchange::events() {
    std::deque<Json> values;
    { std::lock_guard lock(mutex_); values.swap(events_); }
    for (const auto& value : values) event_(value);
}
AsyncExchange::Json AsyncExchange::next(uint32_t caps) {
    if (cancelled_) return {};
    auto previous = caps_.exchange(caps);
    const auto started = ioStarted_.load();
    if ((previous & ~caps) || (started && monotonicMS() - started >= 10000)) cancelIO();
    if (!worker_.joinable()) {
        stopped_ = false; finished_ = false;
        worker_ = std::thread([this] { run(); });
    }
    events();
    std::lock_guard lock(mutex_);
    if (!failure_.empty()) throw std::runtime_error(failure_);
    // Capabilities may have changed while IO was running. Never transmit an old
    // authorized request after revocation; its pending ID expires into file.cancel.
    while (!requests_.empty() && !(caps & commandCapability(requests_.front()))) requests_.pop_front();
    if (requests_.empty()) return {};
    auto result = std::move(requests_.front()); requests_.pop_front();
    wake_.notify_one();
    return result;
}
void AsyncExchange::receive(Json reply) {
    std::lock_guard lock(mutex_);
    // At most eight issued requests plus the timeout cancellation can be outstanding.
    if (replies_.size() >= 16) throw std::runtime_error("remote_command_overflow");
    replies_.push_back(std::move(reply)); wake_.notify_one();
}
void AsyncExchange::run() noexcept {
#ifdef _WIN32
    ioThread_ = OpenThread(THREAD_TERMINATE, FALSE, GetCurrentThreadId());
#endif
    try {
        while (!stopped_ && !cancelled_) {
            std::deque<Json> replies;
            { std::lock_guard lock(mutex_); replies.swap(replies_); }
            for (const auto& reply : replies) {
                if (stopped_) break;
                ioStarted_ = monotonicMS();
                exchange_.receive(reply);
                ioStarted_ = 0;
            }
            for (unsigned count = 0; count < 8 && !stopped_; ++count) {
                { std::lock_guard lock(mutex_); if (requests_.size() >= 8 || events_.size() >= 16) break; }
                ioStarted_ = monotonicMS();
                auto request = exchange_.next(caps_);
                ioStarted_ = 0;
                if (request.is_null()) break;
                std::lock_guard lock(mutex_);
                if (!stopped_) requests_.push_back(std::move(request));
            }
            std::unique_lock lock(mutex_);
            wake_.wait_for(lock, std::chrono::milliseconds(10), [this] { return stopped_ || !replies_.empty(); });
        }
    } catch (const std::exception& error) {
        std::lock_guard lock(mutex_); failure_ = error.what();
    }
    exchange_.reset();
    ioStarted_ = 0;
    finished_ = true;
    wake_.notify_all();
}
void AsyncExchange::reset() noexcept {
    cancel();
    if (worker_.joinable()) {
        // Repeated cancellation covers the race between the flag check and entering
        // a synchronous Windows filesystem call. No thread is detached from its owner.
        while (!finished_) {
#ifdef _WIN32
            cancelIO();
#endif
            std::unique_lock lock(mutex_);
            wake_.wait_for(lock, std::chrono::milliseconds(20), [this] { return finished_.load(); });
        }
        worker_.join();
#ifdef _WIN32
        { std::lock_guard lock(mutex_); if (auto thread = ioThread_.exchange(nullptr)) CloseHandle(thread); }
#endif
    } else exchange_.reset();
    try { events(); } catch (...) {}
    std::lock_guard lock(mutex_);
    requests_.clear(); replies_.clear(); events_.clear(); failure_.clear();
}
void AsyncExchange::cancel() {
    cancelled_ = true; stopped_ = true; caps_ = 0;
    wake_.notify_all(); cancelIO();
}
}
