#pragma once
#include <atomic>
#include <cstdint>
#include <string>

namespace netgo::desktop_linux {
int32_t remoteControl(const std::string& socket, const std::string& site, std::atomic_bool& active,
                      const char* method, const char* path, const char* body, const char* key,
                      char* output, size_t capacity, size_t* length) noexcept;
}
