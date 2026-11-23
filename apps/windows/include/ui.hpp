#pragma once
#include "common.hpp"
#include <optional>

namespace netgo {
std::optional<Json> profileDialog(HWND owner, const Json& profile, bool create);
}
