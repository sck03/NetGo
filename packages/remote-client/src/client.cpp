#include "remote_inventory.hpp"
#include "remote_control.hpp"
#include "netgo_remote_client.h"
#include "remote_transport.hpp"
#include "netgo_remote_ticket.h"
#include "netgo_remote_clock.hpp"
#include "netgo_remote_watchdog.hpp"
#include "remote_input.hpp"
#include "remote_exchange.hpp"
#include <nlohmann/json.hpp>
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <algorithm>
#include <atomic>
#include <chrono>
#include <cstring>
#include <mutex>
#include <stdexcept>
#include <thread>
#include <utility>

namespace {
using Json = nlohmann::json;
using Bytes = std::vector<uint8_t>;
using Clock = std::chrono::steady_clock;
using netgo::remote::Key;
using netgo::remote::Kind;
using netgo::remote::Message;
constexpr char base[] = "/netgo/v2/remote";
void require(bool value, const char *code) {
    if (!value)
        throw std::runtime_error(code);
}
std::string encode(const uint8_t *bytes, size_t size) {
    std::string out(4 * ((size + 2) / 3), '\0');
    auto n = EVP_EncodeBlock(reinterpret_cast<unsigned char *>(out.data()), bytes, int(size));
    out.resize(size_t(n));
    for (auto &c : out) {
        if (c == '+')
            c = '-';
        else if (c == '/')
            c = '_';
    }
    while (!out.empty() && out.back() == '=')
        out.pop_back();
    return out;
}
Bytes decode(std::string value, size_t size) {
    require(!value.empty() && value.size() <= 65536 &&
                value.find_first_not_of("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") ==
                    std::string::npos,
            "remote_invalid_encoding");
    auto original = value;
    for (auto &c : value) {
        if (c == '-')
            c = '+';
        else if (c == '_')
            c = '/';
    }
    while (value.size() % 4)
        value += '=';
    Bytes out(value.size() / 4 * 3);
    auto n =
        EVP_DecodeBlock(out.data(), reinterpret_cast<const unsigned char *>(value.data()), int(value.size()));
    require(n >= 0, "remote_invalid_encoding");
    while (!value.empty() && value.back() == '=') {
        --n;
        value.pop_back();
    }
    require(n >= 0 && size_t(n) == size, "remote_invalid_encoding");
    out.resize(size);
    require(encode(out.data(), size) == original, "remote_invalid_encoding");
    return out;
}
Key key(const Json &value) {
    auto b = decode(value.get<std::string>(), 32);
    Key result{};
    std::copy(b.begin(), b.end(), result.begin());
    return result;
}
Message message(Kind kind, const Json &value) {
    auto text = value.dump();
    return {kind, {text.begin(), text.end()}};
}
Json json(const Message &value) {
    require(value.body.size() <= 65536, "remote_invalid_message");
    return Json::parse(value.body);
}
ng_remote_ticket_id id(const std::string &value) {
    return {reinterpret_cast<const uint8_t *>(value.data()), value.size()};
}
struct Gate {
    netgo::remote::ServerClock clock;
    ng_remote_ticket_verifier *verifier = nullptr;
    ng_remote_gate *handle = nullptr;
    Gate(const Json &a, const Json &ctx, const Key &site, uint32_t caps, uint64_t requested)
        : clock(a.value("server_unix_ms", uint64_t(0)), requested) {
        const auto &s = a.at("session");
        std::string siteID = ctx.at("site_id"), user = s.at("user_id"), controllerID = s.at("controller_id"),
                    hostID = s.at("host_id");
        require(ctx.at("device_id") == controllerID && ctx.at("public_key") == a.at("controller_key") &&
                    key(a.at("site_key")) == site,
                "remote_authorization_mismatch");
        auto controller = key(a.at("controller_key")), host = key(a.at("host_key"));
        auto boot = decode(s.at("host_boot"), 16), binding = decode(s.at("binding"), 32);
        ng_remote_ticket_binding fields{};
        fields.struct_size = sizeof(fields);
        fields.abi_version = NG_REMOTE_ABI;
        fields.site_id = id(siteID);
        fields.user_id = id(user);
        fields.controller_id = id(controllerID);
        fields.host_id = id(hostID);
        std::copy(site.begin(), site.end(), fields.site_key);
        std::copy(controller.begin(), controller.end(), fields.controller_key);
        std::copy(host.begin(), host.end(), fields.host_key);
        std::copy(boot.begin(), boot.end(), fields.host_boot);
        Key digest{};
        require(ng_remote_ticket_binding_digest(&fields, digest.data()) == NG_REMOTE_OK &&
                    std::equal(digest.begin(), digest.end(), binding.begin()),
                "remote_binding_mismatch");
        require(ng_remote_ticket_verifier_create(site.data(), site.size(), &verifier) == NG_REMOTE_OK,
                "remote_site_key_invalid");
        ng_remote_options options{};
        options.struct_size = sizeof(options);
        options.abi_version = NG_REMOTE_ABI;
        std::copy(binding.begin(), binding.end(), options.binding_digest);
        options.allowed_capabilities = caps;
        options.consent_mode = NG_REMOTE_LOCAL_CONSENT;
        options.verify = ng_remote_ticket_verify;
        options.verify_context = verifier;
        clock.configure(options);
        if (ng_remote_gate_create(&options, &handle) != NG_REMOTE_OK) {
            ng_remote_ticket_verifier_destroy(verifier);
            verifier = nullptr;
            throw std::runtime_error("remote_gate_failed");
        }
    }
    ~Gate() {
        ng_remote_gate_destroy(handle);
        ng_remote_ticket_verifier_destroy(verifier);
    }
    bool check(uint32_t cap = NG_REMOTE_VIEW) { return ng_remote_gate_check(handle, cap) == NG_REMOTE_OK; }
    void begin(const Bytes &ticket) {
        auto r = ng_remote_gate_begin(handle, ticket.data(), ticket.size());
        require(r == NG_REMOTE_OK || r == NG_REMOTE_CONSENT_REQUIRED, "remote_ticket_invalid");
        require(ng_remote_gate_consent(handle, 1) == NG_REMOTE_OK, "remote_ticket_expired");
    }
};
} // namespace

