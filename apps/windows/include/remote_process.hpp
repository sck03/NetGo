#pragma once
#include "common.hpp"

namespace netgo {
UINT remoteShutdownMessage();
class RemoteProcess {
    Handle job_, process_;
public:
    void launch(const std::string& site, bool host);
    void stop();
};
}
