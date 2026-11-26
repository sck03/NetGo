#pragma once
#include "common.hpp"
#include <optional>

namespace netgo {
std::wstring errorText(const std::string& error);
std::wstring stateText(const std::string& state);
std::optional<Json> profileDialog(HWND owner, const Json& profile, bool create);
}
