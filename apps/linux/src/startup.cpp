#include "startup.hpp"
#include <glib.h>
#include <filesystem>
#include <stdexcept>
#include <string>
namespace netgo::desktop_linux {
void configureStartup(bool enabled) {
    auto directory = std::filesystem::path(g_get_user_config_dir()) / "autostart";
    auto target = directory / "org.netgo.desktop.desktop";
    if (!enabled) {
        std::error_code error;
        std::filesystem::remove(target, error);
        if (error)
            throw std::runtime_error("无法移除登录启动项");
        return;
    }
    std::filesystem::create_directories(directory);
    auto executable = std::filesystem::canonical("/proc/self/exe").string();
    std::string quoted = "\"";
    for (char c : executable) {
        if (c == '\n' || c == '\r')
            throw std::runtime_error("invalid_startup_path");
        if (c == '%')
            quoted += '%';
        if (c == '\\' || c == '"' || c == '$' || c == 96)
            quoted += '\\';
        quoted += c;
    }
    quoted += '"';
    auto content = "[Desktop Entry]\nType=Application\nName=NetGo Unattended\nExec=" + quoted +
                   "\nTerminal=false\nX-GNOME-Autostart-enabled=true\n";
    GError *error = nullptr;
    auto saved = g_file_set_contents_full(target.c_str(), content.c_str(), gssize(content.size()),
                                          G_FILE_SET_CONTENTS_CONSISTENT, 0600, &error);
    if (error)
        g_error_free(error);
    if (!saved)
        throw std::runtime_error("无法保存登录启动项");
}
}
