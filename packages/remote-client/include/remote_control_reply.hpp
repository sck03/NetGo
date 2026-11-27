#pragma once
#include <algorithm>
#include <cstdint>
#include <exception>
#include <nlohmann/json.hpp>

namespace netgo::remote {
inline int32_t control_error(const std::exception& error, char* output, size_t capacity, size_t* length) noexcept {
    try {
        const auto text = nlohmann::json({{"error", error.what()}}).dump();
        if (!output || !length || text.size() > capacity) return -1;
        std::copy(text.begin(), text.end(), output);
        *length = text.size();
        return 0;
    } catch (...) { return -1; }
}
}
