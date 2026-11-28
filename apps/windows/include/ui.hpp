#pragma once
#include "common.hpp"
#include <optional>

namespace netgo {
std::wstring errorText(const std::string& error);
std::wstring stateText(const std::string& state);
std::filesystem::path profileFileDialog(HWND owner, bool save, const wchar_t *title);
std::optional<Json> advancedProfileDialog(HWND owner, HFONT font, const Json &profile, bool create);
std::optional<Json> profileDialog(HWND owner, const Json& profile, bool create);
}