struct ng_remote_client {
    ng_remote_client_options options;
    std::string site, host;
    std::atomic_bool cancelled = false, started = false;
    std::mutex controlMutex;
    std::vector<char> controlBuffer = std::vector<char>(4 << 20);
    netgo::remote::Exchange exchange{[this](const Json &j) {
        if (options.event) {
            auto s = j.dump();
            options.event(options.context, s.c_str());
        }
    }};
    bool active() { return !cancelled && options.active(options.context) != 0; }
    void status(const char *code) {
        if (options.status)
            options.status(options.context, code);
    }
    Json call(const std::string &method, const std::string &path, const Json &value = nullptr,
              const std::string &idem = {}) {
        std::lock_guard lock(controlMutex);
        auto &output = controlBuffer;
        size_t length = 0;
        auto body = value.is_null() ? std::string() : value.dump();
        require(options.control(options.context, method.c_str(), path.c_str(), body.c_str(), idem.c_str(),
                                output.data(), output.size(), &length) == 0 &&
                    length && length <= output.size(),
                "remote_control_failed");
        auto result = Json::parse(output.data(), output.data() + length);
        if (result.is_object() && result.size() == 1 && result.contains("error"))
            throw std::runtime_error(result.at("error").get<std::string>());
        return result;
    }
    Message receive(netgo::remote::Connection &c, Kind expected) {
        auto until = Clock::now() + std::chrono::seconds(20);
        Message m;
        while (active() && Clock::now() < until) {
            if (c.receive(m, 100)) {
                require(m.kind == expected, "remote_unexpected_message");
                return m;
            }
        }
        throw std::runtime_error("remote_handshake_timeout");
    }
    void close(const std::string &session) {
        try {
            call("DELETE", std::string(base) + "/sessions/" + session);
        } catch (...) {
        }
    }
};

