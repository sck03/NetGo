#include "remote_async_exchange.hpp"
#include <fstream>
#include <iostream>
#include <stdexcept>

using namespace netgo::remote;
using Json = nlohmann::json;
namespace fs = std::filesystem;
void check(bool value, const char* why) { if (!value) throw std::runtime_error(why); }
Json response(Files& files, const Json& request) {
    auto action = request.at("action").get<std::string>();
    if (action == "file.begin") return {{"token", files.begin(request.at("name"), request.at("size"))}};
    if (action == "file.write") return {{"offset", files.write(request.at("token"), request.at("offset"), decode64(request.at("data"), file_chunk))}};
    if (action == "file.commit") { files.commit(request.at("token"), request.at("sha256")); return {{"done", true}}; }
    if (action == "file.open") return files.open(request.at("name"));
    if (action == "file.read") return files.read(request.at("token"), request.at("offset"));
    if (action == "file.cancel") { files.cancel(); return {{"done", true}}; }
    throw std::runtime_error("unexpected file command");
}
int main() {
    auto root = fs::temp_directory_path() / ("netgo-async-" + std::to_string(std::chrono::steady_clock::now().time_since_epoch().count()));
    try {
        fs::create_directories(root / "local"); fs::create_directories(root / "remote");
        const std::string content(512 * 1024, 'a');
        { std::ofstream output(root / "local/source.bin", std::ios::binary); output << content; }
        {
        Files files(root / "remote");
        std::vector<Json> events;
        const auto caller = std::this_thread::get_id();
        AsyncExchange exchange([&](const Json& event) {
            check(std::this_thread::get_id() == caller, "event callback moved off media owner");
            events.push_back(event);
        });
        auto pump = [&](const std::string& action) {
            const auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(5);
            while (std::chrono::steady_clock::now() < deadline) {
                auto request = exchange.next(63);
                if (!request.is_null()) exchange.receive({{"id", request.at("id")}, {"result", response(files, request)}});
                for (const auto& event : events) if (event.value("action", "") == action && event.value("done", false)) {
                    check(!event.contains("error"), "asynchronous transfer failed"); return;
                }
                std::this_thread::sleep_for(std::chrono::milliseconds(1));
            }
            throw std::runtime_error("asynchronous transfer stalled");
        };
        check(exchange.next(63).is_null(), "idle exchange produced a request");
        check(exchange.push({{"action", "upload"}, {"name", "received.bin"}, {"path", path_text(root / "local/source.bin")}}), "upload queue");
        pump("upload"); events.clear();
        std::this_thread::sleep_for(std::chrono::milliseconds(20)); // Worker is waiting without a polling timer.
        check(exchange.push({{"action", "download"}, {"name", "received.bin"}, {"path", path_text(root / "local/result.bin")}}), "download queue");
        pump("download");
        { std::ifstream input(root / "local/result.bin", std::ios::binary);
          check(std::string(std::istreambuf_iterator<char>(input), {}) == content, "asynchronous content mismatch"); }
        events.clear();
        for (unsigned i=0; i<4; ++i) check(exchange.push({{"action", "download"}, {"name", "received.bin"}, {"path", path_text(root / "local" / ("unused-"+std::to_string(i)))}}), "bounded queue rejected too early");
        exchange.push({{"action", "cancel"}});
        exchange.reset();
        unsigned completions=0;
        for(const auto& event:events) if(event.value("action", "")=="download"&&event.value("done",false)) ++completions;
        check(completions==4, "disconnect lost queued completion or duplicated cleanup");
        files.cancel();
        // Cancelling before the first media tick must still release staged files.
        std::vector<Json> queuedEvents;
        AsyncExchange queued([&](const Json& event) { queuedEvents.push_back(event); });
        check(queued.push({{"action", "upload"}, {"name", "unused.bin"}, {"path", path_text(root / "local/source.bin")}}), "lazy queue");
        queued.reset();
        check(queuedEvents.size() == 1 && queuedEvents[0].value("done", false), "unstarted worker lost cleanup");
        }
        fs::remove_all(root);
        std::cout << "PASS asynchronous file roundtrip, bounded ownership and disconnect cleanup\n";
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n'; std::error_code ec; fs::remove_all(root, ec); return 1;
    }
}
