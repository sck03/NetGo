#include "netgo_remote_ticket.h"
#include <openssl/crypto.h>
#include <algorithm>
#include <array>
#include <atomic>
#include <cstring>
#include <fstream>
#include <iostream>
#include <map>
#include <memory>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

namespace {
using Bytes = std::vector<uint8_t>;
using Vectors = std::map<std::string, Bytes>;
using Verifier = std::unique_ptr<ng_remote_ticket_verifier, decltype(&ng_remote_ticket_verifier_destroy)>;
using Gate = std::unique_ptr<ng_remote_gate, decltype(&ng_remote_gate_destroy)>;
unsigned checks = 0;
void require(bool condition, const std::string& message) {
    ++checks;
    if (!condition) throw std::runtime_error(message);
}
Vectors load(const char* path) {
    std::ifstream file(path);
    if (!file) throw std::runtime_error("cannot open shared Go/C++ vectors");
    Vectors out;
    std::string name, hex;
    while (file >> name >> hex) {
        if (hex.size() % 2) throw std::runtime_error("odd hex vector length");
        Bytes bytes;
        for (size_t i = 0; i < hex.size(); i += 2) {
            const auto digit = [](char c) -> uint8_t {
                if (c >= '0' && c <= '9') return static_cast<uint8_t>(c - '0');
                if (c >= 'a' && c <= 'f') return static_cast<uint8_t>(c - 'a' + 10);
                throw std::runtime_error("noncanonical hex vector");
            };
            bytes.push_back(static_cast<uint8_t>((digit(hex[i]) << 4) | digit(hex[i + 1])));
        }
        if (!out.emplace(name, std::move(bytes)).second) throw std::runtime_error("duplicate vector");
    }
    return out;
}
Verifier create(const Bytes& key) {
    ng_remote_ticket_verifier* raw = nullptr;
    require(ng_remote_ticket_verifier_create(key.data(), key.size(), &raw) == NG_REMOTE_OK && raw,
            "create pinned-key verifier");
    return Verifier(raw, ng_remote_ticket_verifier_destroy);
}
ng_remote_claims dirty_claims() {
    ng_remote_claims out;
    std::memset(&out, 0xa5, sizeof(out));
    out.struct_size = sizeof(out);
    out.abi_version = NG_REMOTE_ABI;
    return out;
}
bool cleared(const ng_remote_claims& c) {
    const auto zero = [](uint8_t b) { return b == 0; };
    return c.struct_size == sizeof(c) && c.abi_version == NG_REMOTE_ABI &&
        std::all_of(std::begin(c.binding_digest), std::end(c.binding_digest), zero) &&
        std::all_of(std::begin(c.session_id), std::end(c.session_id), zero) &&
        !c.generation && !c.policy_revision && !c.sequence && !c.issued_unix_ms &&
        !c.expires_unix_ms && !c.capabilities && !c.consent_required;
}
void signature_tests(const Vectors& v, ng_remote_ticket_verifier* verifier) {
    for (const auto& name : {"begin", "renew", "expand", "foreign", "expired"}) {
        auto claims = dirty_claims();
        const auto& wire = v.at(name);
        require(ng_remote_ticket_verify(verifier, wire.data(), wire.size(), &claims) == NG_REMOTE_OK,
                std::string("authentic vector: ") + name);
    }
    auto claims = dirty_claims();
    const auto& begin = v.at("begin");
    require(ng_remote_ticket_verify(verifier, begin.data(), begin.size(), &claims) == NG_REMOTE_OK &&
        claims.generation == 4 && claims.policy_revision == 2 && claims.sequence == 1 &&
        claims.issued_unix_ms == 1700000000000ULL && claims.expires_unix_ms == 1700000020000ULL &&
        claims.capabilities == (NG_REMOTE_VIEW | NG_REMOTE_INPUT) && claims.consent_required == 1 &&
        std::equal(v.at("binding").begin(), v.at("binding").end(), claims.binding_digest), "Go claims decoded exactly");
    for (size_t i = 0; i < sizeof(claims.session_id); ++i)
        require(claims.session_id[i] == i + 113, "session bytes preserved");
    const auto reject = [&](const uint8_t* wire, size_t size, const std::string& name) {
        auto out = dirty_claims();
        require(ng_remote_ticket_verify(verifier, wire, size, &out) == NG_REMOTE_INVALID_TICKET && cleared(out), name);
    };
    for (const auto& entry : v) {
        if (entry.first.compare(0, 8, "invalid_") == 0)
            reject(entry.second.data(), entry.second.size(), "valid signature with malformed claims: " + entry.first);
    }
    for (size_t i = 0; i < begin.size(); ++i) {
        auto changed = begin;
        changed[i] ^= 1;
        reject(changed.data(), changed.size(), "altered byte " + std::to_string(i));
        reject(begin.data(), i, "truncated byte " + std::to_string(i));
    }
    auto trailing = begin;
    trailing.push_back(0);
    reject(trailing.data(), trailing.size(), "trailing bytes");
    auto wrong = create(Bytes(32, 2));
    claims = dirty_claims();
    require(ng_remote_ticket_verify(wrong.get(), begin.data(), begin.size(), &claims) == NG_REMOTE_INVALID_TICKET &&
            cleared(claims), "different site key rejected");
    claims = dirty_claims();
    require(ng_remote_ticket_verify(nullptr, begin.data(), begin.size(), &claims) == NG_REMOTE_INVALID_ARGUMENT &&
            cleared(claims), "missing verifier clears claims");
    claims = dirty_claims();
    require(ng_remote_ticket_verify(verifier, nullptr, begin.size(), &claims) == NG_REMOTE_INVALID_ARGUMENT &&
            cleared(claims), "missing wire clears claims");
    require(ng_remote_ticket_verify(verifier, begin.data(), begin.size(), nullptr) == NG_REMOTE_INVALID_ARGUMENT,
            "null output rejected");
    claims = dirty_claims();
    claims.abi_version++;
    require(ng_remote_ticket_verify(verifier, begin.data(), begin.size(), &claims) == NG_REMOTE_ABI_MISMATCH &&
            claims.generation == 0xa5a5a5a5a5a5a5a5ULL, "unknown ABI output not overwritten");
    for (const auto& key : {Bytes{}, Bytes(31, 1), Bytes(33, 1), Bytes(32, 0)}) {
        ng_remote_ticket_verifier* bad = verifier;
        require(ng_remote_ticket_verifier_create(key.data(), key.size(), &bad) == NG_REMOTE_INVALID_ARGUMENT && !bad,
                "invalid public key rejected");
    }
    std::atomic<bool> concurrent_ok{true};
    std::vector<std::thread> workers;
    for (unsigned i = 0; i < 4; ++i) workers.emplace_back([&] {
        for (unsigned j = 0; j < 64; ++j) {
            auto out = dirty_claims();
            if (ng_remote_ticket_verify(verifier, begin.data(), begin.size(), &out) != NG_REMOTE_OK || out.sequence != 1)
                concurrent_ok = false;
        }
    });
    for (auto& worker : workers) worker.join();
    require(concurrent_ok, "immutable verifier supports concurrent calls");
}

ng_remote_ticket_id id(const char* value) {
    return {reinterpret_cast<const uint8_t*>(value), std::strlen(value)};
}
void binding_tests(const Vectors& v) {
    ng_remote_ticket_binding b{};
    b.struct_size = sizeof(b);
    b.abi_version = NG_REMOTE_ABI;
    b.site_id = id("test-site"); b.user_id = id("test-user");
    b.controller_id = id("test-controller"); b.host_id = id("test-host");
    std::copy(v.at("public_key").begin(), v.at("public_key").end(), b.site_key);
    for (size_t i = 0; i < 32; ++i) {
        b.controller_key[i] = static_cast<uint8_t>(i + 33);
        b.host_key[i] = static_cast<uint8_t>(i + 65);
    }
    for (size_t i = 0; i < 16; ++i) b.host_boot[i] = static_cast<uint8_t>(i + 97);
    std::array<uint8_t, 32> digest{};
    require(ng_remote_ticket_binding_digest(&b, digest.data()) == NG_REMOTE_OK &&
            std::equal(digest.begin(), digest.end(), v.at("binding").begin()), "Go/C++ binding digest matches");
    const auto expected = digest;
    for (unsigned i = 0; i < 8; ++i) {
        auto changed = b;
        switch (i) {
        case 0: changed.site_id = id("other-site"); break;
        case 1: changed.user_id = id("other-user"); break;
        case 2: changed.controller_id = id("other-controller"); break;
        case 3: changed.host_id = id("other-host"); break;
        case 4: changed.site_key[0] ^= 1; break;
        case 5: changed.controller_key[0] ^= 1; break;
        case 6: changed.host_key[0] ^= 1; break;
        default: changed.host_boot[0] ^= 1; break;
        }
        require(ng_remote_ticket_binding_digest(&changed, digest.data()) == NG_REMOTE_OK && digest != expected,
                "every authenticated identity fences grants");
    }
    for (const auto& invalid : {std::string(), std::string("with space"), std::string("x\0y", 3),
                               std::string("\xc3\xa9"), std::string(129, 'x')}) {
        auto changed = b;
        changed.site_id = {reinterpret_cast<const uint8_t*>(invalid.data()), invalid.size()};
        digest.fill(0xa5);
        require(ng_remote_ticket_binding_digest(&changed, digest.data()) == NG_REMOTE_INVALID_ARGUMENT &&
                digest == std::array<uint8_t, 32>{}, "invalid canonical ID clears binding output");
    }
    b.user_id = id("ab"); b.controller_id = id("c");
    std::array<uint8_t, 32> one{};
    require(ng_remote_ticket_binding_digest(&b, one.data()) == NG_REMOTE_OK, "first field boundary");
    b.user_id = id("a"); b.controller_id = id("bc");
    require(ng_remote_ticket_binding_digest(&b, digest.data()) == NG_REMOTE_OK && digest != one,
            "field boundaries remain unambiguous");
}
struct Clocks { uint64_t wall = 1700000000000ULL, mono = 100; };
uint64_t wall(void* context) { return static_cast<Clocks*>(context)->wall; }
uint64_t mono(void* context) { return static_cast<Clocks*>(context)->mono; }
void gate_tests(const Vectors& v, ng_remote_ticket_verifier* verifier) {
    Clocks clocks;
    ng_remote_options options{};
    options.struct_size = sizeof(options);
    options.abi_version = NG_REMOTE_ABI;
    std::copy(v.at("binding").begin(), v.at("binding").end(), options.binding_digest);
    options.allowed_capabilities = NG_REMOTE_ALL_CAPABILITIES;
    options.verify = ng_remote_ticket_verify;
    options.verify_context = verifier;
    options.unix_ms = wall;
    options.monotonic_ms = mono;
    options.clock_context = &clocks;
    const auto make_gate = [&] {
        ng_remote_gate* raw = nullptr;
        require(ng_remote_gate_create(&options, &raw) == NG_REMOTE_OK && raw, "gate with real verifier");
        return Gate(raw, ng_remote_gate_destroy);
    };
    const auto begin = [&](ng_remote_gate* g, const char* name) {
        const auto& wire = v.at(name);
        return ng_remote_gate_begin(g, wire.data(), wire.size());
    };
    const auto renew = [&](ng_remote_gate* g, const char* name) {
        const auto& wire = v.at(name);
        return ng_remote_gate_renew(g, wire.data(), wire.size());
    };
    auto gate = make_gate();
    require(begin(gate.get(), "foreign") == NG_REMOTE_INVALID_TICKET, "other binding denied before consent");
    require(begin(gate.get(), "expired") == NG_REMOTE_EXPIRED, "authentic expired ticket rejected");
    require(begin(gate.get(), "begin") == NG_REMOTE_CONSENT_REQUIRED, "authentic ticket still requires consent");
    require(ng_remote_gate_check(gate.get(), NG_REMOTE_VIEW) == NG_REMOTE_CONSENT_REQUIRED, "no frame before consent");
    require(ng_remote_gate_consent(gate.get(), 1) == NG_REMOTE_OK, "local consent enables gate");
    require(ng_remote_gate_check(gate.get(), NG_REMOTE_VIEW | NG_REMOTE_INPUT) == NG_REMOTE_OK,
            "approved view/input available");
    require(ng_remote_gate_check(gate.get(), NG_REMOTE_AUDIO) == NG_REMOTE_DENIED, "unsigned capability unavailable");
    clocks.wall += 1000; clocks.mono += 1000;
    require(renew(gate.get(), "renew") == NG_REMOTE_OK, "authentic renewal narrows capabilities");
    require(ng_remote_gate_check(gate.get(), NG_REMOTE_INPUT) == NG_REMOTE_DENIED, "input removed immediately");
    require(renew(gate.get(), "renew") == NG_REMOTE_REPLAY, "same renewal cannot replay");
    require(renew(gate.get(), "expand") == NG_REMOTE_DENIED, "even signed grant cannot expand an active session");
    require(renew(gate.get(), "foreign") == NG_REMOTE_INVALID_TICKET, "foreign grant cannot renew");
    ng_remote_snapshot state{};
    state.struct_size = sizeof(state); state.abi_version = NG_REMOTE_ABI;
    require(ng_remote_gate_poll(gate.get(), &state) == NG_REMOTE_OK && state.state == NG_REMOTE_ACTIVE &&
            state.sequence == 2 && state.capabilities == NG_REMOTE_VIEW && state.remaining_ms == 20000,
            "rejected grants leave the narrowed lease unchanged");
    clocks.wall -= 5000; clocks.mono += 20000;
    require(ng_remote_gate_check(gate.get(), NG_REMOTE_VIEW) == NG_REMOTE_EXPIRED, "wall rollback cannot extend lease");
    require(renew(gate.get(), "expand") == NG_REMOTE_EXPIRED, "expired gate cannot resurrect");
    clocks = {};
    auto revoked = make_gate();
    require(begin(revoked.get(), "begin") == NG_REMOTE_CONSENT_REQUIRED, "second gate awaits consent");
    require(ng_remote_gate_consent(revoked.get(), 1) == NG_REMOTE_OK, "second gate consent");
    require(ng_remote_gate_revoke(revoked.get()) == NG_REMOTE_OK, "revoke active gate");
    require(ng_remote_gate_check(revoked.get(), NG_REMOTE_VIEW) == NG_REMOTE_CLOSED &&
            renew(revoked.get(), "renew") == NG_REMOTE_CLOSED, "revocation is terminal");
    // These two gate instances deliberately demonstrate the adapter's boundary:
    // cross-instance consumption and proof of possession need a host coordinator.
}
} // namespace

int main(int argc, char** argv) {
    try {
        if (argc != 3) throw std::runtime_error("usage: remote-ticket-tests <shared-vectors> <expected-openssl-version>");
        require(OpenSSL_version_num() == OPENSSL_VERSION_NUMBER &&
                std::string(OpenSSL_version(OPENSSL_VERSION_STRING)) == argv[2], "compiled and linked crypto match the selected version");
        std::cout << "Linked " << OpenSSL_version(OPENSSL_VERSION) << '\n';
        const auto vectors = load(argv[1]);
        require(vectors.at("public_key").size() == 32 && vectors.at("binding").size() == 32 &&
                vectors.at("begin").size() == NG_REMOTE_TICKET_SIZE, "shared vector dimensions");
        auto verifier = create(vectors.at("public_key"));
        signature_tests(vectors, verifier.get());
        binding_tests(vectors);
        gate_tests(vectors, verifier.get());
        std::cout << "PASS " << checks << " assertions: Go/C++ signed tickets, C ABI adapter, binding and gate integration\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "FAIL after " << checks << " assertions: " << error.what() << '\n';
        return 1;
    }
}
