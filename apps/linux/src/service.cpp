#include "service.hpp"
#include <filesystem>
#include <stdexcept>
#include <sys/stat.h>
#include <unistd.h>
namespace netgo::desktop_linux {
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
