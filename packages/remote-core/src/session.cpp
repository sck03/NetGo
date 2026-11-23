#include "netgo_remote.h"
#include <algorithm>
#include <chrono>
#include <cstring>
#include <limits>
#include <mutex>
#include <new>

namespace {
uint64_t wall_ms(void*) {
    return static_cast<uint64_t>(std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::system_clock::now().time_since_epoch()).count());
}
uint64_t steady_ms(void*) {
    return static_cast<uint64_t>(std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::steady_clock::now().time_since_epoch()).count());
}
bool nonzero(const uint8_t* bytes, size_t size) {
    return std::any_of(bytes, bytes + size, [](uint8_t value) { return value != 0; });
}
bool valid_caps(uint32_t caps) {
    return (caps & NG_REMOTE_VIEW) && !(caps & ~NG_REMOTE_ALL_CAPABILITIES);
}
template<class Action> int32_t boundary(Action action) noexcept {
    try { return action(); } catch (...) { return NG_REMOTE_INTERNAL_ERROR; }
}
}

struct ng_remote_gate {
    const ng_remote_options options;
    std::mutex mutex;
    ng_remote_claims current{};
    uint32_t state = NG_REMOTE_NEW;
    uint32_t capabilities = 0;
    int32_t reason = NG_REMOTE_OK;
    uint64_t last_mono = 0;
    uint64_t deadline = 0;
    bool consented = false;

    explicit ng_remote_gate(const ng_remote_options& value) : options(value) {
        last_mono = options.monotonic_ms(options.clock_context);
    }
    int32_t close(int32_t why) {
        if (state != NG_REMOTE_TERMINATED) {
            state = NG_REMOTE_TERMINATED;
            capabilities = 0;
            reason = why;
        }
        return reason;
    }
    int32_t tick(uint64_t now) {
        if (state == NG_REMOTE_TERMINATED) return reason;
        if (now < last_mono) return close(NG_REMOTE_CLOCK_ERROR);
        last_mono = now;
        // Either clock can end a lease. A wall rollback cannot prolong it and
        // suspend on platforms whose steady clock pauses cannot preserve it.
        if (state != NG_REMOTE_NEW && (now >= deadline ||
            options.unix_ms(options.clock_context) >= current.expires_unix_ms))
            return close(NG_REMOTE_EXPIRED);
        if (state == NG_REMOTE_AWAITING_CONSENT) return NG_REMOTE_CONSENT_REQUIRED;
        if (state == NG_REMOTE_NEW) return NG_REMOTE_DENIED;
        return NG_REMOTE_OK;
    }
    int32_t validate(const ng_remote_claims& claims, uint64_t unix_now, uint64_t mono_now,
                     uint64_t& expires) const {
        if (claims.struct_size != sizeof(claims) || claims.abi_version != NG_REMOTE_ABI ||
            !claims.generation || !claims.policy_revision || !claims.sequence ||
            !nonzero(claims.session_id, sizeof(claims.session_id)) ||
            !valid_caps(claims.capabilities) || claims.consent_required > 1 ||
            std::memcmp(claims.binding_digest, options.binding_digest, 32) != 0)
            return NG_REMOTE_INVALID_TICKET;
        if (claims.expires_unix_ms <= claims.issued_unix_ms ||
            claims.expires_unix_ms - claims.issued_unix_ms > NG_REMOTE_MAX_LEASE_MS ||
            (claims.issued_unix_ms > unix_now && claims.issued_unix_ms - unix_now > 5000))
            return NG_REMOTE_INVALID_TICKET;
        if (claims.expires_unix_ms <= unix_now) return NG_REMOTE_EXPIRED;
        const uint64_t remaining = std::min<uint64_t>(
            claims.expires_unix_ms - unix_now, NG_REMOTE_MAX_LEASE_MS);
        if (mono_now > std::numeric_limits<uint64_t>::max() - remaining)
            return NG_REMOTE_CLOCK_ERROR;
        expires = mono_now + remaining;
        return NG_REMOTE_OK;
    }
    int32_t verify(const uint8_t* ticket, size_t size, ng_remote_claims& claims) const {
        if (!ticket || !size || size > NG_REMOTE_MAX_TICKET_BYTES)
            return NG_REMOTE_INVALID_ARGUMENT;
        claims.struct_size = sizeof(claims);
        claims.abi_version = NG_REMOTE_ABI;
        // Call external code outside our lock; concurrent revoke wins at commit.
        return options.verify(options.verify_context, ticket, size, &claims) == NG_REMOTE_OK
            ? NG_REMOTE_OK : NG_REMOTE_INVALID_TICKET;
    }
};

