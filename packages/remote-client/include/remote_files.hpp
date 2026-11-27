#pragma once
#include <cstdint>
#include <filesystem>
#include <memory>
#include <functional>
#include <string>
#include <vector>
#include <nlohmann/json.hpp>
namespace netgo::remote {
inline constexpr std::uint64_t max_file_bytes = 1024ull * 1024 * 1024;
inline constexpr std::size_t file_chunk = 32 * 1024;
bool valid_file_name(const std::string &);
std::filesystem::path utf8_path(const std::string &);
std::string path_text(const std::filesystem::path &);
// Directory handles pin the exchange root. Children are single basenames;
// links, nonregular files and existing upload destinations are rejected.
class Files {
  public:
    explicit Files(const std::filesystem::path &root, bool exchangeQuota = true);
    ~Files();
    Files(const Files &) = delete;
    nlohmann::json list();
    std::string begin(const std::string &name, std::uint64_t size);
    std::uint64_t write(const std::string &token, std::uint64_t offset, const std::vector<uint8_t> &);
    void commit(const std::string &token, const std::string &sha256,
                const std::function<bool()>& allowed = {});
    nlohmann::json open(const std::string &name);
    nlohmann::json read(const std::string &token, std::uint64_t offset);
    void cancel() noexcept;

  private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};
std::string encode64(const uint8_t *, std::size_t);
std::vector<uint8_t> decode64(const std::string &, std::size_t limit);
} // namespace netgo::remote
