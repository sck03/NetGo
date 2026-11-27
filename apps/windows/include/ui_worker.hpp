#pragma once
#include "common.hpp"
#include <condition_variable>
#include <deque>

namespace netgo {
struct UiResult {
    std::string action, site;
    Json value;
    std::string error;
    bool recovered = false;
};

// Owns IPC work and undelivered results; window messages carry no heap pointers.
class UiWorker {
    std::mutex mutex_;
    std::condition_variable ready_;
    std::deque<Json> jobs_;
    std::deque<std::unique_ptr<UiResult>> results_;
    std::thread thread_;
    bool stopped_ = true;
public:
    ~UiWorker();
    void start(HWND window, UINT message);
    bool submit(Json request);
    std::unique_ptr<UiResult> takeResult();
    void stop();
};
}
