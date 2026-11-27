#include "engine_channel.hpp"
#include "agent_lifecycle.hpp"
#include "remote_process.hpp"
#include <iostream>

using namespace netgo;
void check(bool value, const char* why) { if (!value) throw Error(why); }
template<class F> void fails(F fn, const char* expected) {
    try { fn(); } catch (const Error& e) { check(std::string(e.what()) == expected, e.what()); return; }
    throw Error("expected failure");
}
int main() {
    try {
        std::atomic_bool cancel = false;
        std::string payload(maxEngineRequest, 'x');
        for (bool cancelled : {false, true}) {
            EngineChannel channel(true); // Deliberately never read the child end.
            auto start = Clock::now();
            std::thread trigger;
            if (cancelled) trigger = std::thread([&] { Sleep(60); cancel = true; });
            std::string failure;
            try { channel.transfer(true, payload.data(), DWORD(payload.size()), start + std::chrono::milliseconds(150), cancel); }
            catch (const Error& e) { failure = e.what(); }
            if (trigger.joinable()) trigger.join();
            check(failure == (cancelled ? "cancelled" : "engine_timeout"), "blocked write was not bounded");
            check(Clock::now() - start < std::chrono::seconds(2), "pipe cancellation took too long");
            cancel = false;
        }
        EngineChannel channel(false);
        char byte = 0;
        fails([&] { channel.transfer(false, &byte, 1, Clock::now() + std::chrono::milliseconds(80), cancel); }, "engine_timeout");
        DWORD written = 0;
        check(WriteFile(channel.child(), "a", 1, &written, nullptr) && written == 1, "pipe write");
        check(channel.transfer(false, &byte, 1, Clock::now() + std::chrono::seconds(1), cancel) == 1 && byte == 'a', "cancelled read poisoned the pipe");
        channel.closeChild();
        fails([&] { channel.transfer(false, &byte, 1, Clock::now() + std::chrono::seconds(1), cancel); }, "engine_protocol_failed");

        unsigned calls = 0, starts = 0; bool recovered = false;
        AgentLifecycle agent([&](const Json&, const std::function<bool()>&) -> Json {
            if (++calls == 1) throw Error("agent_unavailable");
            return {{"state", "disconnected"}};
        }, [&] { ++starts; return true; });
        check(agent.call({{"action", "status"}}, [] { return true; }, recovered).at("state") == "disconnected" && recovered && starts == 1, "agent recovery");
        AgentLifecycle live([](const Json&, const std::function<bool()>&) -> Json { throw Error("ipc_failed"); }, [&] { return false; });
        fails([&] { live.call({{"action", "shutdown"}}, [] { return true; }, recovered); }, "ipc_failed");
        check(!recovered, "live cleanup failure reported as success");
        AgentLifecycle uncertain([](const Json&, const std::function<bool()>&) -> Json { throw Error("ipc_failed"); }, [&] { ++starts; return true; });
        fails([&] { uncertain.call({{"action", "connect"}}, [] { return true; }, recovered); }, "ipc_failed");
        check(starts == 1, "mutating command replayed");
        check(remoteWindowClass("a") != remoteWindowClass("b"), "site window identity shared");
        std::cout << "PASS bounded engine IO, agent recovery and site identities\n";
    } catch (const std::exception& e) { std::cerr << e.what() << '\n'; return 1; }
}
