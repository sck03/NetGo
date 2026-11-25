#include "remote_transport.hpp"
#include <deque>
#include <openssl/ssl.h>
#include <openssl/x509.h>
#include <openssl/rand.h>
#include <openssl/crypto.h>
#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#include <mstcpip.h>
#else
#include <sys/socket.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <fcntl.h>
#include <poll.h>
#include <netinet/tcp.h>
#include <time.h>
#if defined(__APPLE__)
#include <mach/mach_time.h>
#endif
#if defined(__linux__)
#include <pthread.h>
#include <signal.h>
#endif
#include <cerrno>
#endif
#include <algorithm>
#include <chrono>
#include <cstring>
#include <stdexcept>
#include <utility>

namespace netgo::remote {
std::uint64_t monotonic_time_ms() noexcept {
#ifdef _WIN32
    return GetTickCount64();
#elif defined(__APPLE__)
    static const auto scale = []() {
        mach_timebase_info_data_t value{};
        mach_timebase_info(&value);
        return value;
    }();
    if (!scale.denom || !scale.numer)
        return UINT64_MAX;
    auto ticks = mach_continuous_time(), whole = ticks / scale.denom;
    if (whole > UINT64_MAX / scale.numer)
        return UINT64_MAX;
    auto nanos = whole * scale.numer, part = (ticks % scale.denom) * scale.numer / scale.denom;
    if (part > UINT64_MAX - nanos)
        return UINT64_MAX;
    return (nanos + part) / 1000000;
#else
    timespec now{};
    if (clock_gettime(CLOCK_BOOTTIME, &now) != 0 || now.tv_sec < 0)
        return UINT64_MAX;
    return std::uint64_t(now.tv_sec) * 1000 + std::uint64_t(now.tv_nsec) / 1000000;
#endif
}
namespace {
using Clock = std::chrono::steady_clock;
struct SignalGuard {
#if defined(__linux__)
    sigset_t blocked{}, previous{};
    bool pendingBefore = false;
    SignalGuard() {
        sigemptyset(&blocked);
        sigaddset(&blocked, SIGPIPE);
        if (pthread_sigmask(SIG_BLOCK, &blocked, &previous) != 0)
            throw std::runtime_error("remote_signal_mask_failed");
        sigset_t pending{};
        sigpending(&pending);
        pendingBefore = sigismember(&pending, SIGPIPE) == 1;
    }
    ~SignalGuard() {
        // Suppress only a new SIGPIPE produced by this thread's TLS operation. Keep
        // preexisting pending signals and the caller's mask; never change a process
        // global signal handler from a reusable transport library.
        if (!pendingBefore) {
            sigset_t pending{};
            sigpending(&pending);
            if (sigismember(&pending, SIGPIPE) == 1) {
                timespec zero{};
                while (sigtimedwait(&blocked, nullptr, &zero) < 0 && errno == EINTR) {
                }
            }
        }
        pthread_sigmask(SIG_SETMASK, &previous, nullptr);
    }
#else
    SignalGuard() = default;
#endif
};
#ifdef _WIN32
using Socket = SOCKET;
constexpr Socket invalid = INVALID_SOCKET;
void close_socket(Socket s) { closesocket(s); }
bool pending() {
    auto e = WSAGetLastError();
    return e == WSAEWOULDBLOCK || e == WSAEINPROGRESS;
}
void nonblock(Socket s) {
    u_long one = 1;
    if (ioctlsocket(s, FIONBIO, &one))
        throw std::runtime_error("remote_socket_failed");
}
#else
using Socket = int;
constexpr Socket invalid = -1;
void close_socket(Socket s) { ::close(s); }
bool pending() { return errno == EWOULDBLOCK || errno == EAGAIN || errno == EINPROGRESS; }
void nonblock(Socket s) {
    auto flags = fcntl(s, F_GETFL, 0);
    if (flags < 0 || fcntl(s, F_SETFL, flags | O_NONBLOCK) < 0)
        throw std::runtime_error("remote_socket_failed");
}
#endif
struct SocketOwner {
    Socket value = invalid;
    ~SocketOwner() {
        if (value != invalid)
            close_socket(value);
    }
    SocketOwner() = default;
    explicit SocketOwner(Socket s) : value(s) {}
    SocketOwner(const SocketOwner &) = delete;
    Socket release() { return std::exchange(value, invalid); }
};
void require(bool ok, const char *error) {
    if (!ok)
        throw std::runtime_error(error);
}
void permission(const Continue &allowed) { require(allowed && allowed(), "remote_closed"); }
sockaddr_storage address(const std::string &ip, std::uint16_t port, int &length) {
    sockaddr_storage out{};
    auto *v4 = reinterpret_cast<sockaddr_in *>(&out);
    auto *v6 = reinterpret_cast<sockaddr_in6 *>(&out);
    if (inet_pton(AF_INET, ip.c_str(), &v4->sin_addr) == 1) {
        v4->sin_family = AF_INET;
        v4->sin_port = htons(port);
        length = sizeof(*v4);
        return out;
    }
    if (inet_pton(AF_INET6, ip.c_str(), &v6->sin6_addr) == 1) {
        v6->sin6_family = AF_INET6;
        v6->sin6_port = htons(port);
        length = sizeof(*v6);
        return out;
    }
    throw std::runtime_error("remote_numeric_address_required");
}
bool ready(Socket s, bool write, std::uint32_t ms, const Continue &allowed) {
    auto until = Clock::now() + std::chrono::milliseconds(ms);
    do {
        permission(allowed);
        auto left = std::chrono::duration_cast<std::chrono::milliseconds>(until - Clock::now()).count();
        auto wait = std::max<std::int64_t>(0, std::min<std::int64_t>(left, 100));
#ifdef _WIN32
        fd_set f;
        FD_ZERO(&f);
        FD_SET(s, &f);
        timeval tv{};
        tv.tv_usec = static_cast<decltype(tv.tv_usec)>(wait * 1000);
        int result = select(0, write ? nullptr : &f, write ? &f : nullptr, nullptr, &tv);
        if (result < 0 && WSAGetLastError() == WSAEINTR)
            continue;
#else
        pollfd f{s, short(write ? POLLOUT : POLLIN), 0};
        int result = poll(&f, 1, int(wait));
        if (result < 0 && errno == EINTR)
            continue;
        require(!(f.revents & POLLNVAL), "remote_socket_failed");
#endif
        require(result >= 0, "remote_socket_failed");
        if (result > 0) {
            permission(allowed);
            return true;
        }
    } while (Clock::now() < until);
    return false;
}
std::shared_ptr<SSL_CTX> context() {
    std::shared_ptr<SSL_CTX> ctx(SSL_CTX_new(TLS_method()), SSL_CTX_free);
    require(bool(ctx), "remote_tls_failed");
    require(SSL_CTX_set_min_proto_version(ctx.get(), TLS1_3_VERSION) == 1 &&
                SSL_CTX_set_max_proto_version(ctx.get(), TLS1_3_VERSION) == 1,
            "remote_tls_failed");
    SSL_CTX_set_options(ctx.get(), SSL_OP_NO_COMPRESSION | SSL_OP_NO_RENEGOTIATION);
    SSL_CTX_set_session_cache_mode(ctx.get(), SSL_SESS_CACHE_OFF);
    SSL_CTX_set_num_tickets(ctx.get(), 0);
    // Trust comes from the control-plane device-key pin, checked before any payload.
    SSL_CTX_set_verify(ctx.get(), SSL_VERIFY_NONE, nullptr);
    return ctx;
}
void identity(SSL_CTX *ctx, const Key &seed) {
    std::unique_ptr<EVP_PKEY, decltype(&EVP_PKEY_free)> key(
        EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, nullptr, seed.data(), seed.size()), EVP_PKEY_free);
    std::unique_ptr<X509, decltype(&X509_free)> cert(X509_new(), X509_free);
    require(key && cert, "remote_identity_failed");
    unsigned char serial[8];
    require(RAND_bytes(serial, sizeof(serial)) == 1, "remote_identity_failed");
    std::uint64_t number = 0;
    std::memcpy(&number, serial, sizeof(number));
    number &= INT64_MAX;
    require(X509_set_version(cert.get(), 2) == 1 &&
                ASN1_INTEGER_set_uint64(X509_get_serialNumber(cert.get()), number) == 1,
            "remote_identity_failed");
    require(X509_gmtime_adj(X509_getm_notBefore(cert.get()), -300) &&
                X509_gmtime_adj(X509_getm_notAfter(cert.get()), 86400),
            "remote_identity_failed");
    std::unique_ptr<X509_NAME, decltype(&X509_NAME_free)> name(X509_NAME_new(), X509_NAME_free);
    require(name && X509_NAME_add_entry_by_txt(name.get(), "CN", MBSTRING_ASC,
                                               reinterpret_cast<const unsigned char *>("NetGo device"), -1,
                                               -1, 0) == 1,
            "remote_identity_failed");
    require(X509_set_subject_name(cert.get(), name.get()) == 1 &&
                X509_set_issuer_name(cert.get(), name.get()) == 1 &&
                X509_set_pubkey(cert.get(), key.get()) == 1 && X509_sign(cert.get(), key.get(), nullptr) > 0,
            "remote_identity_failed");
    require(SSL_CTX_use_certificate(ctx, cert.get()) == 1 && SSL_CTX_use_PrivateKey(ctx, key.get()) == 1,
            "remote_identity_failed");
}
void put(std::uint8_t *p, std::uint32_t value) {
    p[0] = std::uint8_t(value >> 24);
    p[1] = std::uint8_t(value >> 16);
    p[2] = std::uint8_t(value >> 8);
    p[3] = std::uint8_t(value);
}
std::uint32_t get(const std::uint8_t *p) {
    return (std::uint32_t(p[0]) << 24) | (std::uint32_t(p[1]) << 16) | (std::uint32_t(p[2]) << 8) | p[3];
}
} // namespace
struct Connection::Impl {
    SocketOwner socket;
    std::shared_ptr<SSL_CTX> context;
    std::unique_ptr<SSL, decltype(&SSL_free)> tls{nullptr, SSL_free};
    Continue allowed;
    std::array<std::uint8_t, 12> header{};
    std::size_t headerBytes = 0, bodyBytes = 0;
    Message incoming{};
    bool headerReady = false, reading = false;
    Clock::time_point readDeadline;
    struct Output {
        std::vector<uint8_t> bytes;
        std::size_t offset = 0;
        Clock::time_point deadline;
    };
    std::deque<Output> output;
    std::size_t outputBytes = 0;
    Impl(SocketOwner &&s, std::shared_ptr<SSL_CTX> ctx, Continue check)
        : socket(s.release()), context(std::move(ctx)), tls(SSL_new(context.get()), SSL_free),
          allowed(std::move(check)) {
        require(bool(tls) && SSL_set_fd(tls.get(), int(socket.value)) == 1, "remote_tls_failed");
        nonblock(socket.value);
        int one = 1;
        require(setsockopt(socket.value, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char *>(&one),
                           sizeof(one)) == 0,
                "remote_socket_failed");
#ifdef SO_NOSIGPIPE
        require(setsockopt(socket.value, SOL_SOCKET, SO_NOSIGPIPE, reinterpret_cast<const char *>(&one),
                           sizeof(one)) == 0,
                "remote_socket_failed");
#endif
    }
    void wait(int result, Clock::time_point deadline) {
        auto error = SSL_get_error(tls.get(), result);
        require(error == SSL_ERROR_WANT_READ || error == SSL_ERROR_WANT_WRITE, "remote_connection_failed");
        auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - Clock::now()).count();
        require(left > 0, "remote_transport_timeout");
        require(ready(socket.value, error == SSL_ERROR_WANT_WRITE, std::uint32_t(left), allowed),
                "remote_transport_timeout");
    }
    void handshake(bool server) {
        [[maybe_unused]] SignalGuard signals;
        auto deadline = Clock::now() + std::chrono::seconds(5);
        for (;;) {
            permission(allowed);
            int r = server ? SSL_accept(tls.get()) : SSL_connect(tls.get());
            if (r == 1)
                return;
            wait(r, deadline);
        }
    }
    void transfer(void *data, std::size_t count, bool write, Clock::time_point deadline) {
        [[maybe_unused]] SignalGuard signals;
        auto p = static_cast<unsigned char *>(data);
        while (count) {
            permission(allowed);
            std::size_t n = 0;
            int r = write ? SSL_write_ex(tls.get(), p, count, &n) : SSL_read_ex(tls.get(), p, count, &n);
            if (r != 1) {
                wait(r, deadline);
                continue;
            }
            require(n > 0 && n <= count, "remote_connection_failed");
            p += n;
            count -= n;
        }
    }
};
Connection::Connection(std::unique_ptr<Impl> p) : impl_(std::move(p)) {}
Connection::~Connection() = default;
Connection::Connection(Connection &&) noexcept = default;
Connection &Connection::operator=(Connection &&) noexcept = default;
Connection Connection::connect(const std::string &ip, std::uint16_t port, const Key &host, Continue allowed) {
    permission(allowed);
    require(port != 0, "remote_invalid_port");
    int length = 0;
    auto addr = address(ip, port, length);
    SocketOwner socket(::socket(addr.ss_family, SOCK_STREAM, IPPROTO_TCP));
    require(socket.value != invalid, "remote_socket_failed");
    nonblock(socket.value);
    if (::connect(socket.value, reinterpret_cast<sockaddr *>(&addr), length) != 0) {
        require(pending() && ready(socket.value, true, 5000, allowed), "remote_connect_failed");
        int err = 0;
#ifdef _WIN32
        int n = sizeof(err);
#else
        socklen_t n = sizeof(err);
#endif
        require(getsockopt(socket.value, SOL_SOCKET, SO_ERROR, reinterpret_cast<char *>(&err), &n) == 0 &&
                    err == 0,
                "remote_connect_failed");
    }
    auto p = std::make_unique<Impl>(std::move(socket), context(), std::move(allowed));
    p->handshake(false);
    std::unique_ptr<X509, decltype(&X509_free)> cert(SSL_get1_peer_certificate(p->tls.get()), X509_free);
    require(bool(cert), "remote_host_identity_failed");
    auto key = X509_get0_pubkey(cert.get());
    Key actual{};
    std::size_t n = actual.size();
    require(key && EVP_PKEY_is_a(key, "ED25519") &&
                EVP_PKEY_get_raw_public_key(key, actual.data(), &n) == 1 && n == host.size() &&
                CRYPTO_memcmp(actual.data(), host.data(), host.size()) == 0,
            "remote_host_identity_failed");
    return Connection(std::move(p));
}
Key Connection::exporter() const {
    Key key{};
    constexpr char label[] = "EXPORTER-NetGo-remote-v1";
    require(SSL_export_keying_material(impl_->tls.get(), key.data(), key.size(), label, sizeof(label) - 1,
                                       nullptr, 0, 0) == 1,
            "remote_exporter_failed");
    return key;
}
void Connection::authorize(Continue allowed) {
    permission(allowed);
    impl_->allowed = std::move(allowed);
}
void Connection::send(const Message &message) {
    require(impl_->output.empty(), "remote_output_pending");
    auto kind = static_cast<std::uint32_t>(message.kind);
    require(kind >= 1 && kind <= 12 && message.body.size() <= max_message, "remote_invalid_message");
    require(message.kind == Kind::video || message.body.size() <= 65536, "remote_invalid_message");
    auto deadline = Clock::now() + std::chrono::seconds(3);
    std::array<std::uint8_t, 12> header{'N', 'G', 'R', '2'};
    put(header.data() + 4, kind);
    put(header.data() + 8, std::uint32_t(message.body.size()));
    impl_->transfer(header.data(), header.size(), true, deadline);
    if (!message.body.empty())
        impl_->transfer(const_cast<std::uint8_t *>(message.body.data()), message.body.size(), true, deadline);
}
void Connection::enqueue(Message message) {
    auto kind = static_cast<std::uint32_t>(message.kind);
    auto &p = *impl_;
    permission(p.allowed);
    require(kind >= 1 && kind <= 12 && message.body.size() <= max_message, "remote_invalid_message");
    require(message.kind == Kind::video || message.body.size() <= 65536, "remote_invalid_message");
    require(p.output.size() < 64 && p.outputBytes + message.body.size() + 12 <= 2 * max_message,
            "remote_output_backpressure");
    Impl::Output item;
    item.bytes.reserve(message.body.size() + 12);
    item.bytes.resize(12);
    item.bytes[0] = 'N';
    item.bytes[1] = 'G';
    item.bytes[2] = 'R';
    item.bytes[3] = '2';
    put(item.bytes.data() + 4, kind);
    put(item.bytes.data() + 8, std::uint32_t(message.body.size()));
    item.bytes.insert(item.bytes.end(), message.body.begin(), message.body.end());
    item.deadline = Clock::now() + std::chrono::seconds(3);
    p.outputBytes += item.bytes.size();
    p.output.push_back(std::move(item));
}
bool Connection::outputPending() const { return !impl_->output.empty(); }
bool Connection::flush() {
    [[maybe_unused]] SignalGuard signals;
    auto &p = *impl_;
    // Bound work per iteration so input, revocation and audio retain service time.
    for (unsigned budget = 0; budget < 8 && !p.output.empty(); ++budget) {
        permission(p.allowed);
        auto &item = p.output.front();
        require(Clock::now() < item.deadline, "remote_transport_timeout");
        auto count = std::min<std::size_t>(16384, item.bytes.size() - item.offset);
        std::size_t n = 0;
        int result = SSL_write_ex(p.tls.get(), item.bytes.data() + item.offset, count, &n);
        if (result != 1) {
            auto error = SSL_get_error(p.tls.get(), result);
            require(error == SSL_ERROR_WANT_READ || error == SSL_ERROR_WANT_WRITE,
                    "remote_connection_failed");
            return false;
        }
        require(n > 0 && n <= count, "remote_connection_failed");
        item.offset += n;
        if (item.offset == item.bytes.size()) {
            p.outputBytes -= item.bytes.size();
            p.output.pop_front();
        }
    }
    return p.output.empty();
}
bool Connection::receive(Message &message, std::uint32_t idle_ms) {
    [[maybe_unused]] SignalGuard signals;
    require(idle_ms <= 30000, "remote_invalid_timeout");
    auto &p = *impl_;
    auto until = Clock::now() + std::chrono::milliseconds(idle_ms);
    for (;;) {
        permission(p.allowed);
        if (p.reading)
            require(Clock::now() < p.readDeadline, "remote_transport_timeout");
        auto *dest = p.headerReady ? p.incoming.body.data() + p.bodyBytes : p.header.data() + p.headerBytes;
        auto remaining =
            p.headerReady ? p.incoming.body.size() - p.bodyBytes : p.header.size() - p.headerBytes;
        std::size_t n = 0;
        int result = SSL_read_ex(p.tls.get(), dest, remaining, &n);
        if (result != 1) {
            auto error = SSL_get_error(p.tls.get(), result);
            require(error == SSL_ERROR_WANT_READ || error == SSL_ERROR_WANT_WRITE,
                    "remote_connection_failed");
            auto end = p.reading ? std::min(until, p.readDeadline) : until;
            auto left = std::chrono::duration_cast<std::chrono::milliseconds>(end - Clock::now()).count();
            if (left <= 0 ||
                !ready(p.socket.value, error == SSL_ERROR_WANT_WRITE, std::uint32_t(left), p.allowed)) {
                if (p.reading)
                    require(Clock::now() < p.readDeadline, "remote_transport_timeout");
                return false;
            }
            continue;
        }
        require(n > 0 && n <= remaining, "remote_connection_failed");
        if (!p.reading) {
            p.reading = true;
            p.readDeadline = Clock::now() + std::chrono::seconds(3);
        }
        if (p.headerReady)
            p.bodyBytes += n;
        else {
            p.headerBytes += n;
            if (p.headerBytes == p.header.size()) {
                auto kind = get(p.header.data() + 4), size = get(p.header.data() + 8);
                require(std::memcmp(p.header.data(), "NGR2", 4) == 0 && kind >= 1 && kind <= 12 &&
                            size <= max_message,
                        "remote_invalid_message");
                require(kind == static_cast<std::uint32_t>(Kind::video) || size <= 65536,
                        "remote_invalid_message");
                p.incoming.kind = static_cast<Kind>(kind);
                p.incoming.body.resize(size);
                p.headerReady = true;
            }
        }
        if (p.headerReady && p.bodyBytes == p.incoming.body.size()) {
            permission(p.allowed);
            message = std::move(p.incoming);
            p.incoming = {};
            p.headerBytes = p.bodyBytes = 0;
            p.headerReady = p.reading = false;
            return true;
        }
    }
}
struct Listener::Impl {
    SocketOwner socket;
    std::shared_ptr<SSL_CTX> context;
    Continue allowed;
    std::uint16_t port{};
};
Listener::Listener(const std::string &ip, std::uint16_t port, const Key &seed, Continue allowed)
    : impl_(std::make_unique<Impl>()) {
    permission(allowed);
    require(ip != "0.0.0.0" && ip != "::", "remote_wildcard_bind_forbidden");
    int length = 0;
    auto addr = address(ip, port, length);
    impl_->allowed = std::move(allowed);
    impl_->context = context();
    identity(impl_->context.get(), seed);
    impl_->socket.value = ::socket(addr.ss_family, SOCK_STREAM, IPPROTO_TCP);
    require(impl_->socket.value != invalid, "remote_socket_failed");
#ifdef _WIN32
    BOOL exclusive = TRUE;
    require(setsockopt(impl_->socket.value, SOL_SOCKET, SO_EXCLUSIVEADDRUSE,
                       reinterpret_cast<const char *>(&exclusive), sizeof(exclusive)) == 0,
            "remote_socket_failed");
#endif
    require(bind(impl_->socket.value, reinterpret_cast<sockaddr *>(&addr), length) == 0 &&
                listen(impl_->socket.value, 2) == 0,
            "remote_bind_failed");
    nonblock(impl_->socket.value);
#ifdef _WIN32
    int n = sizeof(addr);
#else
    socklen_t n = sizeof(addr);
#endif
    require(getsockname(impl_->socket.value, reinterpret_cast<sockaddr *>(&addr), &n) == 0,
            "remote_socket_failed");
    impl_->port = ntohs(addr.ss_family == AF_INET ? reinterpret_cast<sockaddr_in *>(&addr)->sin_port
                                                  : reinterpret_cast<sockaddr_in6 *>(&addr)->sin6_port);
}
Listener::~Listener() = default;
std::uint16_t Listener::port() const { return impl_->port; }
std::unique_ptr<Connection> Listener::accept(std::uint32_t idle_ms) {
    require(idle_ms <= 30000, "remote_invalid_timeout");
    if (!ready(impl_->socket.value, false, idle_ms, impl_->allowed))
        return {};
    SocketOwner socket(::accept(impl_->socket.value, nullptr, nullptr));
    require(socket.value != invalid, "remote_accept_failed");
    auto p = std::make_unique<Connection::Impl>(std::move(socket), impl_->context, impl_->allowed);
    p->handshake(true);
    return std::unique_ptr<Connection>(new Connection(std::move(p)));
}
} // namespace netgo::remote
