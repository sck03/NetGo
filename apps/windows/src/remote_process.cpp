#include "remote_process.hpp"

namespace netgo {
UINT remoteShutdownMessage() {
    static const UINT message = RegisterWindowMessageW(L"NetGo.RemoteShutdown.v2");
    return message;
}
std::wstring remoteWindowClass(const std::string& site) { return wide("NetGoRemoteWindow-" + site); }
void RemoteProcess::start(const std::string& site, bool host) {
    if (auto existing = FindWindowW(remoteWindowClass(site).c_str(), nullptr)) {
        if (!host) { ShowWindow(existing, SW_RESTORE); SetForegroundWindow(existing); }
        return;
    }
    if (!job_.value) {
        Handle job(CreateJobObjectW(nullptr, nullptr));
        JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
        limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        if (!job.value || !SetInformationJobObject(job, JobObjectExtendedLimitInformation,
                                                  &limits, sizeof(limits)))
            throw Error("remote_program_missing");
        job_.value = job.value;
        job.value = nullptr;
    }
    const auto binary = executableDir() / L"NetGo.exe";
    auto command = L"\"" + binary.wstring() + L"\" --remote --site " + wide(site);
    if (host) command += L" --host";
    STARTUPINFOW start{};
    start.cb = sizeof(start);
    start.dwFlags = STARTF_USESHOWWINDOW;
    start.wShowWindow = host ? SW_SHOWMINIMIZED : SW_SHOWNORMAL;
    PROCESS_INFORMATION child{};
    if (!CreateProcessW(binary.c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_SUSPENDED,
                        nullptr, executableDir().c_str(), &start, &child))
        throw Error("remote_program_missing");
    Handle thread(child.hThread), process(child.hProcess);
    if (!AssignProcessToJobObject(job_, process) || ResumeThread(thread) == DWORD(-1)) {
        TerminateProcess(process, 1);
        throw Error("remote_program_missing");
    }
    if (process_.value) CloseHandle(process_.value);
    process_.value = process.value;
    process.value = nullptr;
    site_ = site;
}
void RemoteProcess::requestStop() {
    if (!process_.value || stopDeadline_ != Clock::time_point{}) return;
    stopDeadline_ = Clock::now() + std::chrono::seconds(5);
    auto window = FindWindowW(remoteWindowClass(site_).c_str(), nullptr);
    DWORD owner = 0;
    if (window) GetWindowThreadProcessId(window, &owner);
    if (owner == GetProcessId(process_))
        PostMessageW(window, remoteShutdownMessage(), 0, 0);
}
void RemoteProcess::launch(const std::string& site, bool host) {
    if (site == site_ && process_.value && WaitForSingleObject(process_, 0) == WAIT_TIMEOUT &&
        stopDeadline_ == Clock::time_point{}) {
        if (!host) if (auto window = FindWindowW(remoteWindowClass(site).c_str(), nullptr)) {
            ShowWindow(window, SW_RESTORE); SetForegroundWindow(window);
        }
        return;
    }
    pendingSite_ = site; pendingHost_ = host;
    requestStop();
    poll();
}
void RemoteProcess::stop() { pendingSite_.clear(); requestStop(); }
void RemoteProcess::poll() {
    if (process_.value) {
        if (WaitForSingleObject(process_, 0) == WAIT_TIMEOUT) {
            if (stopDeadline_ != Clock::time_point{} && Clock::now() >= stopDeadline_)
                TerminateJobObject(job_, 1);
            return;
        }
        CloseHandle(process_.value); process_.value = nullptr;
        CloseHandle(job_.value); job_.value = nullptr;
        site_.clear(); stopDeadline_ = {};
    }
    if (!pendingSite_.empty()) {
        auto next = std::move(pendingSite_); pendingSite_.clear();
        start(next, pendingHost_);
    }
}
}
