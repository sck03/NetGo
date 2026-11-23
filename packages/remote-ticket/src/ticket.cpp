#include "netgo_remote_ticket.h"
#include <openssl/evp.h>
#include <algorithm>
#include <cstring>
#include <memory>
#include <new>
#include <array>

namespace {
using Key = std::unique_ptr<EVP_PKEY, decltype(&EVP_PKEY_free)>;
using Digest = std::unique_ptr<EVP_MD_CTX, decltype(&EVP_MD_CTX_free)>;
constexpr uint8_t magic[8] = {'N', 'G', 'R', 'M', 'T', 'K', '0', '1'};
bool nonzero(const uint8_t* data, size_t size) noexcept {
    return std::any_of(data, data + size, [](uint8_t value) { return value != 0; });
}
uint64_t read64(const uint8_t* data) noexcept {
    uint64_t value = 0;
    for (unsigned i = 0; i < 8; ++i) value = (value << 8) | data[i];
    return value;
}
uint32_t read32(const uint8_t* data) noexcept {
    uint32_t value = 0;
    for (unsigned i = 0; i < 4; ++i) value = (value << 8) | data[i];
    return value;
}
bool valid_id(ng_remote_ticket_id id) noexcept {
    if (!id.data || id.size == 0 || id.size > 128) return false;
    for (size_t i = 0; i < id.size; ++i) {
        const auto c = id.data[i];
        if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
              (c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-')) return false;
    }
    return true;
}
bool valid(const ng_remote_claims& c) noexcept {
    return nonzero(c.binding_digest, 32) && nonzero(c.session_id, 16) &&
        c.generation && c.policy_revision && c.sequence && c.issued_unix_ms &&
        c.expires_unix_ms > c.issued_unix_ms && c.expires_unix_ms - c.issued_unix_ms <= NG_REMOTE_MAX_LEASE_MS &&
        (c.capabilities & NG_REMOTE_VIEW) && !(c.capabilities & ~NG_REMOTE_ALL_CAPABILITIES) && c.consent_required <= 1;
}
} // namespace

struct ng_remote_ticket_verifier {
    const Key key;
    explicit ng_remote_ticket_verifier(Key value) noexcept : key(std::move(value)) {}
};

extern "C" int32_t ng_remote_ticket_verifier_create(const uint8_t* key, size_t size,
                                                     ng_remote_ticket_verifier** output) {
    if (!output) return NG_REMOTE_INVALID_ARGUMENT;
    *output = nullptr;
    if (!key || size != 32 || !nonzero(key, size)) return NG_REMOTE_INVALID_ARGUMENT;
    try {
        Key public_key(EVP_PKEY_new_raw_public_key(EVP_PKEY_ED25519, nullptr, key, size), EVP_PKEY_free);
        if (!public_key) return NG_REMOTE_INTERNAL_ERROR;
        *output = new ng_remote_ticket_verifier(std::move(public_key));
        return NG_REMOTE_OK;
    } catch (...) { return NG_REMOTE_INTERNAL_ERROR; }
}

extern "C" void ng_remote_ticket_verifier_destroy(ng_remote_ticket_verifier* verifier) { delete verifier; }

extern "C" int32_t ng_remote_ticket_verify(void* context, const uint8_t* wire, size_t size,
                                            ng_remote_claims* claims) {
    if (!claims) return NG_REMOTE_INVALID_ARGUMENT;
    if (claims->struct_size != sizeof(*claims) || claims->abi_version != NG_REMOTE_ABI) return NG_REMOTE_ABI_MISMATCH;
    *claims = {};
    claims->struct_size = sizeof(*claims);
    claims->abi_version = NG_REMOTE_ABI;
    if (!context || !wire) return NG_REMOTE_INVALID_ARGUMENT;
    if (size != NG_REMOTE_TICKET_SIZE || std::memcmp(wire, magic, sizeof(magic)) != 0) return NG_REMOTE_INVALID_TICKET;
    try {
        const auto& verifier = *static_cast<ng_remote_ticket_verifier*>(context);
        Digest digest(EVP_MD_CTX_new(), EVP_MD_CTX_free);
        if (!digest || EVP_DigestVerifyInit(digest.get(), nullptr, nullptr, nullptr, verifier.key.get()) != 1)
            return NG_REMOTE_INTERNAL_ERROR;
        if (EVP_DigestVerify(digest.get(), wire + NG_REMOTE_TICKET_MESSAGE_SIZE, 64, wire, NG_REMOTE_TICKET_MESSAGE_SIZE) != 1)
            return NG_REMOTE_INVALID_TICKET;
        ng_remote_claims parsed{};
        parsed.struct_size = sizeof(parsed);
        parsed.abi_version = NG_REMOTE_ABI;
        std::memcpy(parsed.binding_digest, wire + 8, 32);
        std::memcpy(parsed.session_id, wire + 40, 16);
        parsed.generation = read64(wire + 56);
        parsed.policy_revision = read64(wire + 64);
        parsed.sequence = read64(wire + 72);
        parsed.issued_unix_ms = read64(wire + 80);
        parsed.expires_unix_ms = read64(wire + 88);
        parsed.capabilities = read32(wire + 96);
        parsed.consent_required = read32(wire + 100);
        if (!valid(parsed)) return NG_REMOTE_INVALID_TICKET;
        *claims = parsed;
        return NG_REMOTE_OK;
    } catch (...) { return NG_REMOTE_INTERNAL_ERROR; }
}

extern "C" int32_t ng_remote_ticket_binding_digest(const ng_remote_ticket_binding* b, uint8_t output[32]) {
    if (!output) return NG_REMOTE_INVALID_ARGUMENT;
    std::memset(output, 0, 32);
    if (!b) return NG_REMOTE_INVALID_ARGUMENT;
    if (b->struct_size != sizeof(*b) || b->abi_version != NG_REMOTE_ABI) return NG_REMOTE_ABI_MISMATCH;
    if (!valid_id(b->site_id) || !valid_id(b->user_id) || !valid_id(b->controller_id) || !valid_id(b->host_id) ||
        !nonzero(b->site_key, 32) || !nonzero(b->controller_key, 32) || !nonzero(b->host_key, 32) || !nonzero(b->host_boot, 16))
        return NG_REMOTE_INVALID_ARGUMENT;
    try {
        Digest digest(EVP_MD_CTX_new(), EVP_MD_CTX_free);
        constexpr char domain[] = "NetGo remote binding v1"; // includes the NUL
        if (!digest || EVP_DigestInit_ex(digest.get(), EVP_sha256(), nullptr) != 1 ||
            EVP_DigestUpdate(digest.get(), domain, sizeof(domain)) != 1) return NG_REMOTE_INTERNAL_ERROR;
        const ng_remote_ticket_id fields[] = {b->site_id, {b->site_key, 32}, b->user_id, b->controller_id,
            {b->controller_key, 32}, b->host_id, {b->host_key, 32}, {b->host_boot, 16}};
        for (const auto& field : fields) {
            const auto size = static_cast<uint32_t>(field.size);
            const uint8_t prefix[] = {static_cast<uint8_t>(size >> 24), static_cast<uint8_t>(size >> 16),
                static_cast<uint8_t>(size >> 8), static_cast<uint8_t>(size)};
            if (EVP_DigestUpdate(digest.get(), prefix, sizeof(prefix)) != 1 ||
                EVP_DigestUpdate(digest.get(), field.data, field.size) != 1) return NG_REMOTE_INTERNAL_ERROR;
        }
        uint8_t result[32]{};
        unsigned size = 0;
        if (EVP_DigestFinal_ex(digest.get(), result, &size) != 1 || size != 32) return NG_REMOTE_INTERNAL_ERROR;
        std::memcpy(output, result, 32);
        return NG_REMOTE_OK;
    } catch (...) { return NG_REMOTE_INTERNAL_ERROR; }
}

extern "C" int32_t ng_remote_possession_verify(const uint8_t key[32], const uint8_t* wire, size_t size,
                                                 const uint8_t challenge[32], const uint8_t channel[32],
                                                 const uint8_t signature[64]) {
    if (!key || !wire || !challenge || !channel || !signature || size != NG_REMOTE_TICKET_SIZE ||
        !nonzero(key,32) || !nonzero(challenge,32) || !nonzero(channel,32)) return NG_REMOTE_INVALID_ARGUMENT;
    try {
        constexpr char domain[] = "NetGo remote possession v1";
        std::array<uint8_t,sizeof(domain)+96> message{};
        std::memcpy(message.data(),domain,sizeof(domain));
        unsigned length=0;
        if (EVP_Digest(wire,size,message.data()+sizeof(domain),&length,EVP_sha256(),nullptr)!=1 || length!=32)
            return NG_REMOTE_INTERNAL_ERROR;
        std::memcpy(message.data()+sizeof(domain)+32,challenge,32);
        std::memcpy(message.data()+sizeof(domain)+64,channel,32);
        Key public_key(EVP_PKEY_new_raw_public_key(EVP_PKEY_ED25519,nullptr,key,32),EVP_PKEY_free);
        Digest digest(EVP_MD_CTX_new(),EVP_MD_CTX_free);
        if (!public_key || !digest || EVP_DigestVerifyInit(digest.get(),nullptr,nullptr,nullptr,public_key.get())!=1)
            return NG_REMOTE_INTERNAL_ERROR;
        return EVP_DigestVerify(digest.get(),signature,64,message.data(),message.size())==1 ? NG_REMOTE_OK : NG_REMOTE_DENIED;
    } catch (...) { return NG_REMOTE_INTERNAL_ERROR; }
}