namespace {
struct Monitor {
    ng_remote_client &client;
    std::string session;
    std::atomic_bool stop = false;
    netgo::remote::Watchdog watchdog;
    std::mutex mutex;
    Json renewal;
    std::thread worker;
    Monitor(ng_remote_client &c, std::string s) : client(c), session(std::move(s)) {
        worker = std::thread([this] {
            try {
                uint64_t sequence = 0;
                while (live()) {
                    const auto nextPoll = netgo::remote::monotonic_time_ms() + 3000;
                    try {
                        // Observe host consumption once. Renewal itself revalidates the
                        // live authorization and returns the next sequence atomically.
                        if (!sequence) {
                            auto a = client.call("GET", std::string(base) + "/sessions/" + session);
                            if (!live()) break;
                            require(a.at("session").value("state", "") != "closed", "remote_revoked");
                            if (a.at("session").value("state", "") == "active")
                                sequence = a.at("session").at("sequence").get<uint64_t>();
                        }
                        if (sequence) {
                            auto requested = netgo::remote::monotonic_time_ms();
                            auto fresh =
                                client.call("POST", std::string(base) + "/sessions/" + session + "/renew",
                                            {{"sequence", sequence}});
                            if (!live())
                                break;
                            const auto nextSequence = fresh.at("session").at("sequence").get<uint64_t>();
                            require(nextSequence > sequence && fresh.at("session").value("state", "") == "active", "remote_renewal_invalid");
                            sequence = nextSequence;
                            fresh["netgo_requested_mono_ms"] = requested;
                            std::lock_guard lock(mutex);
                            renewal = std::move(fresh);
                        }
                        // Request time is part of the polling interval. Adding three more seconds
                        // after slow successful calls can expire our unchanged six-second watchdog.
                        if (!watchdog.refresh())
                            break;
                    } catch (const std::exception& error) {
                        if (!netgo::remote::transient_control(error)) throw;
                        // The renewal may have committed before its reply was lost.
                        // Re-read its sequence; never replay a mutation blindly.
                        sequence = 0;
                        for (int i=0;i<5&&live();++i) std::this_thread::sleep_for(std::chrono::milliseconds(50));
                        continue;
                    }
                    while (live() && netgo::remote::monotonic_time_ms() < nextPoll)
                        std::this_thread::sleep_for(std::chrono::milliseconds(100));
                }
            } catch (...) {
            }
            watchdog.close();
        });
    }
    ~Monitor() {
        stop = true;
        watchdog.close();
        if (worker.joinable())
            worker.join();
    }
    bool live() { return !stop && client.active() && watchdog.live(); }
    Json take() {
        std::lock_guard lock(mutex);
        return std::exchange(renewal, Json());
    }
};
void run(ng_remote_client &c) {
    auto ctx = c.call("CONTEXT", "");
    require(ctx.at("site_id") == c.site, "remote_site_mismatch");
    auto inventory = netgo::remote::inventory([&](const std::string& path){return c.call("GET",path);},c.host);
    uint32_t available = 0;
    bool found = false;
    for (const auto &h : inventory.at("hosts"))
        if (h.at("id") == c.host) {
            found = true;
            auto state = h.at("status").get<std::string>();
            require(state != "busy", "remote_host_busy");
            require(state == "ready", "remote_host_unavailable");
            available = h.at("available_capabilities").get<uint32_t>() & h.at("capabilities").get<uint32_t>();
        }
    require(found, "remote_host_unavailable");
    auto requested = c.options.capabilities & available;
    require((requested & NG_REMOTE_VIEW) != 0, "remote_view_forbidden");
    Key nonce{};
    require(RAND_bytes(nonce.data(), nonce.size()) == 1, "remote_random_failed");
    auto next = inventory.find("next_generation");
    require(next != inventory.end() && next->is_number_unsigned(), "remote_generation_invalid");
    auto generation = next->get<uint64_t>();
    require(generation > 0 && generation <= uint64_t(INT64_MAX), "remote_generation_invalid");
    auto requestedAt = netgo::remote::monotonic_time_ms();
    auto a = c.call("POST", std::string(base) + "/sessions",
                    {{"host_id", c.host}, {"generation", generation}, {"capabilities", requested}},
                    encode(nonce.data(), nonce.size()));
    auto session = a.at("session").at("id").get<std::string>();
    try {
        require(a.at("session").at("host_id") == c.host, "remote_host_mismatch");
        require(a.at("session").at("capabilities") == requested, "remote_capabilities_mismatch");
        Gate gate(a, ctx, key(a.at("site_key")), requested, requestedAt);
        auto ticket = decode(a.at("ticket"), NG_REMOTE_TICKET_SIZE);
        gate.begin(ticket);
        Monitor monitor(c, session);
        auto endpoint = a.at("endpoint").get<std::string>();
        auto colon = endpoint.rfind(':');
        require(colon != std::string::npos, "remote_invalid_endpoint");
        auto ip = endpoint.substr(0, colon);
        if (ip.size() > 2 && ip.front() == '[' && ip.back() == ']')
            ip = ip.substr(1, ip.size() - 2);
        auto port = std::stoul(endpoint.substr(colon + 1));
        require(port > 0 && port <= 65535, "remote_invalid_endpoint");
        auto connection = netgo::remote::Connection::connect(ip, uint16_t(port), key(a.at("host_key")),
                                                             [&] { return monitor.live() && gate.check(); });
        connection.send(message(Kind::hello, {{"id", session}, {"ticket", a.at("ticket")}}));
        auto challenge = c.receive(connection, Kind::challenge);
        require(challenge.body.size() == 32, "remote_challenge_invalid");
        auto channel = connection.exporter();
        Key digest{};
        unsigned count = 0;
        require(EVP_Digest(ticket.data(), ticket.size(), digest.data(), &count, EVP_sha256(), nullptr) == 1 &&
                    count == 32,
                "remote_hash_failed");
        std::string proof("NetGo remote possession v1\0", 27);
        proof.append(reinterpret_cast<char *>(digest.data()), 32);
        proof.append(reinterpret_cast<char *>(challenge.body.data()), 32);
        proof.append(reinterpret_cast<char *>(channel.data()), 32);
        Bytes signature(64);
        require(c.options.sign(c.options.context, reinterpret_cast<const uint8_t *>(proof.data()),
                               proof.size(), signature.data()) == 0,
                "remote_sign_failed");
        connection.send({Kind::proof, std::move(signature)});
        c.status("remote_waiting_consent");
        auto format = json(c.receive(connection, Kind::accepted));
        auto width = format.at("width").get<uint32_t>(), height = format.at("height").get<uint32_t>(),
             fps = format.at("fps").get<uint32_t>();
        auto caps = a.at("session").at("capabilities").get<uint32_t>();
        require(width >= 2 && width <= 8192 && height >= 2 && height <= 8192 && fps >= 1 && fps <= 120 &&
                    format.value("capabilities", caps) == caps,
                "remote_format_unsupported");
        require(c.options.format(c.options.context, width, height, fps, caps) == 0,
                "remote_decoder_unavailable");
        c.status("remote_connected");
        int64_t stamp = 0;
        while (monitor.live() && gate.check()) {
            auto renewal = monitor.take();
            if (!renewal.is_null()) {
                auto bytes = decode(renewal.at("ticket"), NG_REMOTE_TICKET_SIZE);
                gate.clock.observe(renewal.value("server_unix_ms", uint64_t(0)),
                                   renewal.at("netgo_requested_mono_ms").get<uint64_t>());
                require(ng_remote_gate_renew(gate.handle, bytes.data(), bytes.size()) == NG_REMOTE_OK,
                        "remote_renewal_invalid");
                connection.enqueue(message(Kind::renew, {{"ticket", renewal.at("ticket")}}));
            }
            if (c.options.input && gate.check(NG_REMOTE_INPUT)) {
                std::array<char, 65536> input{};
                size_t size = 0;
                require(c.options.input(c.options.context, input.data(), input.size(), &size) == 0 &&
                            size <= input.size(),
                        "remote_invalid_input");
                if (size) {
                    auto events = Json::parse(input.data(), input.data() + size);
                    require(events.is_array(), "remote_invalid_input");
                    if (!events.empty()) {
                        require(netgo::remote::valid_input_batch(events), "remote_invalid_input");
                        connection.enqueue(message(Kind::input, events));
                    }
                }
            }
            if (c.options.tick)
                c.options.tick(c.options.context);
            ng_remote_snapshot current{};
            current.struct_size = sizeof(current);
            current.abi_version = NG_REMOTE_ABI;
            require(ng_remote_gate_poll(gate.handle, &current) == NG_REMOTE_OK, "remote_closed");
            if (!connection.outputPending()) {
                for (unsigned burst = 0; burst < 4; ++burst) {
                    auto command = c.exchange.next(current.capabilities);
                    if (command.is_null()) break;
                    connection.enqueue(message(Kind::command, command));
                }
            }
            connection.flush();
            Message m;
            if (!connection.receive(m, 20))
                continue;
            if (m.kind == Kind::close)
                break;
            if (m.kind == Kind::video) {
                require(c.options.video(c.options.context, m.body.data(), m.body.size(), stamp) == 0,
                        "remote_decoder_failed");
                stamp += 10000000 / fps;
            } else if (m.kind == Kind::audio) {
                require(gate.check(NG_REMOTE_AUDIO) && c.options.audio &&
                            c.options.audio(c.options.context, m.body.data(), m.body.size()) == 0,
                        "remote_audio_unavailable");
            } else if (m.kind == Kind::response)
                c.exchange.receive(json(m));
            else
                throw std::runtime_error("remote_unexpected_message");
        }
    } catch (...) {
        c.exchange.reset();
        c.close(session);
        throw;
    }
    c.exchange.reset();
    c.close(session);
    c.status("remote_disconnected");
}
} // namespace
extern "C" int32_t ng_remote_client_create(const ng_remote_client_options *o, ng_remote_client **output) {
    if (!output)
        return NG_REMOTE_CLIENT_INVALID;
    *output = nullptr;
    if (!o || o->struct_size != sizeof(*o) || o->abi_version != NG_REMOTE_CLIENT_ABI || !o->site_id ||
        !o->host_id || !o->active || !o->control || !o->sign || !o->format || !o->video ||
        !(o->capabilities & NG_REMOTE_VIEW) || (o->capabilities & ~uint32_t(NG_REMOTE_ALL_CAPABILITIES)) ||
        (o->capabilities & NG_REMOTE_AUDIO && !o->audio))
        return NG_REMOTE_CLIENT_INVALID;
    try {
        auto p = std::make_unique<ng_remote_client>();
        p->options = *o;
        p->site = o->site_id;
        p->host = o->host_id;
        if (p->site.empty() || p->site.size() > 63 || p->host.empty() || p->host.size() > 128)
            return NG_REMOTE_CLIENT_INVALID;
        *output = p.release();
        return NG_REMOTE_CLIENT_OK;
    } catch (...) {
        return NG_REMOTE_CLIENT_FAILED;
    }
}
extern "C" int32_t ng_remote_client_run(ng_remote_client *c) {
    if (!c)
        return NG_REMOTE_CLIENT_INVALID;
    if (c->started.exchange(true))
        return NG_REMOTE_CLIENT_BUSY;
    struct Cleanup {
        ng_remote_client *client;
        ~Cleanup() {
            client->cancelled = true;
            client->exchange.reset();
        }
    } cleanup{c};
    try {
        run(*c);
        return NG_REMOTE_CLIENT_OK;
    } catch (const std::exception &e) {
        if (!c->active()) {
            c->status("remote_disconnected");
            return NG_REMOTE_CLIENT_OK;
        }
        c->status(e.what());
        return NG_REMOTE_CLIENT_FAILED;
    } catch (...) {
        c->status("remote_failed");
        return NG_REMOTE_CLIENT_FAILED;
    }
}
extern "C" int32_t ng_remote_client_command(ng_remote_client *c, const char *data, size_t size) {
    if (!c || !data || !size || size > 65536 || c->cancelled)
        return NG_REMOTE_CLIENT_INVALID;
    try {
        return c->exchange.push(Json::parse(data, data + size)) ? NG_REMOTE_CLIENT_OK : NG_REMOTE_CLIENT_BUSY;
    } catch (...) {
        return NG_REMOTE_CLIENT_INVALID;
    }
}
extern "C" void ng_remote_client_cancel(ng_remote_client *c) {
    if (c)
        c->cancelled = true;
}
extern "C" void ng_remote_client_destroy(ng_remote_client *c) { delete c; }
