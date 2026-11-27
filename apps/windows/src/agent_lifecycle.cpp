#include "agent_lifecycle.hpp"

namespace netgo {
bool AgentLifecycle::ensureStarted() {
    Handle mutex(OpenMutexW(SYNCHRONIZE | MUTEX_MODIFY_STATE, FALSE,
                            wide("Local\\NetGoNativeAgent-" + sid()).c_str()));
    if (mutex.value) {
        auto state = WaitForSingleObject(mutex, 0);
        if (state != WAIT_OBJECT_0 && state != WAIT_ABANDONED) return false;
        ReleaseMutex(mutex);
    } else if (GetLastError() != ERROR_FILE_NOT_FOUND) {
        throw Error("agent_identity_failed");
    }
    if (Clock::now() < retryAfter_) return false;
    retryAfter_ = Clock::now() + std::chrono::seconds(5);
    auto file = executableDir() / L"NetGo.exe";
    auto command = L"\"" + file.wstring() + L"\" --agent";
    STARTUPINFOW start{};
    start.cb = sizeof(start);
    PROCESS_INFORMATION child{};
    if (!CreateProcessW(file.c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
                        nullptr, file.parent_path().c_str(), &start, &child))
        throw Error("agent_unavailable");
    Handle process(child.hProcess), thread(child.hThread);
    return true;
}

Json AgentLifecycle::call(const Json& request, const std::function<bool()>& active, bool& recovered) {
    recovered = false;
    try { return request_(request, active); }
    catch (const Error& error) {
        auto action = request.value("action", "");
        auto reason = std::string(error.what());
        if ((reason != "agent_unavailable" && reason != "ipc_failed") ||
            (action != "status" && action != "profiles" && action != "shutdown") || !active()) throw;
        if (!(start_ ? start_() : ensureStarted())) throw;
    }
    auto deadline = Clock::now() + std::chrono::seconds(30);
    while (active() && Clock::now() < deadline) {
        try { auto result = request_(request, active); recovered = true; return result; }
        catch (const Error& error) {
            if (std::string(error.what()) != "agent_unavailable") throw;
        }
    }
    throw Error(active() ? "agent_unavailable" : "cancelled");
}
}
