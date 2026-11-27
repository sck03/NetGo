#pragma once
#include "common.hpp"

namespace netgo {
struct UiPreferences {
    std::string selected, autoConnectSite, unattendedSite;
    bool startup = false, autoConnect = false, closeToTray = true, minimizeToTray = false;
    bool operator==(const UiPreferences&) const = default;
    static UiPreferences decode(const Json& value);
    static UiPreferences load(const std::filesystem::path& root = dataRoot());
    static UiPreferences update(const std::function<void(UiPreferences&)>& edit,
                                const std::filesystem::path& root = dataRoot());
    static UINT changedMessage();
    void normalize();
    void forgetSite(const std::string& site);
};
}
