#include "remote_files.hpp"
#include "remote_exchange.hpp"
#include "remote_commands.hpp"
#include <future>
#include <openssl/evp.h>
#include <fstream>
#include <iostream>
#include <functional>
#include <cstdlib>
#ifdef _WIN32
#include <windows.h>
#else
#include <sys/wait.h>
#include <unistd.h>
#endif
#ifndef _WIN32
#include <sys/stat.h>
#endif
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
// Deliberately bypass destructors: restart recovery must rely on OS lock release.
void crashUpload(const fs::path &root) {
    Files files(root);
    auto token = files.begin("interrupted.bin", 65536);
    files.write(token, 0, std::vector<uint8_t>(32768, 42));
    std::_Exit(0);
}
void runCrashedUploader(const fs::path &root) {
#ifdef _WIN32
    wchar_t executable[32768]{};
    check(GetModuleFileNameW(nullptr, executable, 32768) != 0, "test executable path");
    auto command = L"\"" + std::wstring(executable) + L"\" --crash-upload \"" + root.wstring() + L"\"";
    STARTUPINFOW startup{}; startup.cb = sizeof(startup);
    PROCESS_INFORMATION process{};
    check(CreateProcessW(executable, command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
                         nullptr, nullptr, &startup, &process), "start crashed uploader");
    CloseHandle(process.hThread);
    const auto waited = WaitForSingleObject(process.hProcess, 10000);
    DWORD code = 1;
    GetExitCodeProcess(process.hProcess, &code);
    if (waited != WAIT_OBJECT_0) TerminateProcess(process.hProcess, 1);
    CloseHandle(process.hProcess);
    check(waited == WAIT_OBJECT_0 && code == 0, "crashed uploader failed");
#else
    const auto child = fork();
    check(child >= 0, "fork uploader");
    if (!child) crashUpload(root);
    int status = 0;
    check(waitpid(child, &status, 0) == child && WIFEXITED(status) && WEXITSTATUS(status) == 0,
          "crashed uploader failed");
#endif
}
int main(int argc, char **argv) {
    if (argc == 3 && std::string(argv[1]) == "--crash-upload") crashUpload(utf8_path(argv[2]));
    {
        std::promise<void> entered, release;
        auto unblock = release.get_future().share();
        CommandWorker commands([&](const Json& request, const std::function<bool()>&) {
            if (request.at("id") == 1u) { entered.set_value(); unblock.wait(); }
            return Json{{"done",true}};
        }, [] {});
        check(commands.submit({{"id",1u}}), "command rejected");
        entered.get_future().wait();
        const bool nonblocking = commands.take().is_null();
        for (unsigned id=2;id<=8;++id) check(commands.submit({{"id",id}}), "bounded command capacity lost");
        const bool bounded = !commands.submit({{"id",9u}});
        const bool cancellationReserved = commands.submit({{"id",9u},{"action","file.cancel"}}) &&
            !commands.submit({{"id",10u},{"action","file.cancel"}});
        release.set_value();
        check(nonblocking && bounded && cancellationReserved, "slow IO blocks media/cancellation or queue is unbounded");
    }
    {
        CommandWorker commands([](const Json&, const std::function<bool()>&) {
            return Json{{"done", true}};
        }, [] {});
        check(!commands.submit({{"action", "file.list"}}), "missing command id accepted");
        check(commands.submit({{"id", 1u}, {"action", 7}}), "malformed action was not queued");
        check(commands.submit({{"id", 2u}, {"action", "file.list"}}), "follow-up command rejected");
        const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(2);
        unsigned received = 0;
        while (received < 2 && std::chrono::steady_clock::now() < deadline) {
            auto response = commands.take();
            if (response.is_null()) { std::this_thread::sleep_for(std::chrono::milliseconds(1)); continue; }
            check(response.at("id") == ++received, "command result order changed");
            check(received == 1 ? response.contains("error") : response.at("result").value("done", false),
                  "malformed action terminated the worker");
        }
        check(received == 2, "command worker did not recover");
    }
    auto root = fs::temp_directory_path() /
                fs::path("netgo-exchange-test-" +
                         std::to_string(std::chrono::steady_clock::now().time_since_epoch().count()));
    try {
        fs::create_directories(root / "remote");
        fs::create_directories(root / "local");
        {
            Files files(root / "remote");
            auto token = files.begin("revoked-at-commit.bin", 1);
            files.write(token, 0, {42});
            denied([&] { files.commit(token, hash({42}), [] { return false; }); });
            files.cancel();
            check(!fs::exists(root / "remote/revoked-at-commit.bin"), "revoked commit published a file");
        }
        const auto interrupted = root / "restart";
        runCrashedUploader(interrupted);
        size_t partials = 0;
        for (const auto &entry : fs::directory_iterator(interrupted))
            if (entry.path().extension() == ".part") ++partials;
        check(partials == 1, "crash did not leave a partial upload");
        {
            Files recovered(interrupted);
            for (const auto &entry : fs::directory_iterator(interrupted))
                check(entry.path().extension() != ".part", "orphan upload survived restart");
            auto token = recovered.begin("live.bin", 1);
            recovered.write(token, 0, {42});
            Files second(interrupted);
            denied([&] { second.begin("parallel.bin", 0); });
            recovered.commit(token, hash({42}));
            check(fs::exists(interrupted / "live.bin"), "active upload was reclaimed");
            token = second.begin("after.bin", 0);
            second.commit(token, hash({}));
        }
        {
            const auto hidden = root / "hidden-quota";
            fs::create_directories(hidden);
            for (unsigned i = 0; i < 256; ++i)
                std::ofstream(hidden / (".unrecognized-" + std::to_string(i)));
            Files files(hidden);
            denied([&] { files.begin("over-quota.bin", 0); });
            check(fs::exists(hidden / ".unrecognized-0"), "unowned file was reclaimed");
        }
        for (auto name : {"", ".", "..", "../outside", "a/b", "a\\b", "C:test", "CON", "NUL.txt", "file.",
                          "COM1.bin", ".secret", "file "})
            check(!valid_file_name(name), "unsafe name");
        check(valid_file_name("report.txt"), "valid name rejected");
        std::vector<uint8_t> content(file_chunk*10+17);
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
#ifndef _WIN32
        check(mkfifo((root / "remote/pipe").c_str(), 0600) == 0, "create fifo");
        {
            Files files(root / "remote");
            denied([&] { files.open("pipe"); });
            files.list();
        }
        fs::remove(root / "remote/pipe");
#endif
        fs::create_directories(root / "full-local");
        for (unsigned i = 0; i < 256; ++i)
            std::ofstream(root / "full-local" / ("existing-" + std::to_string(i)));
        {
            Files quota(root / "full-local");
            denied([&] { quota.begin("blocked", 0); });
        }
        {
            Files selected(root / "full-local", false);
            auto token = selected.begin("chosen.txt", 0);
            selected.commit(token, hash({}));
        }
        check(fs::exists(root / "full-local/chosen.txt"), "local destination inherited host quota");
        std::ofstream source(root / "local/source.bin", std::ios::binary);
        source.write(reinterpret_cast<const char *>(content.data()), std::streamsize(content.size()));
        source.close();
        {
            std::vector<Json> events;
            Exchange exchange([&](const Json &e) { events.push_back(e); });
            Files remote(root / "remote");
            size_t maxInFlight=0;
            auto pump = [&] {
                for (int i = 0; i < 100; ++i) {
                    std::vector<Json> batch;
                    for(unsigned slot=0;slot<9;++slot){auto q=exchange.next(63);if(q.is_null())break;batch.push_back(std::move(q));}
                    check(batch.size()<=8,"transfer window unbounded");
                    maxInFlight=std::max(maxInFlight,batch.size());
                    if(batch.empty())return;
                    for(const auto& q:batch){
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
            check(maxInFlight==8,"transfers still wait for every chunk acknowledgement");
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
            exchange.push({{"action", "upload"},
                           {"name", "revoked.bin"},
                           {"path", path_text(root / "local/source.bin")}});
            pending = exchange.next(63);
            token = remote.begin(pending.at("name"), pending.at("size"));
            check(exchange.next(1).is_null(), "revocation overtook pending request");
            exchange.receive({{"id", pending.at("id")}, {"result", {{"token", token}}}});
            pump();
            check(!fs::exists(root / "remote/revoked.bin"), "revoked upload published");
            exchange.push({{"action", "download"},
                           {"name", "empty.bin"},
                           {"path", path_text(root / "local/cancel-download.bin")}});
            pending = exchange.next(63);
            auto opened = remote.open("empty.bin");
            exchange.receive({{"id", pending.at("id")}, {"result", opened}});
            pending = exchange.next(63);
            auto last = remote.read(pending.at("token"), 0);
            exchange.push({{"action", "cancel"}});
            exchange.receive({{"id", pending.at("id")}, {"result", last}});
            pump();
            check(!fs::exists(root / "local/cancel-download.bin"), "cancelled final chunk published");
            events.clear();
            exchange.push({{"action", "upload"},
                           {"name", "queued.bin"},
                           {"path", path_text(root / "local/source.bin")}});
            exchange.push({{"action", "cancel"}});
            pump();
            check(events.size() >= 2 && events.front().at("action") == "upload" &&
                      events.front().contains("error"),
                  "queued cancel lost completion");
            events.clear();
            exchange.push({{"action", "download"},
                           {"name", "queued.bin"},
                           {"path", path_text(root / "local/unused.bin")}});
            exchange.reset();
            check(events.size() == 1 && events.front().at("action") == "download" &&
                      events.front().contains("error"),
                  "queued disconnect lost completion");
            check(exchange.push({{"action", "clipboard.set"}, {"text", std::string(8192, char(1))}}),
                  "escaped clipboard limit rejected");
            pending = exchange.next(9);
            check(pending.at("text").get<std::string>().size() == 8192, "clipboard truncated");
            exchange.receive({{"id", pending.at("id")}, {"result", {{"text", ""}}}});
            check(!exchange.push({{"action", "clipboard.set"}, {"text", std::string(8193, 'a')}}),
                  "oversized clipboard accepted");
            exchange.push({{"action", "file.list"}});
            auto expired = exchange.next(63);
            auto cancellation = exchange.next(63, std::chrono::steady_clock::now() + std::chrono::seconds(11));
            check(cancellation.at("action") == "file.cancel" && events.back().value("error", "") == "remote_command_timeout", "file timeout escaped into media");
            exchange.receive({{"id", expired.at("id")}, {"result", {{"files", Json::array()}}}});
            check(exchange.next(63, std::chrono::steady_clock::now() + std::chrono::seconds(11)).is_null(), "cancel timeout escaped");
            exchange.push({{"action", "clipboard.get"}});
            check(exchange.next(63).is_null() && events.back().value("error", "") == "remote_command_timeout", "new request exceeded stalled peer window");
            exchange.receive({{"id", cancellation.at("id")}, {"result", {{"done", true}}}});
            exchange.push({{"action", "clipboard.get"}});
            auto next = exchange.next(63);
            exchange.receive({{"id", next.at("id")}, {"result", {{"text", "still connected"}}}});
            check(events.back().at("text") == "still connected", "late reply poisoned subsequent commands");
            for (auto &e : fs::directory_iterator(root / "remote"))
                check(e.path().extension() != ".part", "temporary upload leaked");
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