extern "C" uint32_t ng_remote_abi_version(void) { return NG_REMOTE_ABI; }

extern "C" int32_t ng_remote_gate_create(const ng_remote_options* options, ng_remote_gate** output) {
    if (!output) return NG_REMOTE_INVALID_ARGUMENT;
    *output = nullptr;
    if (!options) return NG_REMOTE_INVALID_ARGUMENT;
    if (options->struct_size != sizeof(*options) || options->abi_version != NG_REMOTE_ABI)
        return NG_REMOTE_ABI_MISMATCH;
    if (!options->verify || !valid_caps(options->allowed_capabilities) ||
        !nonzero(options->binding_digest, sizeof(options->binding_digest)) ||
        options->consent_mode > NG_REMOTE_ALLOW_UNATTENDED ||
        (options->unix_ms == nullptr) != (options->monotonic_ms == nullptr))
        return NG_REMOTE_INVALID_ARGUMENT;
    return boundary([&]() -> int32_t {
        auto config = *options;
        if (!config.unix_ms) { config.unix_ms = wall_ms; config.monotonic_ms = steady_ms; }
        *output = new ng_remote_gate(config);
        return NG_REMOTE_OK;
    });
}

extern "C" void ng_remote_gate_destroy(ng_remote_gate* gate) { delete gate; }

extern "C" int32_t ng_remote_gate_begin(ng_remote_gate* gate, const uint8_t* ticket, size_t size) {
    if (!gate) return NG_REMOTE_INVALID_ARGUMENT;
    return boundary([&]() -> int32_t {
        ng_remote_claims claims{};
        const auto verified = gate->verify(ticket, size, claims);
        if (verified != NG_REMOTE_OK) return verified;
        std::lock_guard<std::mutex> lock(gate->mutex);
        const uint64_t now = gate->options.monotonic_ms(gate->options.clock_context);
        gate->tick(now);
        if (gate->state == NG_REMOTE_TERMINATED) return gate->reason;
        if (gate->state != NG_REMOTE_NEW) return NG_REMOTE_REPLAY;
        uint64_t deadline = 0;
        const auto valid = gate->validate(claims, gate->options.unix_ms(gate->options.clock_context), now, deadline);
        if (valid != NG_REMOTE_OK) return valid;
        gate->current = claims;
        gate->capabilities = claims.capabilities & gate->options.allowed_capabilities;
        gate->deadline = deadline;
        gate->state = gate->options.consent_mode == NG_REMOTE_LOCAL_CONSENT || claims.consent_required
            ? NG_REMOTE_AWAITING_CONSENT : NG_REMOTE_ACTIVE;
        return gate->state == NG_REMOTE_ACTIVE ? NG_REMOTE_OK : NG_REMOTE_CONSENT_REQUIRED;
    });
}

extern "C" int32_t ng_remote_gate_consent(ng_remote_gate* gate, uint32_t allow) {
    if (!gate || allow > 1) return NG_REMOTE_INVALID_ARGUMENT;
    return boundary([&]() -> int32_t {
        std::lock_guard<std::mutex> lock(gate->mutex);
        gate->tick(gate->options.monotonic_ms(gate->options.clock_context));
        if (gate->state == NG_REMOTE_TERMINATED) return gate->reason;
        if (gate->state != NG_REMOTE_AWAITING_CONSENT) return NG_REMOTE_DENIED;
        if (!allow) return gate->close(NG_REMOTE_DENIED);
        gate->consented = true;
        gate->state = NG_REMOTE_ACTIVE;
        return NG_REMOTE_OK;
    });
}

