#include "netgo_remote_host.h"
#include "remote_control.hpp"
#include "netgo_remote_ticket.h"
#include "netgo_remote_clock.hpp"
#include "netgo_remote_watchdog.hpp"
#include "netgo_remote_session_fence.hpp"
#include "remote_transport.hpp"
#include "remote_input.hpp"
#include "remote_files.hpp"
#include "remote_commands.hpp"
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <openssl/crypto.h>
#include <atomic>
#include <chrono>
#include <mutex>
#include <thread>
#include <stdexcept>
#include <algorithm>
#include <cstring>
namespace {
using namespace netgo::remote;
using Json = nlohmann::json;
using Bytes = std::vector<uint8_t>;
using Clock = std::chrono::steady_clock;
constexpr char base[] = "/netgo/v2/remote";
void require(bool v, const char *e) {
    if (!v)
        throw std::runtime_error(e);
}
Bytes decode(const Json &v, size_t n) {
    auto b = decode64(v.get<std::string>(), n);
    require(b.size() == n, "remote_invalid_encoding");
    return b;
}
Key key(const Json &j) {
    auto b = decode(j, 32);
    Key k{};
    std::copy(b.begin(), b.end(), k.begin());
    return k;
}
Message message(Kind kind, const Json &j) {
    auto s = j.dump();
    require(s.size() <= 65536, "remote_invalid_message");
    return {kind, {s.begin(), s.end()}};
}
Json json(const Message &m) {
    require(m.body.size() <= 65536, "remote_invalid_message");
    return Json::parse(m.body);
}
struct Gate {
    ServerClock clock;
    ng_remote_ticket_verifier *verifier = nullptr;
    ng_remote_gate *value = nullptr;
    ng_remote_claims offered{};
    Gate(const Json &a, const Json &ctx, const Key &host, uint32_t caps, bool unattended, uint64_t requested)
        : clock(a.value("server_unix_ms", uint64_t(0)), requested) {
        const auto &s = a.at("session");
        require(ctx.at("device_id") == s.at("host_id") && key(a.at("host_key")) == host,
                "remote_host_mismatch");
        auto site = key(a.at("site_key")), controller = key(a.at("controller_key"));
        auto boot = decode(s.at("host_boot"), 16), binding = decode(s.at("binding"), 32);
        std::string siteID = ctx.at("site_id"), user = s.at("user_id"), control = s.at("controller_id"),
                    device = s.at("host_id");
        auto id = [](const std::string &v) {
            return ng_remote_ticket_id{reinterpret_cast<const uint8_t *>(v.data()), v.size()};
        };
        ng_remote_ticket_binding f{};
        f.struct_size = sizeof(f);
        f.abi_version = NG_REMOTE_ABI;
        f.site_id = id(siteID);
        f.user_id = id(user);
        f.controller_id = id(control);
        f.host_id = id(device);
        std::copy(site.begin(), site.end(), f.site_key);
        std::copy(controller.begin(), controller.end(), f.controller_key);
        std::copy(host.begin(), host.end(), f.host_key);
        std::copy(boot.begin(), boot.end(), f.host_boot);
        Key digest{};
        require(ng_remote_ticket_binding_digest(&f, digest.data()) == NG_REMOTE_OK &&
                    std::equal(digest.begin(), digest.end(), binding.begin()),
                "remote_binding_mismatch");
        require(ng_remote_ticket_verifier_create(site.data(), site.size(), &verifier) == NG_REMOTE_OK,
                "remote_invalid_site_key");
        ng_remote_options o{};
        o.struct_size = sizeof(o);
        o.abi_version = NG_REMOTE_ABI;
        std::copy(binding.begin(), binding.end(), o.binding_digest);
        o.allowed_capabilities = caps;
        o.consent_mode = unattended ? NG_REMOTE_ALLOW_UNATTENDED : NG_REMOTE_LOCAL_CONSENT;
        o.verify = ng_remote_ticket_verify;
        o.verify_context = verifier;
        clock.configure(o);
        if (ng_remote_gate_create(&o, &value) != NG_REMOTE_OK) {
            ng_remote_ticket_verifier_destroy(verifier);
            verifier = nullptr;
            throw std::runtime_error("remote_gate_failed");
        }
    }
    ~Gate() {
        ng_remote_gate_destroy(value);
        ng_remote_ticket_verifier_destroy(verifier);
    }
    bool check(uint32_t cap = NG_REMOTE_VIEW) { return ng_remote_gate_check(value, cap) == NG_REMOTE_OK; }
    uint64_t offer(const Json &ticket) {
        auto bytes = decode(ticket, NG_REMOTE_TICKET_SIZE);
        ng_remote_claims next{};
        next.struct_size = sizeof(next);
        next.abi_version = NG_REMOTE_ABI;
        require(ng_remote_ticket_verify(verifier, bytes.data(), bytes.size(), &next) == NG_REMOTE_OK,
                "remote_renewal_invalid");
        if (offered.sequence) {
            require(std::memcmp(next.binding_digest, offered.binding_digest, 32) == 0 &&
                        std::memcmp(next.session_id, offered.session_id, 16) == 0 &&
                        next.generation == offered.generation && next.sequence > offered.sequence &&
                        next.policy_revision >= offered.policy_revision &&
                        next.issued_unix_ms >= offered.issued_unix_ms &&
                        !(next.capabilities & ~offered.capabilities), "remote_renewal_invalid");
        }
        offered = next;
        return next.sequence;
    }
};
} // namespace
struct ng_remote_host {
    ng_remote_host_options o{};
    std::string site, name, platform, directory;
    std::atomic_bool cancelled = false, started = false;
    std::mutex control;
    std::vector<char> controlBuffer = std::vector<char>(4 << 20);
    bool active() { return !cancelled && o.active(o.context); }
    void status(const std::string &s) {
        if (o.status)
            o.status(o.context, s.c_str());
    }
    Json call(const char *method, const std::string &path, const Json &body = nullptr) {
        std::lock_guard lock(control);
        auto &out = controlBuffer;
        size_t size = 0;
        auto text = body.is_null() ? std::string() : body.dump();
        require(o.control(o.context, method, path.c_str(), text.c_str(), "", out.data(), out.size(), &size) ==
                        0 &&
                    size && size <= out.size(),
                "remote_control_failed");
        auto j = Json::parse(out.data(), out.data() + size);
        if (j.is_object() && j.size() == 1 && j.contains("error"))
            throw std::runtime_error(j.at("error").get<std::string>());
        return j;
    }
};
namespace {
struct Monitor {
    ng_remote_host &host;
    std::string boot;
    Watchdog watchdog;
    SessionFence session;
    std::atomic_bool stop = false;
    std::mutex snapshotMutex;
    std::string snapshotID;
    Json snapshot;
    uint64_t observed = 0;
    std::thread worker;
    Monitor(ng_remote_host &h, std::string b)
        : host(h), boot(std::move(b)), worker([this] {
              try {
                  while (live()) {
                      auto next = monotonic_time_ms() + 3000;
                      try {
                          host.call("POST", std::string(base) + "/host/heartbeat", {{"boot", boot}});
                          if (!live())
                              break;
                          auto s = session.snapshot();
                          if (!s.second.empty()) {
                              try {
                                  auto sent = monotonic_time_ms();
                                  auto a = host.call("GET", std::string(base) + "/sessions/" + s.second);
                                  if (a.at("session").value("state", "") == "closed")
                                      session.reject(s);
                                  else {
                                      std::lock_guard lock(snapshotMutex);
                                      snapshotID = s.second;
                                      snapshot = std::move(a);
                                      observed = sent;
                                  }
                              } catch (const std::exception& error) {
                                  if (netgo::remote::transient_control(error)) throw;
                                  session.reject(s);
                              }
                          }
                          if (!watchdog.refresh())
                              break;
                      } catch (const std::exception& error) {
                          if (!netgo::remote::transient_control(error)) throw;
                          for (int i=0;i<5&&live();++i) std::this_thread::sleep_for(std::chrono::milliseconds(50));
                          continue;
                      }
                      while (live() && monotonic_time_ms() < next)
                          std::this_thread::sleep_for(std::chrono::milliseconds(50));
                  }
              } catch (...) {
              }
              watchdog.close();
          }) {}
    ~Monitor() {
        stop = true;
        watchdog.close();
        if (worker.joinable())
            worker.join();
    }
    bool live() { return !stop && host.active() && watchdog.live(); }
    bool authorized() { return live() && session.live(); }
    bool renewal(const std::string &id, uint64_t sequence, Json &out, uint64_t &at) {
        std::lock_guard lock(snapshotMutex);
        if (snapshotID != id || !snapshot.is_object() ||
            snapshot.at("session").at("sequence").get<uint64_t>() < sequence ||
            monotonic_time_ms() - observed > 5000) return false;
        out = snapshot;
        at = observed;
        return true;
    }
};
Message receive(Connection &c, Kind kind, const Continue &allowed) {
    Message m;
    auto end = Clock::now() + std::chrono::seconds(20);
    while (allowed() && Clock::now() < end) {
        if (c.receive(m, 50)) {
            require(m.kind == kind, "remote_unexpected_message");
            return m;
        }
    }
    throw std::runtime_error("remote_handshake_timeout");
}
uint32_t commandCapability(const std::string& action) {
    return action.rfind("clipboard.", 0) == 0 ? NG_REMOTE_CLIPBOARD
                     : action == "file.begin" || action == "file.write" || action == "file.commit"
                         ? NG_REMOTE_UPLOAD
                     : action == "file.cancel" ? NG_REMOTE_VIEW
                                               : NG_REMOTE_DOWNLOAD;
}
Json command(ng_remote_host &h, Gate &gate, Files *files, const Json &req, const Continue& active) {
    const auto action = req.at("action").get<std::string>();
    const auto cap = commandCapability(action);
    auto allowed = [&] { return active() && gate.check(cap); };
    require(allowed(), "remote_command_forbidden");
    if (action == "clipboard.get" || action == "clipboard.set") {
        require(h.o.clipboard, "remote_clipboard_unavailable");
        auto text = req.value("text", std::string());
        require(text.size() <= 8192 && text.find('\0') == std::string::npos, "remote_clipboard_too_large");
        std::array<char, 8192> output{};
        size_t n = 0;
        require(h.o.clipboard(h.o.context, action == "clipboard.set", text.data(), text.size(), output.data(),
                              output.size(), &n) == 0 &&
                    n <= output.size(),
                "remote_clipboard_unavailable");
        return {{"text", std::string(output.data(), n)}};
    }
    require(files, "remote_files_unavailable");
    if (action == "file.list")
        return {{"files", files->list()}};
    if (action == "file.begin")
        return {{"token", files->begin(req.at("name"), req.at("size"))}, {"offset", 0}};
    if (action == "file.write")
        return {{"offset",
                 files->write(req.at("token"), req.at("offset"), decode64(req.at("data"), file_chunk))}};
    if (action == "file.commit") {
        files->commit(req.at("token"), req.at("sha256"), allowed);
        return {{"done", true}};
    }
    if (action == "file.open")
        return files->open(req.at("name"));
    if (action == "file.read")
        return files->read(req.at("token"), req.at("offset"));
    if (action == "file.cancel") {
        files->cancel();
        return {{"done", true}};
    }
    throw std::runtime_error("remote_invalid_command");
}
void run(ng_remote_host &h) {
    auto ctx = h.call("CONTEXT", "");
    require(ctx.at("site_id") == h.site, "remote_site_mismatch");
    std::string device = ctx.at("device_id"), ip = ctx.at("lease").at("ip");
    Key seed{}, publicKey{};
    require(RAND_bytes(seed.data(), int(seed.size())) == 1, "remote_random_failed");
    auto pk = std::unique_ptr<EVP_PKEY, decltype(&EVP_PKEY_free)>(
        EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr, seed.data(), seed.size()), EVP_PKEY_free);
    size_t kn = 32;
    require(pk && EVP_PKEY_get_raw_public_key(pk.get(), publicKey.data(), &kn) == 1 && kn == 32,
            "remote_key_failed");
    Listener listener(ip, 29990, seed, [&] { return h.active(); });
    OPENSSL_cleanse(seed.data(), seed.size());
    pk.reset();
    std::array<uint8_t, 16> bootBytes{};
    require(RAND_bytes(bootBytes.data(), int(bootBytes.size())) == 1, "remote_random_failed");
    auto boot = encode64(bootBytes.data(), bootBytes.size());
    Json registration = {{"id", device},
                         {"name", h.name},
                         {"platform", h.platform},
                         {"transport_key", encode64(publicKey.data(), publicKey.size())},
                         {"boot", boot},
                         {"enabled", true},
                         {"capabilities", h.o.capabilities},
                         {"require_consent", !h.o.unattended},
                         {"port", listener.port()}};
    h.call("PUT", std::string(base) + "/host", registration);
    auto unregister = [&] {
        registration["enabled"] = false;
        try {
            h.call("PUT", std::string(base) + "/host", registration);
        } catch (...) {
        }
    };
    try {
        Monitor monitor(h, boot);
        h.status(h.o.unattended ? "remote_host_unattended" : "remote_host_waiting");
        while (monitor.live()) {
            std::unique_ptr<Connection> connection;
            try {
                connection = listener.accept(100);
            } catch (...) {
                if (!monitor.live())
                    break;
                std::this_thread::sleep_for(std::chrono::milliseconds(50));
                continue;
            }
            if (!connection)
                continue;
            std::string id;
            bool media = false;
            try {
                auto hello = json(receive(*connection, Kind::hello, [&] { return monitor.live(); }));
                id = hello.at("id");
                require(id.size() == 22 &&
                            id.find_first_not_of(
                                "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") ==
                                std::string::npos,
                        "remote_invalid_session");
                auto requested = monotonic_time_ms();
                auto a = h.call("GET", std::string(base) + "/sessions/" + id);
                const auto &s = a.at("session");
                require(s.at("host_id") == device && s.at("host_boot") == boot && s.at("state") == "issued" &&
                            hello.at("ticket") == a.at("ticket"),
                        "remote_authorization_mismatch");
                auto wire = decode(a.at("ticket"), NG_REMOTE_TICKET_SIZE);
                Gate gate(a, ctx, publicKey, h.o.capabilities, h.o.unattended != 0, requested);
                auto result = ng_remote_gate_begin(gate.value, wire.data(), wire.size());
                require(result == NG_REMOTE_OK || result == NG_REMOTE_CONSENT_REQUIRED,
                        "remote_ticket_invalid");
                Key challenge{};
                require(RAND_bytes(challenge.data(), int(challenge.size())) == 1, "remote_random_failed");
                auto channel = connection->exporter();
                connection->send({Kind::challenge, {challenge.begin(), challenge.end()}});
                auto proof = receive(*connection, Kind::proof, [&] { return monitor.live(); });
                auto controller = key(a.at("controller_key"));
                require(proof.body.size() == 64 &&
                            ng_remote_possession_verify(controller.data(), wire.data(), wire.size(),
                                                        challenge.data(), channel.data(),
                                                        proof.body.data()) == NG_REMOTE_OK,
                        "remote_proof_invalid");
                if (result == NG_REMOTE_CONSENT_REQUIRED) {
                    auto user = s.at("user_id").get<std::string>();
                    require(h.o.consent && h.o.consent(h.o.context, user.c_str(), s.at("capabilities")) == 1,
                            "remote_consent_denied");
                    require(ng_remote_gate_consent(gate.value, 1) == NG_REMOTE_OK, "remote_ticket_expired");
                }
                require(gate.check(), "remote_ticket_expired");
                h.call("POST", std::string(base) + "/sessions/" + id + "/consume",
                       {{"sequence", s.at("sequence")},
                        {"boot", boot},
                        {"proof",
                         {{"challenge", encode64(challenge.data(), challenge.size())},
                          {"channel", encode64(channel.data(), channel.size())},
                          {"signature", encode64(proof.body.data(), proof.body.size())}}}});
                monitor.session.bind(id);
                connection->authorize([&] { return monitor.authorized() && gate.check(); });
                uint32_t w = 0, height = 0, fps = 0;
                media = true;
                require(h.o.open(h.o.context, s.at("capabilities"), &w, &height, &fps) == 0 && w >= 16 &&
                            w <= 4096 && height >= 16 && height <= 2160 && fps >= 1 && fps <= 60,
                        "remote_capture_unavailable");
                std::unique_ptr<Files> files;
                if (h.o.capabilities & (NG_REMOTE_UPLOAD | NG_REMOTE_DOWNLOAD))
                    files = std::make_unique<Files>(utf8_path(h.directory));
                CommandWorker commandWorker([&](const Json& request, const Continue& running) {
                    auto authorized = [&] { return running() && monitor.authorized() && gate.check(); };
                    auto result = command(h, gate, files.get(), request, authorized);
                    require(authorized(), "remote_closed");
                    return result;
                }, [&] { if (files) files->cancel(); });
                connection->send(message(Kind::accepted, {{"width", w},
                                                          {"height", height},
                                                          {"fps", fps},
                                                          {"capabilities", s.at("capabilities")}}));
                h.status("remote_host_connected");
                Bytes buffer(max_message);
                uint64_t commandWindow = monotonic_time_ms(), lastCommand = commandWindow;
                unsigned commands = 0;
                uint64_t appliedSequence = gate.offer(a.at("ticket"));
                uint64_t pendingRenewal = 0;
                while (monitor.authorized() && gate.check()) {
                    // Control IO belongs to Monitor. Capture/input continue using
                    // the old, bounded ticket while a fresh authorization arrives.
                    Json fresh;
                    uint64_t observed = 0;
                    if (pendingRenewal && monitor.renewal(id, appliedSequence + 1, fresh, observed)) {
                        bool hadInput = gate.check(NG_REMOTE_INPUT), hadUpload = gate.check(NG_REMOTE_UPLOAD),
                             hadDownload = gate.check(NG_REMOTE_DOWNLOAD);
                        // Snapshots may lead or lag offers. Advance on any newer authoritative
                        // sequence, keeping a later offer pending instead of starving renewal.
                        auto bytes = decode(fresh.at("ticket"), NG_REMOTE_TICKET_SIZE);
                        gate.clock.observe(fresh.at("server_unix_ms"), observed);
                        require(ng_remote_gate_renew(gate.value, bytes.data(), bytes.size()) == NG_REMOTE_OK,
                                "remote_renewal_invalid");
                        appliedSequence = fresh.at("session").at("sequence").get<uint64_t>();
                        if (pendingRenewal <= appliedSequence) pendingRenewal = 0;
                        if (hadInput && !gate.check(NG_REMOTE_INPUT) && h.o.input) {
                            constexpr char release[] = R"({"type":"release"})";
                            require(h.o.input(h.o.context, release, sizeof(release)-1) == 0, "remote_input_unavailable");
                        }
                        if (files && ((hadUpload && !gate.check(NG_REMOTE_UPLOAD)) ||
                                      (hadDownload && !gate.check(NG_REMOTE_DOWNLOAD)))) commandWorker.cancel();
                    }
                    if (files && monotonic_time_ms() - lastCommand > 30000) {
                        commandWorker.cancel();
                        lastCommand = monotonic_time_ms();
                    }
                    connection->flush();
                    if (!connection->outputPending()) {
                        auto response = commandWorker.take();
                        if (!response.is_null()) {
                            require(monitor.authorized() && gate.check(), "remote_closed");
                            if (!gate.check(commandCapability(response.at("_action")))) {
                                response.erase("result");
                                response["error"] = "remote_command_forbidden";
                            }
                            response.erase("_action");
                            connection->enqueue(message(Kind::response, response));
                        }
                    }
                    Message m;
                    if (connection->receive(m, 0)) {
                        if (m.kind == Kind::close)
                            break;
                        if (m.kind == Kind::renew) {
                            auto update = json(m);
                            auto sequence = gate.offer(update.at("ticket"));
                            if (sequence > appliedSequence) pendingRenewal = sequence;
                        } else if (m.kind == Kind::input) {
                            auto events = json(m);
                            require(valid_input_batch(events), "remote_invalid_input");
                            for (const auto &event : events) {
                                require(monitor.authorized() && gate.check(NG_REMOTE_INPUT),
                                        "remote_input_forbidden");
                                auto text = event.dump();
                                require(h.o.input, "remote_input_unavailable");
                                auto result = h.o.input(h.o.context, text.data(), text.size());
                                require(result >= 0, "remote_input_unavailable");
                                if (result > 0) {
                                    constexpr char release[] = "{\"type\":\"release\"}";
                                    require(h.o.input(h.o.context, release, sizeof(release) - 1) == 0,
                                            "remote_input_unavailable");
                                    break;
                                }
                            }
                        } else if (m.kind == Kind::command) {
                            lastCommand = monotonic_time_ms();
                            if (lastCommand - commandWindow >= 1000) {
                                commands = 0;
                                commandWindow = lastCommand;
                            }
                            require(++commands <= 256, "remote_command_rate_exceeded");
                            auto req = json(m);
                            require(req.contains("id") && req.at("id").is_number_unsigned(), "remote_invalid_command");
                            require(commandWorker.submit(std::move(req)), "remote_command_busy");
                        } else
                            throw std::runtime_error("remote_unexpected_message");
                    }
                    if (connection->outputPending()) {
                        std::this_thread::sleep_for(std::chrono::milliseconds(1));
                        continue;
                    }
                    size_t size = 0;
                    uint32_t kind = 5;
                    require(h.o.poll(h.o.context, &kind, buffer.data(), buffer.size(), &size) == 0 &&
                                size <= buffer.size(),
                            "remote_capture_failed");
                    if (size) {
                        require(kind == 5 || kind == 10, "remote_invalid_media");
                        if (kind == 10 && !gate.check(NG_REMOTE_AUDIO)) continue;
                        require(monitor.authorized() && gate.check(), "remote_closed");
                        connection->enqueue(
                            {static_cast<Kind>(kind), Bytes(buffer.begin(), buffer.begin() + size)});
                    } else
                        std::this_thread::sleep_for(std::chrono::milliseconds(2));
                }
                ng_remote_gate_revoke(gate.value);
            } catch (const std::exception &e) {
                h.status(e.what());
            }
            connection.reset();
            if (media)
                h.o.close(h.o.context);
            monitor.session.bind({});
            if (!id.empty())
                try {
                    h.call("DELETE", std::string(base) + "/sessions/" + id);
                } catch (...) {
                }
            if (monitor.live())
                h.status(h.o.unattended ? "remote_host_unattended" : "remote_host_waiting");
        }
    } catch (...) {
        unregister();
        throw;
    }
    unregister();
    h.status("remote_host_stopped");
}
} // namespace
extern "C" int32_t ng_remote_host_create(const ng_remote_host_options *o, ng_remote_host **out) {
    if (!out)
        return -1;
    *out = nullptr;
    if (!o || o->struct_size != sizeof(*o) || o->abi_version != NG_REMOTE_HOST_ABI || !o->site_id ||
        !o->name || !o->platform || !o->active || !o->control || !o->open || !o->poll || !o->close ||
        !o->consent || o->unattended > 1 || !(o->capabilities & NG_REMOTE_VIEW) ||
        (o->capabilities & ~uint32_t(NG_REMOTE_ALL_CAPABILITIES)) ||
        ((o->capabilities & NG_REMOTE_INPUT) && !o->input) ||
        ((o->capabilities & NG_REMOTE_CLIPBOARD) && !o->clipboard) ||
        ((o->capabilities & (NG_REMOTE_UPLOAD | NG_REMOTE_DOWNLOAD)) &&
         (!o->exchange_directory || !*o->exchange_directory)))
        return -1;
    try {
        auto h = std::make_unique<ng_remote_host>();
        h->o = *o;
        h->site = o->site_id;
        h->name = o->name;
        h->platform = o->platform;
        if (o->exchange_directory)
            h->directory = o->exchange_directory;
        if (h->site.empty() || h->site.size() > 63 || h->name.empty() || h->name.size() > 128 ||
            (h->platform != "windows" && h->platform != "macos" && h->platform != "linux"))
            return -1;
        *out = h.release();
        return 0;
    } catch (...) {
        return -2;
    }
}
extern "C" int32_t ng_remote_host_run(ng_remote_host *h) {
    if (!h)
        return -1;
    if (h->started.exchange(true))
        return -3;
    try {
        run(*h);
        return 0;
    } catch (const std::exception &e) {
        h->status(e.what());
        return h->active() ? -2 : 0;
    } catch (...) {
        return -2;
    }
}
extern "C" void ng_remote_host_cancel(ng_remote_host *h) {
    if (h)
        h->cancelled = true;
}
extern "C" void ng_remote_host_destroy(ng_remote_host *h) { delete h; }
