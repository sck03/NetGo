#include "service.hpp"
#include <filesystem>
#include <algorithm>
#include <cstring>
#include <csignal>
#include <stdexcept>
#include <sys/stat.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
namespace netgo::desktop_linux {
namespace {
bool available(const std::string &path) {
    sockaddr_un address{};
    if (path.size() >= sizeof(address.sun_path)) return false;
    address.sun_family = AF_UNIX;
    std::memcpy(address.sun_path, path.c_str(), path.size() + 1);
    const int fd = socket(AF_UNIX, SOCK_STREAM | SOCK_NONBLOCK | SOCK_CLOEXEC, 0);
    if (fd < 0) return false;
    const bool ready = connect(fd, reinterpret_cast<sockaddr *>(&address), sizeof(address)) == 0;
    close(fd);
    return ready;
}
GSubprocess *launchService(const std::string &root, const std::string &socket) {
    auto folder = std::filesystem::canonical("/proc/self/exe").parent_path();
    auto binary = folder / "netgo-client-service";
    if (std::filesystem::exists("/opt/netgo-client/bin/netgo-client-service"))
        binary = "/opt/netgo-client/bin/netgo-client-service";
    GError *error = nullptr;
    auto service = g_subprocess_new(
        GSubprocessFlags(G_SUBPROCESS_FLAGS_STDOUT_SILENCE | G_SUBPROCESS_FLAGS_STDERR_SILENCE), &error,
        binary.c_str(), "--root", root.c_str(), "--socket", socket.c_str(), "--daemon",
        "/run/netgo-client/netbird.sock", nullptr);
    if (error) {
        std::string message = error->message;
        g_error_free(error);
        throw std::runtime_error(message);
    }
    return service;
}
}
ServiceSupervisor::~ServiceSupervisor() { stop(); }
void ServiceSupervisor::stop() {
    if (!process_) return;
    if (!exited_->load()) g_subprocess_send_signal(process_, SIGTERM);
    g_object_unref(process_);
    process_ = nullptr;
}
void ServiceSupervisor::restart() {
    if (process_ && !exited_->load()) {
        g_subprocess_send_signal(process_, SIGTERM);
        // Network replacement runs on the connection worker, away from GTK.
        for (unsigned i = 0; i < 100 && !exited_->load(); ++i) g_usleep(100000);
        if (!exited_->load()) g_subprocess_force_exit(process_);
    }
    stop();
    retryAt_ = 0;
    failures_ = 0;
}
bool ServiceSupervisor::ensure(const std::string &root, const std::string &socket) {
    if (process_ && exited_->load()) stop();
    if (available(socket)) {
        failures_ = 0;
        retryAt_ = 0;
        return true;
    }
    if (process_ || g_get_monotonic_time() < retryAt_) return false;
    retryAt_ = g_get_monotonic_time() + (gint64(1) << std::min(failures_, 5u)) * 1000000;
    failures_ = std::min(failures_ + 1, 5u);
    process_ = launchService(root, socket);
    exited_ = std::make_shared<std::atomic_bool>(false);
    auto completion = new std::shared_ptr<std::atomic_bool>(exited_);
    g_subprocess_wait_async(process_, nullptr, [](GObject *source, GAsyncResult *result, gpointer data) {
        std::unique_ptr<std::shared_ptr<std::atomic_bool>> exit(static_cast<std::shared_ptr<std::atomic_bool> *>(data));
        g_subprocess_wait_finish(G_SUBPROCESS(source), result, nullptr);
        (*exit)->store(true);
    }, completion);
    return false;
}
bool ensureNetwork() {
    struct stat info{};
    if (stat("/run/netgo-client/netbird.sock", &info) == 0 && S_ISSOCK(info.st_mode) &&
        info.st_uid == getuid() && std::filesystem::exists("/opt/netgo-client/bin/netgo-client-service"))
        return false;
    auto folder = std::filesystem::canonical("/proc/self/exe").parent_path();
    auto binary = folder / "netgo-client-service";
    auto owner = std::to_string(getuid());
    GError *error = nullptr;
    auto setup = g_subprocess_new(
        GSubprocessFlags(G_SUBPROCESS_FLAGS_STDOUT_SILENCE | G_SUBPROCESS_FLAGS_STDERR_SILENCE), &error,
        "pkexec", binary.c_str(), "--network", "install", "--source", folder.c_str(), "--owner",
        owner.c_str(), nullptr);
    if (!setup) {
        if (error)
            g_error_free(error);
        throw std::runtime_error("无法打开系统管理员授权，请检查桌面系统的授权组件");
    }
    auto ok = g_subprocess_wait_check(setup, nullptr, &error);
    g_object_unref(setup);
    if (!ok) {
        if (error)
            g_error_free(error);
        throw std::runtime_error("网络组件安装未完成，请确认管理员授权后重试");
    }
    return true;
}
}
