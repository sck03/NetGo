#pragma once
#include "client.hpp"
#include <filesystem>
namespace netgo::desktop_linux {
class HostPlatform {
  public:
    HostPlatform();
    ~HostPlatform();
    HostPlatform(const HostPlatform &) = delete;
    uint32_t prepare(uint32_t capabilities, const std::filesystem::path &restorePath, bool persistent,
                     std::function<bool()> active);
    void open(uint32_t capabilities, uint32_t &width, uint32_t &height, uint32_t &fps);
    bool poll(uint8_t *output, size_t capacity, size_t &length);
    void input(const Json &);
    void close() noexcept;

  private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};
} // namespace netgo::desktop_linux
