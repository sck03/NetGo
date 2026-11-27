#include "ui_worker.hpp"

namespace netgo {
UiWorker::~UiWorker() { stop(); }

void UiWorker::start(HWND window, UINT message) {
    stop();
    stopped_ = false;
    thread_ = std::thread([this, window, message] {
        for (;;) {
            Json job;
            {
                std::unique_lock lock(mutex_);
                ready_.wait(lock, [this] { return stopped_ || !jobs_.empty(); });
                if (stopped_) return;
                job = std::move(jobs_.front());
                jobs_.pop_front();
            }
            auto result = std::make_unique<UiResult>();
            result->action = job.at("action");
            result->site = job.value("site", "");
            try { result->value = pipeCall(job, [this] { std::lock_guard lock(mutex_); return !stopped_; }); }
            catch (const std::exception &e) { result->error = e.what(); }
            job = Json::object();
            std::lock_guard lock(mutex_);
            if (stopped_) return;
            results_.push_back(std::move(result));
            if (!PostMessageW(window, message, 0, 0)) results_.pop_back();
        }
    });
}

bool UiWorker::submit(Json request) {
    std::lock_guard lock(mutex_);
    if (stopped_ || jobs_.size() + results_.size() >= 32) return false;
    jobs_.push_back(std::move(request));
    ready_.notify_one();
    return true;
}

std::unique_ptr<UiResult> UiWorker::takeResult() {
    std::lock_guard lock(mutex_);
    if (results_.empty()) return {};
    auto result = std::move(results_.front());
    results_.pop_front();
    return result;
}

void UiWorker::stop() {
    {
        std::lock_guard lock(mutex_);
        stopped_ = true;
        jobs_.clear();
    }
    ready_.notify_one();
    if (thread_.joinable()) thread_.join();
    std::lock_guard lock(mutex_);
    results_.clear();
}
}
