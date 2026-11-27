#pragma once
#include "common.hpp"

namespace netgo {
struct UiPreferences {
    std::string selected, autoConnectSite;
    bool startup = false, autoConnect = false, closeToTray = true, minimizeToTray = false;
    static UiPreferences decode(const Json& value);
    static UiPreferences load();
    void save() const;
};
}
