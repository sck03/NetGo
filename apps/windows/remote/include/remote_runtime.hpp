#pragma once
#include "common.hpp"
#include <functional>
#include <optional>

namespace netgo::desktop {
struct Frame {
    unsigned width{}, height{};
    std::vector<unsigned char> bgra;
};
struct Callbacks {
    std::function<bool()> running;
    std::function<void(std::string)> status;
    std::function<void(Frame)> present;
    std::function<bool(const std::string &, uint32_t)> consent;
    std::function<std::vector<Json>()> inputs;
    std::function<void(uint32_t)> permissions;
    bool unattended = false;
    std::function<std::vector<Json>()> commands;
    std::function<void(Json)> event;
};
std::string clipboardText(const std::optional<std::string> &value = std::nullopt);
void host(const std::string &site, Callbacks callbacks);
void view(const std::string &site, const std::string &host, uint32_t capabilities, Callbacks callbacks);
Json inventory(const std::string &site, const std::function<bool()> &active = {});
} // namespace netgo::desktop
