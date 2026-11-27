#include "ui_preferences.hpp"
#include "startup.hpp"
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
    result.unattendedSite = site("unattended_site");
    result.startup = flag("startup", false);
    result.closeToTray = flag("close_to_tray", true);
    result.minimizeToTray = flag("minimize_to_tray", false);
    result.autoConnect = flag("auto_connect", false) && !result.autoConnectSite.empty();
    result.normalize();
    return result;
}
void UiPreferences::normalize() {
    if (autoConnectSite.empty()) autoConnect = false;
    if (!autoConnect) autoConnectSite.clear();
    if (!startup || !autoConnect || unattendedSite != autoConnectSite) unattendedSite.clear();
}
void UiPreferences::forgetSite(const std::string& site) {
    if (autoConnectSite == site) { autoConnect = false; autoConnectSite.clear(); }
    if (unattendedSite == site) unattendedSite.clear();
}
UiPreferences UiPreferences::load(const std::filesystem::path& root) {
    try { return decode(Json::parse(readFile(root / L".preferences", 8192))); }
    catch (const std::exception&) { return {}; }
}
UINT UiPreferences::changedMessage() {
    static const UINT message = RegisterWindowMessageW(
        wide("NetGoPreferences-" + sha256(utf8(dataRoot().wstring()))).c_str());
    return message;
}
UiPreferences UiPreferences::update(const std::function<void(UiPreferences&)>& edit,
                                    const std::filesystem::path& root) {
    const auto name = wide("Local\\NetGoPreferences-" + sid() + "-" + sha256(utf8(root.wstring())));
    Handle mutex(CreateMutexW(nullptr, FALSE, name.c_str()));
    if (!mutex.value) throw Error("preferences_busy");
    const auto locked = WaitForSingleObject(mutex, 2000);
    if (locked != WAIT_OBJECT_0 && locked != WAIT_ABANDONED) throw Error("preferences_busy");
    struct Unlock { HANDLE mutex; ~Unlock() { ReleaseMutex(mutex); } } unlock{mutex};
    auto previous = load(root), next = previous;
    edit(next);
    next.normalize();
    if (next == previous) return next;
    secureDirectory(root);
    const bool startupChanged = previous.startup != next.startup;
    if (startupChanged) netgo::startup(next.startup);
    try {
        const Json value = {{"selected", next.selected}, {"auto_connect_site", next.autoConnectSite},
                            {"unattended_site", next.unattendedSite},
                            {"startup", next.startup}, {"auto_connect", next.autoConnect},
                            {"close_to_tray", next.closeToTray}, {"minimize_to_tray", next.minimizeToTray}};
        atomicFile(root / L".preferences", value.dump(2));
    } catch (...) {
        if (startupChanged) { try { netgo::startup(previous.startup); } catch (...) {} }
        throw;
    }
    PostMessageW(HWND_BROADCAST, changedMessage(), 0, 0);
    return next;
}
}
