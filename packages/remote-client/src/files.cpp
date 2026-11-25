#include "remote_files.hpp"
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <algorithm>
#include <array>
#include <cctype>
#include <stdexcept>
#ifdef _WIN32
#include <windows.h>
#else
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#endif
namespace netgo::remote {
namespace fs = std::filesystem;
using Json = nlohmann::json;
namespace {
void require(bool v, const char *code) {
    if (!v)
        throw std::runtime_error(code);
}
std::string randomToken() {
    std::array<uint8_t, 16> b{};
    require(RAND_bytes(b.data(), int(b.size())) == 1, "remote_random_failed");
    return encode64(b.data(), b.size());
}
struct Digest {
    std::unique_ptr<EVP_MD_CTX, decltype(&EVP_MD_CTX_free)> ctx{EVP_MD_CTX_new(), EVP_MD_CTX_free};
    Digest() {
        require(ctx && EVP_DigestInit_ex(ctx.get(), EVP_sha256(), nullptr) == 1, "remote_hash_failed");
    }
    void update(const std::vector<uint8_t> &b) {
        require(EVP_DigestUpdate(ctx.get(), b.data(), b.size()) == 1, "remote_hash_failed");
    }
    std::string finish() {
        std::array<uint8_t, 32> b{};
        unsigned n = 0;
        require(EVP_DigestFinal_ex(ctx.get(), b.data(), &n) == 1 && n == 32, "remote_hash_failed");
        return encode64(b.data(), b.size());
    }
};
#ifdef _WIN32
using Native = HANDLE;
const Native invalid = INVALID_HANDLE_VALUE;
void closeFile(Native f) {
    if (f != invalid)
        CloseHandle(f);
}
#else
using Native = int;
constexpr Native invalid = -1;
void closeFile(Native f) {
    if (f != invalid)
        ::close(f);
}
#endif
} // namespace
std::string encode64(const uint8_t *p, std::size_t n) {
    require(n <= 4 * 1024 * 1024, "remote_invalid_encoding");
    std::string s(4 * ((n + 2) / 3), '\0');
    if (n)
        s.resize(size_t(EVP_EncodeBlock(reinterpret_cast<uint8_t *>(s.data()), p, int(n))));
    for (char &c : s) {
        if (c == '+')
            c = '-';
        else if (c == '/')
            c = '_';
    }
    while (!s.empty() && s.back() == '=')
        s.pop_back();
    return s;
}
std::vector<uint8_t> decode64(const std::string &s, std::size_t limit) {
    require(s.size() <= (limit + 2) / 3 * 4 &&
                s.find_first_not_of("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") ==
                    std::string::npos,
            "remote_invalid_encoding");
    if (s.empty())
        return {};
    std::string t = s;
    for (char &c : t) {
        if (c == '-')
            c = '+';
        else if (c == '_')
            c = '/';
    }
    while (t.size() % 4)
        t += '=';
    std::vector<uint8_t> b(t.size() / 4 * 3);
    int n = EVP_DecodeBlock(b.data(), reinterpret_cast<const uint8_t *>(t.data()), int(t.size()));
    while (!t.empty() && t.back() == '=') {
        --n;
        t.pop_back();
    }
    require(n >= 0 && size_t(n) <= limit, "remote_invalid_encoding");
    b.resize(size_t(n));
    require(encode64(b.data(), b.size()) == s, "remote_invalid_encoding");
    return b;
}
std::filesystem::path utf8_path(const std::string &s) {
#ifdef __cpp_char8_t
    return fs::path(std::u8string(reinterpret_cast<const char8_t *>(s.data()), s.size()));
#else
    return fs::u8path(s);
#endif
}
std::string path_text(const fs::path &p) {
    auto s = p.u8string();
    return std::string(reinterpret_cast<const char *>(s.data()), s.size());
}
bool valid_file_name(const std::string &s) {
    if (s.empty() || s.size() > 180 || s.front() == '.' || s.back() == '.' || s.back() == ' ')
        return false;
    for (unsigned char c : s)
        if (c < 32 || c == 127 || std::string("<>:\"/\\|?*").find(char(c)) != std::string::npos)
            return false;
    auto stem = s.substr(0, s.find('.'));
    for (char &c : stem)
        c = char(std::toupper(static_cast<unsigned char>(c)));
    if (stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL")
        return false;
    if (stem.size() == 4 && (stem.substr(0, 3) == "COM" || stem.substr(0, 3) == "LPT") && stem[3] >= '0' &&
        stem[3] <= '9')
        return false;
    return true;
}
struct Files::Impl {
    fs::path root;
    Native directory = invalid, file = invalid;
    std::string token, temp, name;
    std::uint64_t size = 0, offset = 0;
    bool uploading = false;
    std::unique_ptr<Digest> digest;
    bool quota;
    explicit Impl(const fs::path &p, bool budget) : quota(budget) {
        require(!p.empty() && p.is_absolute(), "remote_exchange_root_required");
        fs::create_directories(p);
        require(!fs::is_symlink(fs::symlink_status(p)), "remote_exchange_symlink");
        root = fs::canonical(p);
#ifdef _WIN32
        directory =
            CreateFileW(root.c_str(), FILE_LIST_DIRECTORY, FILE_SHARE_READ | FILE_SHARE_WRITE, nullptr,
                        OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
        BY_HANDLE_FILE_INFORMATION info{};
        if (directory == invalid || !GetFileInformationByHandle(directory, &info) ||
            (info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) {
            closeFile(directory);
            directory = invalid;
            throw std::runtime_error("remote_exchange_unavailable");
        }
#else
        directory = ::open(root.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
        require(directory != invalid, "remote_exchange_unavailable");
#endif
    }
    ~Impl() {
        cancel();
        closeFile(directory);
    }
    Native child(const std::string &n, bool create) {
#ifdef _WIN32
        auto p = root / utf8_path(n);
        auto h = CreateFileW(p.c_str(), create ? GENERIC_WRITE : GENERIC_READ, FILE_SHARE_READ, nullptr,
                             create ? CREATE_NEW : OPEN_EXISTING,
                             FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAG_SEQUENTIAL_SCAN, nullptr);
        BY_HANDLE_FILE_INFORMATION i{};
        if (h != invalid &&
            (!GetFileInformationByHandle(h, &i) ||
             (i.dwFileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY)) ||
             i.nNumberOfLinks != 1)) {
            closeFile(h);
            h = invalid;
        }
        return h;
#else
        auto h = openat(
            directory, n.c_str(),
            (create ? O_WRONLY | O_CREAT | O_EXCL : O_RDONLY | O_NONBLOCK) | O_NOFOLLOW | O_CLOEXEC, 0600);
        struct stat st{};
        if (h >= 0 && (fstat(h, &st) || !S_ISREG(st.st_mode) || st.st_nlink != 1)) {
            closeFile(h);
            h = invalid;
        }
        return h;
#endif
    }
    std::uint64_t length(Native h) {
#ifdef _WIN32
        LARGE_INTEGER n{};
        require(GetFileSizeEx(h, &n) && n.QuadPart >= 0, "remote_file_stat_failed");
        return std::uint64_t(n.QuadPart);
#else
        struct stat s{};
        require(!fstat(h, &s) && s.st_size >= 0, "remote_file_stat_failed");
        return std::uint64_t(s.st_size);
#endif
    }
    void remove(const std::string &n) noexcept {
#ifdef _WIN32
        DeleteFileW((root / utf8_path(n)).c_str());
#else
        unlinkat(directory, n.c_str(), 0);
#endif
    }
    void cancel() noexcept {
        closeFile(file);
        file = invalid;
        if (!temp.empty())
            remove(temp);
        temp.clear();
        token.clear();
        name.clear();
        digest.reset();
        size = offset = 0;
        uploading = false;
    }
};
Files::Files(const fs::path &root, bool quota) : impl_(std::make_unique<Impl>(root, quota)) {}
Files::~Files() = default;
void Files::cancel() noexcept { impl_->cancel(); }
Json Files::list() {
    auto &p = *impl_;
    Json list = Json::array();
    for (const auto &e : fs::directory_iterator(p.root)) {
        auto name = path_text(e.path().filename());
        if (!valid_file_name(name))
            continue;
        auto h = p.child(name, false);
        if (h == invalid)
            continue;
        std::uint64_t n = 0;
        try {
            n = p.length(h);
        } catch (...) {
            closeFile(h);
            continue;
        }
        closeFile(h);
        if (n <= max_file_bytes)
            list.push_back({{"name", name}, {"size", n}});
        if (list.size() == 256)
            break;
    }
    return list;
}
std::string Files::begin(const std::string &name, std::uint64_t size) {
    auto &p = *impl_;
    require(valid_file_name(name) && size <= max_file_bytes, "remote_invalid_file");
    require(p.file == invalid, "remote_transfer_busy");
    require(!fs::exists(p.root / utf8_path(name)), "remote_file_exists");
    if (p.quota) {
        auto listing = list();
        std::uint64_t used = 0;
        for (const auto &e : listing) {
            require(e.at("name") != name, "remote_file_exists");
            used += e.at("size").get<std::uint64_t>();
        }
        require(listing.size() < 256 && used + size <= 2 * max_file_bytes, "remote_exchange_full");
    }
    p.token = randomToken();
    p.temp = ".netgo-" + p.token + ".part";
    p.file = p.child(p.temp, true);
    if (p.file == invalid) {
        p.temp.clear();
        throw std::runtime_error("remote_file_create_failed");
    }
    p.uploading = true;
    p.name = name;
    p.size = size;
    p.offset = 0;
    p.digest = std::make_unique<Digest>();
    return p.token;
}
std::uint64_t Files::write(const std::string &token, std::uint64_t offset, const std::vector<uint8_t> &data) {
    auto &p = *impl_;
    require(p.file != invalid && p.uploading && token == p.token && offset == p.offset && !data.empty() &&
                data.size() <= file_chunk && data.size() <= p.size - p.offset,
            "remote_invalid_file_chunk");
    std::size_t done = 0;
    while (done < data.size()) {
#ifdef _WIN32
        DWORD n = 0;
        require(WriteFile(p.file, data.data() + done, DWORD(data.size() - done), &n, nullptr) && n > 0,
                "remote_file_write_failed");
#else
        auto n = ::write(p.file, data.data() + done, data.size() - done);
        require(n > 0, "remote_file_write_failed");
#endif
        done += std::size_t(n);
    }
    p.digest->update(data);
    p.offset += data.size();
    return p.offset;
}
void Files::commit(const std::string &token, const std::string &sha) {
    auto &p = *impl_;
    require(p.file != invalid && p.uploading && token == p.token && p.offset == p.size,
            "remote_incomplete_file");
    require(p.digest->finish() == sha, "remote_file_hash_mismatch");
#ifdef _WIN32
    require(FlushFileBuffers(p.file), "remote_file_flush_failed");
    closeFile(p.file);
    p.file = invalid;
    require(MoveFileW((p.root / utf8_path(p.temp)).c_str(), (p.root / utf8_path(p.name)).c_str()),
            "remote_file_exists");
#else
    require(!fsync(p.file), "remote_file_flush_failed");
    require(!linkat(p.directory, p.temp.c_str(), p.directory, p.name.c_str(), 0), "remote_file_exists");
    p.remove(p.temp);
    require(!fsync(p.directory), "remote_file_flush_failed");
    closeFile(p.file);
    p.file = invalid;
#endif
    p.temp.clear();
    p.cancel();
}
Json Files::open(const std::string &name) {
    auto &p = *impl_;
    require(valid_file_name(name), "remote_invalid_file");
    require(p.file == invalid, "remote_transfer_busy");
    p.file = p.child(name, false);
    require(p.file != invalid, "remote_file_unavailable");
    try {
        p.size = p.length(p.file);
        require(p.size <= max_file_bytes, "remote_file_too_large");
        p.offset = 0;
        p.uploading = false;
        p.token = randomToken();
        p.digest = std::make_unique<Digest>();
        return {{"token", p.token}, {"size", p.size}, {"name", name}};
    } catch (...) {
        p.cancel();
        throw;
    }
}
Json Files::read(const std::string &token, std::uint64_t offset) {
    auto &p = *impl_;
    require(p.file != invalid && !p.uploading && token == p.token && offset == p.offset &&
                p.length(p.file) == p.size,
            "remote_file_changed");
    std::vector<uint8_t> data(size_t(std::min<std::uint64_t>(file_chunk, p.size - p.offset)));
    std::size_t done = 0;
    while (done < data.size()) {
#ifdef _WIN32
        DWORD n = 0;
        require(ReadFile(p.file, data.data() + done, DWORD(data.size() - done), &n, nullptr) && n > 0,
                "remote_file_read_failed");
#else
        auto n = ::read(p.file, data.data() + done, data.size() - done);
        require(n > 0, "remote_file_read_failed");
#endif
        done += std::size_t(n);
    }
    p.digest->update(data);
    p.offset += data.size();
    Json result = {
        {"offset", offset}, {"data", encode64(data.data(), data.size())}, {"done", p.offset == p.size}};
    if (p.offset == p.size) {
        result["sha256"] = p.digest->finish();
        p.cancel();
    }
    return result;
}
} // namespace netgo::remote
