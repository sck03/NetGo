#include "engine_channel.hpp"
#include <sddl.h>

namespace netgo {
EngineChannel::EngineChannel(bool parentWrites) {
    auto name = wide("\\\\.\\pipe\\netgo-engine-" + randomID());
    PSECURITY_DESCRIPTOR descriptor = nullptr;
    auto acl = wide("D:P(A;;GA;;;" + sid() + ")");
    if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(), SDDL_REVISION_1,
                                                              &descriptor, nullptr))
        throw Error("engine_pipe_failed");
    std::unique_ptr<void, decltype(&LocalFree)> guard(descriptor, LocalFree);
    SECURITY_ATTRIBUTES security{sizeof(security), descriptor, FALSE};
    parent_.value = CreateNamedPipeW(name.c_str(),
        (parentWrites ? PIPE_ACCESS_OUTBOUND : PIPE_ACCESS_INBOUND) |
            FILE_FLAG_OVERLAPPED | FILE_FLAG_FIRST_PIPE_INSTANCE,
        PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS,
        1, 65536, 65536, 0, &security);
    if (parent_.value == INVALID_HANDLE_VALUE) throw Error("engine_pipe_failed");
    security.bInheritHandle = TRUE;
    child_.value = CreateFileW(name.c_str(), parentWrites ? GENERIC_READ : GENERIC_WRITE,
                              0, &security, OPEN_EXISTING, 0, nullptr);
    if (child_.value == INVALID_HANDLE_VALUE) throw Error("engine_pipe_failed");
    Handle event(CreateEventW(nullptr, TRUE, FALSE, nullptr));
    OVERLAPPED connected{};
    connected.hEvent = event;
    // CreateFile already connected this private instance; no external client wait.
    if (!event.value || (!ConnectNamedPipe(parent_, &connected) && GetLastError() != ERROR_PIPE_CONNECTED))
        throw Error("engine_pipe_failed");
}

void EngineChannel::closeChild() {
    if (child_.value && child_.value != INVALID_HANDLE_VALUE) CloseHandle(child_.value);
    child_.value = nullptr;
}

DWORD EngineChannel::transfer(bool write, void* bytes, DWORD size, Clock::time_point deadline,
                              const std::atomic_bool& cancel) {
    if (cancel || Clock::now() >= deadline) throw Error(cancel ? "cancelled" : "engine_timeout");
    Handle event(CreateEventW(nullptr, TRUE, FALSE, nullptr));
    if (!event.value) throw Error("engine_pipe_failed");
    OVERLAPPED operation{};
    operation.hEvent = event;
    DWORD count = 0;
    BOOL done = write ? WriteFile(parent_, bytes, size, &count, &operation)
                      : ReadFile(parent_, bytes, size, &count, &operation);
    if (!done && GetLastError() == ERROR_IO_PENDING) {
        for (;;) {
            auto wait = WaitForSingleObject(event, 20);
            if (wait == WAIT_OBJECT_0) break;
            if (wait != WAIT_TIMEOUT || cancel || Clock::now() >= deadline) {
                CancelIoEx(parent_, &operation);
                // Drain cancellation before releasing the OVERLAPPED and caller buffer.
                GetOverlappedResult(parent_, &operation, &count, TRUE);
                throw Error(cancel ? "cancelled" : "engine_timeout");
            }
        }
        done = GetOverlappedResult(parent_, &operation, &count, FALSE);
    }
    if (!done || !count) throw Error(write ? "engine_input_failed" : "engine_protocol_failed");
    return count;
}
}