extern "C" int32_t ng_remote_gate_renew(ng_remote_gate* gate, const uint8_t* ticket, size_t size) {
    if (!gate) return NG_REMOTE_INVALID_ARGUMENT;
    return boundary([&]() -> int32_t {
        ng_remote_claims claims{};
        const auto verified = gate->verify(ticket, size, claims);
        if (verified != NG_REMOTE_OK) return verified;
        std::lock_guard<std::mutex> lock(gate->mutex);
        const uint64_t now = gate->options.monotonic_ms(gate->options.clock_context);
        const auto active = gate->tick(now);
        if (active != NG_REMOTE_OK) return active;
        uint64_t deadline = 0;
        const auto valid = gate->validate(claims, gate->options.unix_ms(gate->options.clock_context), now, deadline);
        if (valid != NG_REMOTE_OK) return valid;
        if (std::memcmp(claims.session_id, gate->current.session_id, 16) != 0 ||
            claims.generation != gate->current.generation) return NG_REMOTE_DENIED;
        if (claims.sequence <= gate->current.sequence || claims.policy_revision < gate->current.policy_revision ||
            claims.issued_unix_ms < gate->current.issued_unix_ms) return NG_REMOTE_REPLAY;
        const uint32_t caps = claims.capabilities & gate->options.allowed_capabilities;
        if (caps & ~gate->capabilities) return NG_REMOTE_DENIED;
        if (claims.consent_required && !gate->consented) return NG_REMOTE_CONSENT_REQUIRED;
        gate->current = claims;
        gate->capabilities = caps;
        gate->deadline = deadline;
        return NG_REMOTE_OK;
    });
}

extern "C" int32_t ng_remote_gate_revoke(ng_remote_gate* gate) {
    if (!gate) return NG_REMOTE_INVALID_ARGUMENT;
    return boundary([&]() -> int32_t {
        std::lock_guard<std::mutex> lock(gate->mutex);
        gate->close(NG_REMOTE_CLOSED);
        return NG_REMOTE_OK;
    });
}

extern "C" int32_t ng_remote_gate_check(ng_remote_gate* gate, uint32_t required) {
    if (!gate || !required || (required & ~NG_REMOTE_ALL_CAPABILITIES)) return NG_REMOTE_INVALID_ARGUMENT;
    return boundary([&]() -> int32_t {
        std::lock_guard<std::mutex> lock(gate->mutex);
        const auto active = gate->tick(gate->options.monotonic_ms(gate->options.clock_context));
        if (active != NG_REMOTE_OK) return active;
        return (required & gate->capabilities) == required ? NG_REMOTE_OK : NG_REMOTE_DENIED;
    });
}

extern "C" int32_t ng_remote_gate_poll(ng_remote_gate* gate, ng_remote_snapshot* snapshot) {
    if (!gate || !snapshot) return NG_REMOTE_INVALID_ARGUMENT;
    if (snapshot->struct_size != sizeof(*snapshot) || snapshot->abi_version != NG_REMOTE_ABI)
        return NG_REMOTE_ABI_MISMATCH;
    return boundary([&]() -> int32_t {
        std::lock_guard<std::mutex> lock(gate->mutex);
        const uint64_t now = gate->options.monotonic_ms(gate->options.clock_context);
        const auto result = gate->tick(now);
        *snapshot = {};
        snapshot->struct_size = sizeof(*snapshot);
        snapshot->abi_version = NG_REMOTE_ABI;
        snapshot->state = gate->state;
        snapshot->capabilities = gate->state == NG_REMOTE_ACTIVE ? gate->capabilities : 0;
        snapshot->terminal_reason = gate->reason;
        snapshot->remaining_ms = gate->state != NG_REMOTE_TERMINATED && gate->deadline > now
            ? gate->deadline - now : 0;
        snapshot->sequence = gate->current.sequence;
        snapshot->policy_revision = gate->current.policy_revision;
        return result;
    });
}
