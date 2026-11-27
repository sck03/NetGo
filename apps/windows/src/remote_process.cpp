#include "remote_process.hpp"

namespace netgo {
UINT remoteShutdownMessage() {
    static const UINT message = RegisterWindowMessageW(L"NetGo.RemoteShutdown.v2");
    return message;
}
void RemoteProcess::launch(const std::string& site, bool host) {
    if (auto existing = FindWindowW(L"NetGoRemoteWindow", nullptr)) {
        if (!host) { ShowWindow(existing, SW_RESTORE); SetForegroundWindow(existing); }
        return;
    }
    if (process_.value && WaitForSingleObject(process_, 0) == WAIT_TIMEOUT) return;
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
}
void RemoteProcess::stop() {
    if (auto window = FindWindowW(L"NetGoRemoteWindow", nullptr))
        PostMessageW(window, remoteShutdownMessage(), 0, 0);
}
}
