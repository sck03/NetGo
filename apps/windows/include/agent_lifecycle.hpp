#pragma once
#include "common.hpp"

namespace netgo {
// Used only on the UI worker. Never restart a live but unresponsive agent, and
// never replay a mutating user command whose acknowledgement may have been lost.
class AgentLifecycle {
public:
    using Call = std::function<Json(const Json&, const std::function<bool()>&)>;
private:
    Clock::time_point retryAfter_{};
    bool ensureStarted();
    Call request_;
    std::function<bool()> start_;
public:
    explicit AgentLifecycle(Call request = pipeCall, std::function<bool()> start = {})
        : request_(std::move(request)), start_(std::move(start)) {}
    Json call(const Json& request, const std::function<bool()>& active, bool& recovered);
};
}
