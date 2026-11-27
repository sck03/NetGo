#include "ui_preferences.hpp"
#include <algorithm>

namespace netgo {
UiPreferences UiPreferences::decode(const Json& value) {
    UiPreferences result;
    if (!value.is_object()) return result;
    auto flag = [&](const char* key, bool fallback) {
        auto item = value.find(key);
        return item != value.end() && item->is_boolean() ? item->get<bool>() : fallback;
    };
    auto site = [&](const char* key) {
        auto item = value.find(key);
        if (item == value.end() || !item->is_string()) return std::string();
        auto id = item->get<std::string>();
        auto valid = [](unsigned char c) {
            return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
                   (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.';
        };
        if (id.size() > 63 || !std::all_of(id.begin(), id.end(), valid)) return std::string();
        return id;
    };
    result.selected = site("selected");
    result.autoConnectSite = site("auto_connect_site");
    result.startup = flag("startup", false);
    result.closeToTray = flag("close_to_tray", true);
    result.minimizeToTray = flag("minimize_to_tray", false);
    result.autoConnect = flag("auto_connect", false) && !result.autoConnectSite.empty();
    return result;
}
UiPreferences UiPreferences::load() {
    try { return decode(Json::parse(readFile(dataRoot() / L".preferences", 8192))); }
    catch (const std::exception&) { return {}; }
}
void UiPreferences::save() const {
    secureDirectory(dataRoot());
    const Json value = {{"selected", selected}, {"auto_connect_site", autoConnectSite},
                        {"startup", startup}, {"auto_connect", autoConnect},
                        {"close_to_tray", closeToTray}, {"minimize_to_tray", minimizeToTray}};
    atomicFile(dataRoot() / L".preferences", value.dump(2));
}
}
