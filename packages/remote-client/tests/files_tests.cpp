#include "remote_files.hpp"
#include "remote_exchange.hpp"
#include <openssl/evp.h>
#include <fstream>
#include <iostream>
#include <functional>
using namespace netgo::remote;
using Json = nlohmann::json;
namespace fs = std::filesystem;
void check(bool v, const char *e) {
    if (!v)
        throw std::runtime_error(e);
}
void denied(const std::function<void()> &f) {
    bool rejected = false;
    try {
        f();
    } catch (...) {
        rejected = true;
    }
    check(rejected, "invalid operation accepted");
}
std::string hash(const std::vector<uint8_t> &b) {
    uint8_t digest[32];
    unsigned n = 0;
    check(EVP_Digest(b.data(), b.size(), digest, &n, EVP_sha256(), nullptr) == 1 && n == 32, "hash");
    return encode64(digest, 32);
}
int main() {
    auto root = fs::temp_directory_path() /
                fs::path("netgo-exchange-test-" +
                         std::to_string(std::chrono::steady_clock::now().time_since_epoch().count()));
    try {
        fs::create_directories(root / "remote");
        fs::create_directories(root / "local");
        for (auto name : {"", ".", "..", "../outside", "a/b", "a\\b", "C:test", "CON", "NUL.txt", "file.",
                          "COM1.bin", ".secret", "file "})
            check(!valid_file_name(name), "unsafe name");
        check(valid_file_name("report.txt"), "valid name rejected");
        std::vector<uint8_t> content(70001);
        for (size_t i = 0; i < content.size(); ++i)
            content[i] = uint8_t(i * 31);
        {
            Files files(root / "remote");
            denied([&] { files.begin("../escape", 2); });
            denied([&] { files.begin("huge", max_file_bytes + 1); });
            auto token = files.begin("report.bin", content.size());
            denied([&] { files.write(token, 1, {1}); });
            denied([&] { files.write("wrong", 0, {1}); });
            size_t offset = 0;
            while (offset < content.size()) {
                auto n = std::min(file_chunk, content.size() - offset);
                files.write(token, offset, {content.begin() + offset, content.begin() + offset + n});
                offset += n;
            }
            files.commit(token, hash(content));
            denied([&] { files.begin("report.bin", 0); });
            auto info = files.open("report.bin");
            std::vector<uint8_t> downloaded;
            while (true) {
                auto p = files.read(info.at("token"), downloaded.size());
                auto bytes = decode64(p.at("data"), file_chunk);
                downloaded.insert(downloaded.end(), bytes.begin(), bytes.end());
                if (p.at("done").get<bool>()) {
                    check(p.at("sha256") == hash(content), "download hash");
                    break;
                }
            }
            check(downloaded == content, "round trip");
            auto bad = files.begin("bad.bin", 1);
            files.write(bad, 0, {42});
            denied([&] { files.commit(bad, hash({41})); });
            files.cancel();
            check(!fs::exists(root / "remote/bad.bin"), "bad file published");
            auto empty = files.begin("empty.bin", 0);
            files.commit(empty, hash({}));
            files.begin("cancelled.bin", 5);
            files.cancel();
            check(!fs::exists(root / "remote/cancelled.bin"), "cancelled file published");
        }
        std::error_code error;
        fs::create_hard_link(root / "remote/report.bin", root / "remote/hard.bin", error);
        if (!error) {
            Files files(root / "remote");
            denied([&] { files.open("hard.bin"); });
            fs::remove(root / "remote/hard.bin");
        }
        fs::create_symlink(root / "remote/report.bin", root / "remote/link.bin", error);
        if (!error) {
            Files files(root / "remote");
            denied([&] { files.open("link.bin"); });
            fs::remove(root / "remote/link.bin");
        }
        std::ofstream source(root / "local/source.bin", std::ios::binary);
        source.write(reinterpret_cast<const char *>(content.data()), std::streamsize(content.size()));
        source.close();
        {
            std::vector<Json> events;
            Exchange exchange([&](const Json &e) { events.push_back(e); });
            Files remote(root / "remote");
            auto pump = [&] {
                for (int i = 0; i < 100; ++i) {
                    auto q = exchange.next(63);
                    if (q.is_null())
                        return;
                    auto a = q.at("action").get<std::string>();
                    Json r;
                    if (a == "file.begin")
                        r = {{"token", remote.begin(q.at("name"), q.at("size"))}};
                    else if (a == "file.write")
                        r = {{"offset", remote.write(q.at("token"), q.at("offset"),
                                                     decode64(q.at("data"), file_chunk))}};
                    else if (a == "file.commit") {
                        remote.commit(q.at("token"), q.at("sha256"));
                        r = {{"done", true}};
                    } else if (a == "file.open")
                        r = remote.open(q.at("name"));
                    else if (a == "file.read")
                        r = remote.read(q.at("token"), q.at("offset"));
                    else if (a == "file.cancel") {
                        remote.cancel();
                        r = {{"done", true}};
                    } else
                        throw std::runtime_error("unexpected request");
                    exchange.receive({{"id", q.at("id")}, {"result", r}});
                }
                throw std::runtime_error("transfer did not finish");
            };
            check(exchange.push({{"action", "upload"},
                                 {"name", "uploaded.bin"},
                                 {"path", path_text(root / "local/source.bin")}}),
                  "upload command");
            pump();
            check(events.back().at("done") && !events.back().contains("error"), "upload failed");
            events.clear();
            check(exchange.push({{"action", "download"},
                                 {"name", "uploaded.bin"},
                                 {"path", path_text(root / "local/download.bin")}}),
                  "download command");
            pump();
            check(events.back().at("done") && !events.back().contains("error"), "download failed");
            check(fs::file_size(root / "local/download.bin") == content.size(), "download size");
            exchange.push({{"action", "upload"},
                           {"name", "denied.bin"},
                           {"path", path_text(root / "local/source.bin")}});
            check(exchange.next(1).is_null() && events.back().contains("error"),
                  "view only transfer permitted");
            exchange.push({{"action", "upload"},
                           {"name", "cancel-test.bin"},
                           {"path", path_text(root / "local/source.bin")}});
            auto pending = exchange.next(63);
            check(!pending.is_null(), "pending upload");
            auto token = remote.begin(pending.at("name"), pending.at("size"));
            exchange.push({{"action", "cancel"}});
            check(exchange.next(63).is_null(), "cancel overtook in-flight request");
            exchange.receive({{"id", pending.at("id")}, {"result", {{"token", token}}}});
            pump();
            check(!fs::exists(root / "remote/cancel-test.bin"), "cancel published partial file");
            for (auto &e : fs::directory_iterator(root / "remote"))
                check(e.path().filename().string().rfind(".netgo-", 0) != 0, "temporary upload leaked");
        }
        fs::remove_all(root);
        std::cout << "file boundaries, integrity, upload/download, permission and cancellation passed\n";
        return 0;
    } catch (const std::exception &e) {
        std::cerr << e.what() << '\n';
        std::error_code ec;
        fs::remove_all(root, ec);
        return 1;
    }
}
