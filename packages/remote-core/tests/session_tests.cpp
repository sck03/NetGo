#include "netgo_remote.h"
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstring>
#include <iostream>
#include <limits>
#include <mutex>
#include <stdexcept>
#include <thread>

namespace {
unsigned checks = 0;
void require(bool condition, const char* message) {
    ++checks;
    if (!condition) throw std::runtime_error(message);
}
struct Fixture {
    std::atomic<uint64_t> wall{100000}, mono{1000};
    std::atomic<bool> reject{false};
    std::mutex mutex;
    std::condition_variable ready;
    bool block = false, entered = false, release = false;
    ng_remote_gate* gate = nullptr;
    ng_remote_options options{};

    // This is a deterministic authenticated-claims test seam, NOT a signature
    // implementation or a product ticket decoder. No raw struct goes on the wire.
    static int32_t verify(void* context, const uint8_t* data, size_t size, ng_remote_claims* out) {
        auto& self = *static_cast<Fixture*>(context);
        if (self.reject || size != sizeof(*out)) return NG_REMOTE_INVALID_TICKET;
        {
            std::unique_lock<std::mutex> lock(self.mutex);
            if (self.block) {
                self.entered = true;
                self.ready.notify_all();
                if (!self.ready.wait_for(lock, std::chrono::seconds(3), [&] { return self.release; }))
                    return NG_REMOTE_INVALID_TICKET;
            }
        }
        std::memcpy(out, data, size);
        return NG_REMOTE_OK;
    }
    static uint64_t unix_clock(void* context) { return static_cast<Fixture*>(context)->wall; }
    static uint64_t mono_clock(void* context) { return static_cast<Fixture*>(context)->mono; }
    explicit Fixture(uint32_t mode = NG_REMOTE_LOCAL_CONSENT, uint32_t caps = NG_REMOTE_ALL_CAPABILITIES) {
        options.struct_size = sizeof(options);
        options.abi_version = NG_REMOTE_ABI;
        std::memset(options.binding_digest, 7, sizeof(options.binding_digest));
        options.allowed_capabilities = caps;
        options.consent_mode = mode;
        options.verify = verify;
        options.verify_context = this;
        options.unix_ms = unix_clock;
        options.monotonic_ms = mono_clock;
        options.clock_context = this;
        require(ng_remote_gate_create(&options, &gate) == NG_REMOTE_OK && gate, "create gate");
    }
    ~Fixture() { ng_remote_gate_destroy(gate); }
    ng_remote_claims ticket(uint32_t caps = NG_REMOTE_VIEW | NG_REMOTE_INPUT, uint64_t sequence = 1) {
        ng_remote_claims result{};
        result.struct_size = sizeof(result);
        result.abi_version = NG_REMOTE_ABI;
        std::memcpy(result.binding_digest, options.binding_digest, 32);
        result.session_id[0] = 9;
        result.generation = 4;
        result.policy_revision = 2;
        result.sequence = sequence;
        result.issued_unix_ms = wall;
        result.expires_unix_ms = wall + 20000;
        result.capabilities = caps;
        return result;
    }
    int32_t begin(const ng_remote_claims& value) {
        return ng_remote_gate_begin(gate, reinterpret_cast<const uint8_t*>(&value), sizeof(value));
    }
    int32_t renew(const ng_remote_claims& value) {
        return ng_remote_gate_renew(gate, reinterpret_cast<const uint8_t*>(&value), sizeof(value));
    }
    ng_remote_snapshot snapshot() {
        ng_remote_snapshot result{};
        result.struct_size = sizeof(result);
        result.abi_version = NG_REMOTE_ABI;
        ng_remote_gate_poll(gate, &result);
        return result;
    }
};

void consent_and_capabilities() {
    Fixture f;
    require(ng_remote_gate_check(f.gate, NG_REMOTE_VIEW) == NG_REMOTE_DENIED, "new gate denied");
    require(f.begin(f.ticket()) == NG_REMOTE_CONSENT_REQUIRED, "local consent default");
    require(f.snapshot().capabilities == 0, "pending snapshot cannot grant capabilities");
    require(ng_remote_gate_check(f.gate, NG_REMOTE_INPUT) == NG_REMOTE_CONSENT_REQUIRED, "input before consent denied");
    require(f.renew(f.ticket(NG_REMOTE_VIEW, 2)) == NG_REMOTE_CONSENT_REQUIRED, "pending renewal denied");
    require(ng_remote_gate_consent(f.gate, 1) == NG_REMOTE_OK, "local approval");
    require(ng_remote_gate_check(f.gate, NG_REMOTE_VIEW | NG_REMOTE_INPUT) == NG_REMOTE_OK, "approved intersection");
    for (uint32_t capability : {NG_REMOTE_AUDIO, NG_REMOTE_CLIPBOARD, NG_REMOTE_UPLOAD, NG_REMOTE_DOWNLOAD})
        require(ng_remote_gate_check(f.gate, capability) == NG_REMOTE_DENIED, "independent capability denied");
    require(ng_remote_gate_check(f.gate, NG_REMOTE_VIEW | NG_REMOTE_AUDIO) == NG_REMOTE_DENIED, "all requested bits required");
    require(ng_remote_gate_check(f.gate, 0) == NG_REMOTE_INVALID_ARGUMENT, "empty action denied");
    require(ng_remote_gate_check(f.gate, 1u << 31) == NG_REMOTE_INVALID_ARGUMENT, "unknown action denied");
    require(f.begin(f.ticket()) == NG_REMOTE_REPLAY, "second begin rejected");
    Fixture view(NG_REMOTE_ALLOW_UNATTENDED, NG_REMOTE_VIEW);
    require(view.begin(view.ticket()) == NG_REMOTE_OK, "explicit unattended option");
    require(view.snapshot().capabilities == NG_REMOTE_VIEW, "host capabilities restrict signed grant");
    require(ng_remote_gate_check(view.gate, NG_REMOTE_INPUT) == NG_REMOTE_DENIED, "host view-only restriction");
    Fixture signed_consent(NG_REMOTE_ALLOW_UNATTENDED);
    auto ticket = signed_consent.ticket(); ticket.consent_required = 1;
    require(signed_consent.begin(ticket) == NG_REMOTE_CONSENT_REQUIRED, "signed consent overrides unattended");
    require(ng_remote_gate_consent(signed_consent.gate, 0) == NG_REMOTE_DENIED, "local decline");
    require(signed_consent.snapshot().state == NG_REMOTE_TERMINATED, "decline terminal");
    require(ng_remote_gate_consent(signed_consent.gate, 1) == NG_REMOTE_DENIED, "late consent cannot revive");
}

void expiry_and_clocks() {
    Fixture f(NG_REMOTE_ALLOW_UNATTENDED);
    require(f.begin(f.ticket()) == NG_REMOTE_OK, "start timed session");
    f.wall = 1; f.mono = 20999;
    require(ng_remote_gate_check(f.gate, NG_REMOTE_VIEW) == NG_REMOTE_OK, "wall rollback cannot advance monotonic deadline");
    f.mono = 21000;
    require(ng_remote_gate_check(f.gate, NG_REMOTE_VIEW) == NG_REMOTE_EXPIRED, "deadline inclusive");
    require(f.snapshot().capabilities == 0 && f.snapshot().remaining_ms == 0, "expiration clears permissions");
    require(f.renew(f.ticket(NG_REMOTE_VIEW, 2)) == NG_REMOTE_EXPIRED, "renewal cannot revive");
    require(f.begin(f.ticket()) == NG_REMOTE_EXPIRED, "begin cannot revive expired gate");
    Fixture pending;
    require(pending.begin(pending.ticket()) == NG_REMOTE_CONSENT_REQUIRED, "pending start");
    pending.mono = 21000;
    require(ng_remote_gate_consent(pending.gate, 1) == NG_REMOTE_EXPIRED, "late local approval denied");
    Fixture backward(NG_REMOTE_ALLOW_UNATTENDED);
    require(backward.begin(backward.ticket()) == NG_REMOTE_OK, "start rollback test");
    backward.mono = 999;
    require(ng_remote_gate_check(backward.gate, NG_REMOTE_VIEW) == NG_REMOTE_CLOCK_ERROR, "monotonic rollback fails closed");
    backward.mono = 1001;
    require(ng_remote_gate_check(backward.gate, NG_REMOTE_VIEW) == NG_REMOTE_CLOCK_ERROR, "clock recovery cannot revive");
    Fixture suspend(NG_REMOTE_ALLOW_UNATTENDED);
    require(suspend.begin(suspend.ticket()) == NG_REMOTE_OK, "start suspend test");
    suspend.wall += 20000;
    require(ng_remote_gate_check(suspend.gate, NG_REMOTE_VIEW) == NG_REMOTE_EXPIRED, "suspend expires even if platform monotonic clock pauses");
}

void reject_bad_claims() {
    Fixture f(NG_REMOTE_ALLOW_UNATTENDED);
    f.reject = true;
    require(f.begin(f.ticket()) == NG_REMOTE_INVALID_TICKET, "signature verifier rejection");
    f.reject = false;
    for (int variation = 0; variation != 11; ++variation) {
        auto ticket = f.ticket();
        switch (variation) {
        case 0: ticket.binding_digest[0] ^= 1; break;
        case 1: ticket.capabilities = NG_REMOTE_INPUT; break;
        case 2: ticket.capabilities |= 1u << 30; break;
        case 3: ticket.sequence = 0; break;
        case 4: ticket.generation = 0; break;
        case 5: ticket.policy_revision = 0; break;
        case 6: ticket.abi_version++; break;
        case 7: ticket.expires_unix_ms = ticket.issued_unix_ms + 30001; break;
        case 8: ticket.issued_unix_ms += 6000; break;
        case 9: ticket.consent_required = 2; break;
        case 10: std::memset(ticket.session_id, 0, 16); break;
        }
        require(f.begin(ticket) == NG_REMOTE_INVALID_TICKET, "malformed or wrong-bound claims rejected");
    }
    auto expired = f.ticket(); expired.issued_unix_ms -= 20000; expired.expires_unix_ms = f.wall;
    require(f.begin(expired) == NG_REMOTE_EXPIRED, "expired ticket rejected");
    uint8_t byte = 0;
    require(ng_remote_gate_begin(f.gate, &byte, NG_REMOTE_MAX_TICKET_BYTES + 1) == NG_REMOTE_INVALID_ARGUMENT, "oversized ticket refused before verifier reads");
    require(f.snapshot().state == NG_REMOTE_NEW, "invalid tickets grant nothing");
    require(f.begin(f.ticket()) == NG_REMOTE_OK, "valid ticket after invalid input");
}

void renewal_and_revoke() {
    Fixture f(NG_REMOTE_ALLOW_UNATTENDED);
    auto initial = f.ticket();
    require(f.begin(initial) == NG_REMOTE_OK, "start renewal session");
    require(f.renew(initial) == NG_REMOTE_REPLAY, "same ticket replay");
    f.wall += 10000; f.mono += 10000;
    auto next = f.ticket(NG_REMOTE_VIEW | NG_REMOTE_INPUT, 2);
    auto wrong = next; wrong.session_id[0]++;
    require(f.renew(wrong) == NG_REMOTE_DENIED, "wrong session renewal");
    wrong = next; wrong.generation++;
    require(f.renew(wrong) == NG_REMOTE_DENIED, "generation change rejected");
    wrong = next; wrong.policy_revision--;
    require(f.renew(wrong) == NG_REMOTE_REPLAY, "old policy revision rejected");
    wrong = next; wrong.capabilities |= NG_REMOTE_AUDIO;
    require(f.renew(wrong) == NG_REMOTE_DENIED, "renewal cannot widen permissions");
    require(f.renew(next) == NG_REMOTE_OK && f.snapshot().remaining_ms == 20000, "bounded renewal");
    next.sequence++; next.policy_revision++; next.capabilities = NG_REMOTE_VIEW;
    require(f.renew(next) == NG_REMOTE_OK, "permission narrowing");
    require(ng_remote_gate_check(f.gate, NG_REMOTE_INPUT) == NG_REMOTE_DENIED, "input disabled immediately");
    next.sequence++; next.capabilities |= NG_REMOTE_INPUT;
    require(f.renew(next) == NG_REMOTE_DENIED, "narrowed permission cannot be restored by renewal");
    require(ng_remote_gate_revoke(f.gate) == NG_REMOTE_OK, "revoke active session");
    require(ng_remote_gate_revoke(f.gate) == NG_REMOTE_OK, "revoke idempotent");
    require(f.snapshot().state == NG_REMOTE_TERMINATED && f.snapshot().capabilities == 0, "revocation terminal");
    require(f.renew(next) == NG_REMOTE_CLOSED, "renew cannot undo revoke");
}

void revocation_wins_race() {
    Fixture f(NG_REMOTE_ALLOW_UNATTENDED);
    require(f.begin(f.ticket()) == NG_REMOTE_OK, "start concurrent session");
    f.block = true;
    std::atomic<int32_t> result{NG_REMOTE_INTERNAL_ERROR};
    std::thread renewal([&] { result = f.renew(f.ticket(NG_REMOTE_VIEW, 2)); });
    bool entered = false;
    {
        std::unique_lock<std::mutex> lock(f.mutex);
        entered = f.ready.wait_for(lock, std::chrono::seconds(2), [&] { return f.entered; });
    }
    const auto revoked = ng_remote_gate_revoke(f.gate);
    {
        std::lock_guard<std::mutex> lock(f.mutex);
        f.release = true;
        f.ready.notify_all();
    }
    renewal.join();
    require(entered && revoked == NG_REMOTE_OK, "revocation does not wait for external verification");
    require(result == NG_REMOTE_CLOSED, "inflight verified renewal cannot reopen revoked session");
    require(ng_remote_gate_check(f.gate, NG_REMOTE_VIEW) == NG_REMOTE_CLOSED, "revoked action rejected");
}
}

int main() {
    try {
        consent_and_capabilities(); expiry_and_clocks(); reject_bad_claims();
        renewal_and_revoke(); revocation_wins_race();
        std::cout << "PASS " << checks << " remote gate checks (test verifier; no media/crypto integration claim)\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "FAIL " << error.what() << '\n';
        return 1;
    }
}
