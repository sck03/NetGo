#include "common.hpp"
#include "startup.hpp"
namespace netgo {
void startup(bool enabled) {
    HKEY key = nullptr;
    if (RegCreateKeyExW(HKEY_CURRENT_USER, L"Software\\Microsoft\\Windows\\CurrentVersion\\Run", 0, nullptr,
                        0, KEY_SET_VALUE, nullptr, &key, nullptr) != ERROR_SUCCESS)
        throw Error("startup_setting_failed");
    if (enabled) {
        std::wstring value = L"\"" + (executableDir() / L"NetGo.exe").wstring() + L"\" --tray";
        auto e = RegSetValueExW(key, L"NetGoNative", 0, REG_SZ, reinterpret_cast<const BYTE *>(value.c_str()),
                                DWORD((value.size() + 1) * sizeof(wchar_t)));
        RegCloseKey(key);
        if (e != ERROR_SUCCESS)
            throw Error("startup_setting_failed");
    } else {
        RegDeleteValueW(key, L"NetGoNative");
        RegCloseKey(key);
    }
}
}
