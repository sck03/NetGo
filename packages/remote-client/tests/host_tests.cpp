#include "netgo_remote_host.h"
#include "netgo_remote_ticket.h"
#include "remote_files.hpp"
#include <openssl/evp.h>
#include <atomic>
#include <chrono>
#include <cstring>
#include <fstream>
#include <iostream>
#include <mutex>
#include <thread>
#ifdef _WIN32
#include <winsock2.h>
#endif
using namespace netgo::remote;
using Json = nlohmann::json;
using Key = std::array<uint8_t, 32>;
void check(bool v, const char *e) {
    if (!v)
        throw std::runtime_error(e);
}
Key publicKey(const Key &seed) {
    auto p = EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr, seed.data(), 32);
    Key k{};
    size_t n = 32;
    check(p && EVP_PKEY_get_raw_public_key(p, k.data(), &n) == 1, "key");
    EVP_PKEY_free(p);
    return k;
}
void sign(const Key &seed, const uint8_t *data, size_t size, uint8_t *output) {
    auto p = EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr, seed.data(), 32);
    auto c = EVP_MD_CTX_new();
    size_t n = 64;
    check(p && c && EVP_DigestSignInit(c, nullptr, nullptr, nullptr, p) == 1 &&
              EVP_DigestSign(c, output, &n, data, size) == 1 && n == 64,
          "sign");
    EVP_MD_CTX_free(c);
    EVP_PKEY_free(p);
}
void put(uint8_t *out, uint64_t n, unsigned count) {
    for (unsigned i = 0; i < count; ++i)
        out[count - 1 - i] = uint8_t(n >> (i * 8));
}
struct Fixture {
    Key siteSeed{}, controllerSeed{}, siteKey{}, controllerKey{};
    Json registration, authorization;
    std::mutex mutex;
    std::atomic_bool live = true, registered = false, mediaOpen = false, completed = false;
    std::atomic_uint opened = 0, closed = 0, consents = 0, events = 0, renewals = 0;
    bool denied = false, unattended = false;
    std::string clipboard = "initial", failure;
    std::filesystem::path root;
    ng_remote_client *client = nullptr;
    Fixture() {
        siteSeed.fill(8);
        controllerSeed.fill(9);
        siteKey = publicKey(siteSeed);
        controllerKey = publicKey(controllerSeed);
        root = std::filesystem::temp_directory_path() /
               ("netgo-host-test-" +
                std::to_string(std::chrono::steady_clock::now().time_since_epoch().count()));
        std::filesystem::create_directories(root / "host");
        std::filesystem::create_directories(root / "viewer");
        std::ofstream(root / "viewer" / "source.txt") << "bounded authenticated transfer";
    }
    void issue() {
        std::string site = "site", user = "user", host = "host", controller = "controller";
        ng_remote_ticket_binding f{};
        f.struct_size = sizeof(f);
        f.abi_version = NG_REMOTE_ABI;
        auto id = [](const std::string &s) {
            return ng_remote_ticket_id{reinterpret_cast<const uint8_t *>(s.data()), s.size()};
        };
        f.site_id = id(site);
        f.user_id = id(user);
        f.host_id = id(host);
        f.controller_id = id(controller);
        std::copy(siteKey.begin(), siteKey.end(), f.site_key);
        std::copy(controllerKey.begin(), controllerKey.end(), f.controller_key);
        auto hostKey = decode64(registration.at("transport_key"), 32),
             boot = decode64(registration.at("boot"), 16);
        std::copy(hostKey.begin(), hostKey.end(), f.host_key);
        std::copy(boot.begin(), boot.end(), f.host_boot);
        Key digest{};
        check(ng_remote_ticket_binding_digest(&f, digest.data()) == 0, "binding");
        std::vector<uint8_t> ticket(168);
        std::memcpy(ticket.data(), "NGRMTK01", 8);
        std::copy(digest.begin(), digest.end(), ticket.begin() + 8);
        std::fill(ticket.begin() + 40, ticket.begin() + 56, 5);
        put(ticket.data() + 56, 1, 8);
        put(ticket.data() + 64, 1, 8);
        put(ticket.data() + 72, 1, 8);
        auto now = uint64_t(std::chrono::duration_cast<std::chrono::milliseconds>(
                                std::chrono::system_clock::now().time_since_epoch())
                                .count());
        put(ticket.data() + 80, now, 8);
        put(ticket.data() + 88, now + 7000, 8);
        put(ticket.data() + 96, 59, 4);
        put(ticket.data() + 100, unattended ? 0 : 1, 4);
        sign(siteSeed, ticket.data(), 104, ticket.data() + 104);
        authorization = {{"server_unix_ms", now},
                         {"site_key", encode64(siteKey.data(), 32)},
                         {"host_key", registration.at("transport_key")},
                         {"controller_key", encode64(controllerKey.data(), 32)},
                         {"ticket", encode64(ticket.data(), ticket.size())},
                         {"endpoint", "127.0.0.1:" + std::to_string(registration.at("port").get<unsigned>())},
                         {"session",
                          {{"id", encode64(ticket.data() + 40, 16)},
                           {"host_id", host},
                           {"controller_id", controller},
                           {"user_id", user},
                           {"binding", encode64(digest.data(), 32)},
                           {"host_boot", registration.at("boot")},
                           {"state", "issued"},
                           {"sequence", 1},
                           {"capabilities", 59}}}};
    }
    Json control(bool host, const std::string &method, const std::string &path, const char *body) {
        // Exercise both server-query delay and an already-captured response arriving late.
        if (host && !unattended && method == "GET" && path.find("/sessions/") != std::string::npos && renewals > 0)
            std::this_thread::sleep_for(std::chrono::milliseconds(400));
        if (!host && method == "GET" && path.find("/sessions/") != std::string::npos)
            std::this_thread::sleep_for(std::chrono::milliseconds(60));
        std::unique_lock lock(mutex);
        if (method == "CONTEXT")
            return {{"site_id", "site"},
                    {"device_id", host ? "host" : "controller"},
                    {"user_id", "user"},
                    {"public_key", encode64(controllerKey.data(), 32)},
                    {"lease", {{"ip", "127.0.0.1"}}}};
        if (method == "PUT") {
            registration = Json::parse(body);
            registered = registration.at("enabled").get<bool>();
            return registration;
        }
        if (path.find("heartbeat") != std::string::npos)
            return {{"ok", true}};
        if (path == "/netgo/v2/remote")
            return {{"next_generation", uint64_t(1)},
                    {"hosts", Json::array({{{"id", "host"},
                                            {"status", "ready"},
                                            {"capabilities", 59},
                                            {"available_capabilities", 59}}})}};
        if (path == "/netgo/v2/remote/sessions") {
            issue();
            return authorization;
        }
        if (path.find("consume") != std::string::npos) {
            authorization["session"]["state"] = "active";
            return authorization;
        }
        if (path.find("/renew") != std::string::npos) {
            auto sequence = authorization.at("session").at("sequence").get<uint64_t>();
            check(Json::parse(body).at("sequence").get<uint64_t>() == sequence, "renew sequence");
            auto wire = decode64(authorization.at("ticket").get<std::string>(), 168);
            auto now = uint64_t(std::chrono::duration_cast<std::chrono::milliseconds>(
                std::chrono::system_clock::now().time_since_epoch()).count());
            put(wire.data()+72, ++sequence, 8);
            put(wire.data()+80, now, 8);
            put(wire.data()+88, now + 7000, 8);
            sign(siteSeed, wire.data(), 104, wire.data()+104);
            authorization["ticket"] = encode64(wire.data(), wire.size());
            authorization["session"]["sequence"] = sequence;
            authorization["server_unix_ms"] = now;
            ++renewals;
            return authorization;
        }
        if (method == "DELETE") {
            authorization["session"]["state"] = "closed";
            return {{"ok", true}};
        }
        auto snapshot = authorization;
        lock.unlock();
        if (host && unattended && method == "GET" && path.find("/sessions/") != std::string::npos && renewals > 0)
            std::this_thread::sleep_for(std::chrono::milliseconds(400));
        return snapshot;
    }
    void submit(const Json &j) {
        auto s = j.dump();
        check(ng_remote_client_command(client, s.c_str(), s.size()) == 0, "submit");
    }
    void event(const Json &e) {
        if (e.contains("error")) {
            failure = e.at("error");
            live = false;
            return;
        }
        if (!e.value("done", false))
            return;
        ++events;
        auto action = e.at("action").get<std::string>();
        if (action == "clipboard.set")
            submit({{"action", "clipboard.get"}});
        else if (action == "clipboard.get") {
            check(e.at("text") == "hello UTF-8 世界", "clipboard");
            submit({{"action", "upload"},
                    {"name", "received.txt"},
                    {"path", path_text(root / "viewer" / "source.txt")}});
        } else if (action == "upload")
            submit({{"action", "file.list"}});
        else if (action == "file.list") {
            check(e.at("files").size() == 1 && e.at("files")[0].at("name") == "received.txt", "list");
            submit({{"action", "download"},
                    {"name", "received.txt"},
                    {"path", path_text(root / "viewer" / "download.txt")}});
        } else if (action == "download") {
            completed = true;
        }
    }
};
struct Context {
    Fixture *f;
    bool host;
};
int32_t control(void *p, const char *method, const char *path, const char *body, const char *, char *output,
                size_t cap, size_t *size) {
    try {
        auto &c = *static_cast<Context *>(p);
        auto s = c.f->control(c.host, method, path, body).dump();
        if (s.size() > cap)
            return -1;
        std::copy(s.begin(), s.end(), output);
        *size = s.size();
        return 0;
    } catch (...) {
        return -1;
    }
}
int main() {
    try {
#ifdef _WIN32
        WSADATA ws{};
        check(WSAStartup(MAKEWORD(2, 2), &ws) == 0, "winsock");
#endif
        for (unsigned scenario = 0; scenario < 3; ++scenario) {
            Fixture f;
            f.unattended = scenario == 1;
            f.denied = scenario == 2;
            Context hostContext{&f, true}, clientContext{&f, false};
            auto folder = path_text(f.root / "host");
            ng_remote_host_options h{};
            h.struct_size = sizeof(h);
            h.abi_version = NG_REMOTE_HOST_ABI;
            h.site_id = "site";
            h.name = "test";
            h.platform = "linux";
            h.exchange_directory = folder.c_str();
            h.capabilities = 59;
            h.unattended = f.unattended;
            h.context = &hostContext;
            h.control = control;
            h.active = [](void *p) -> int32_t { return static_cast<Context *>(p)->f->live; };
            h.consent = [](void *p, const char *, uint32_t) -> int32_t {
                auto &f = *static_cast<Context *>(p)->f;
                ++f.consents;
                return !f.denied;
            };
            h.open = [](void *p, uint32_t, uint32_t *w, uint32_t *h, uint32_t *fps) -> int32_t {
                auto &f = *static_cast<Context *>(p)->f;
                ++f.opened;
                f.mediaOpen = true;
                *w = 640;
                *h = 360;
                *fps = 20;
                return 0;
            };
            h.poll = [](void *, uint32_t *, uint8_t *, size_t, size_t *size) -> int32_t {
                *size = 0;
                return 0;
            };
            h.close = [](void *p) {
                auto &f = *static_cast<Context *>(p)->f;
                ++f.closed;
                f.mediaOpen = false;
            };
            h.input = [](void *, const char *, size_t) -> int32_t { return 0; };
            h.clipboard = [](void *p, int32_t set, const char *text, size_t size, char *out, size_t cap,
                             size_t *n) -> int32_t {
                auto &f = *static_cast<Context *>(p)->f;
                if (set)
                    f.clipboard.assign(text, size);
                if (f.clipboard.size() > cap)
                    return -1;
                std::copy(f.clipboard.begin(), f.clipboard.end(), out);
                *n = f.clipboard.size();
                return 0;
            };
            ng_remote_host *host = nullptr;
            check(ng_remote_host_create(&h, &host) == 0, "host create");
            std::thread hostThread([&] { ng_remote_host_run(host); });
            // Every wait is bounded, including test failures; never orphan a host thread.
            std::atomic_bool done = false;
            std::thread timeout([&] {
                for (unsigned i = 0; i < 1500 && !done; ++i) {
                    if (f.completed && f.renewals >= 4) f.live = false;
                    std::this_thread::sleep_for(std::chrono::milliseconds(10));
                }
                if (!done)
                    f.live = false;
            });
            for (unsigned i = 0; i < 300 && !f.registered && f.live; ++i)
                std::this_thread::sleep_for(std::chrono::milliseconds(10));
            int result = -1;
            if (f.registered) {
                ng_remote_client_options o{};
                o.struct_size = sizeof(o);
                o.abi_version = NG_REMOTE_CLIENT_ABI;
                o.site_id = "site";
                o.host_id = "host";
                o.capabilities = 59;
                o.context = &clientContext;
                o.control = control;
                o.active = h.active;
                o.sign = [](void *p, const uint8_t *data, size_t size, uint8_t out[64]) -> int32_t {
                    try {
                        sign(static_cast<Context *>(p)->f->controllerSeed, data, size, out);
                        return 0;
                    } catch (...) {
                        return -1;
                    }
                };
                o.format = [](void *p, uint32_t, uint32_t, uint32_t, uint32_t) -> int32_t {
                    try {
                        static_cast<Context *>(p)->f->submit(
                            {{"action", "clipboard.set"}, {"text", "hello UTF-8 世界"}});
                        return 0;
                    } catch (...) {
                        return -1;
                    }
                };
                o.video = [](void *, const uint8_t *, size_t, int64_t) -> int32_t { return 0; };
                o.event = [](void *p, const char *text) {
                    auto &f = *static_cast<Context *>(p)->f;
                    try {
                        f.event(Json::parse(text));
                    } catch (const std::exception &e) {
                        f.failure = e.what();
                        f.live = false;
                    }
                };
                if (ng_remote_client_create(&o, &f.client) == 0)
                    result = ng_remote_client_run(f.client);
            }
            f.live = false;
            ng_remote_host_cancel(host);
            hostThread.join();
            done = true;
            timeout.join();
            ng_remote_client_destroy(f.client);
            ng_remote_host_destroy(host);
            if (!f.failure.empty())
                throw std::runtime_error(f.failure);
            check(f.opened == f.closed, "media cleanup");
            check(f.consents == (f.unattended ? 0u : 1u), "consent policy");
            if (f.denied)
                check(!f.completed && f.opened == 0 && result == NG_REMOTE_CLIENT_FAILED, "denied capture");
            else {
                check(f.completed && f.renewals >= 2 && f.events == 5 && f.opened == 1, "authenticated exchange");
                check(std::filesystem::file_size(f.root / "viewer" / "download.txt") == 30,
                      "download integrity");
            }
            check(!f.registered, "host unregister");
            std::filesystem::remove_all(f.root);
        }
        std::cout
            << "host/client TLS, consent, unattended authorization, clipboard and file transfer passed\n";
        return 0;
    } catch (const std::exception &e) {
        std::cerr << e.what() << '\n';
        return 1;
    }
}
