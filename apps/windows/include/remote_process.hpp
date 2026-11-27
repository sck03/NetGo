#pragma once
#include "common.hpp"

namespace netgo {
UINT remoteShutdownMessage();
std::wstring remoteWindowClass(const std::string& site);
class RemoteProcess {
    Handle job_, process_;
    std::string site_, pendingSite_;
    bool pendingHost_ = false;
    Clock::time_point stopDeadline_{};
    void start(const std::string& site, bool host);
    void requestStop();
public:
    void launch(const std::string& site, bool host);
    void stop();
    void poll();
};
}
