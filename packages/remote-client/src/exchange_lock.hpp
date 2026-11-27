#pragma once
#include <filesystem>
#include <stdexcept>
#ifdef _WIN32
#include <windows.h>
#else
#include <cerrno>
#include <fcntl.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

namespace netgo::remote {
// One writer/reclaimer per exchange directory, including across processes.
// The OS releases the lock on a crash. Never unlink the lock file: replacing its
// inode would allow two writers to hold different locks for the same directory.
class ExchangeLock {
#ifdef _WIN32
    HANDLE handle_ = INVALID_HANDLE_VALUE;
#else
    int handle_ = -1;
#endif
public:
    explicit ExchangeLock(const std::filesystem::path &root) {
        const auto path = root / ".netgo-exchange.lock";
#ifdef _WIN32
        handle_ = CreateFileW(path.c_str(), GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ,
                              nullptr, OPEN_ALWAYS, FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
        if (handle_ == INVALID_HANDLE_VALUE) {
            if (GetLastError() == ERROR_SHARING_VIOLATION) return;
            throw std::runtime_error("remote_exchange_lock_failed");
        }
        BY_HANDLE_FILE_INFORMATION info{};
        if (!GetFileInformationByHandle(handle_, &info) || info.nNumberOfLinks != 1 ||
            (info.dwFileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY))) {
            CloseHandle(handle_);
            throw std::runtime_error("remote_exchange_lock_failed");
        }
#else
        handle_ = ::open(path.c_str(), O_RDWR | O_CREAT | O_CLOEXEC | O_NOFOLLOW | O_NONBLOCK, 0600);
        if (handle_ < 0) throw std::runtime_error("remote_exchange_lock_failed");
        struct stat info{};
        if (fstat(handle_, &info) || !S_ISREG(info.st_mode) || info.st_nlink != 1) {
            ::close(handle_);
            throw std::runtime_error("remote_exchange_lock_failed");
        }
        if (flock(handle_, LOCK_EX | LOCK_NB)) {
            const int error = errno;
            ::close(handle_);
            handle_ = -1;
            if (error != EWOULDBLOCK && error != EAGAIN)
                throw std::runtime_error("remote_exchange_lock_failed");
        }
#endif
    }
    ExchangeLock(const ExchangeLock &) = delete;
    ExchangeLock &operator=(const ExchangeLock &) = delete;
    bool acquired() const {
#ifdef _WIN32
        return handle_ != INVALID_HANDLE_VALUE;
#else
        return handle_ >= 0;
#endif
    }
    ~ExchangeLock() {
#ifdef _WIN32
        if (acquired()) CloseHandle(handle_);
#else
        if (acquired()) ::close(handle_);
#endif
    }
};
} // namespace netgo::remote
