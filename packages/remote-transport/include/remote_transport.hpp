#pragma once
#include <array>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <vector>

namespace netgo::remote {
using Key = std::array<std::uint8_t, 32>;
using Continue = std::function<bool()>;
enum class Kind : std::uint32_t {
    hello = 1,
    challenge,
    proof,
    accepted,
    video,
    input,
    renew,
    close,
    ping,
    audio,
    command,
    response
};
struct Message {
    Kind kind{};
    std::vector<std::uint8_t> body;
};
inline constexpr std::size_t max_message = 4 * 1024 * 1024;

// Private C++ transport adapter, separate from the public C authorization ABI.
// The caller supplies an authoritative numeric overlay address and device key.
// A connection belongs to exactly one worker. No DNS, public relay or reconnect.
class Connection final {
  public:
    ~Connection();
    Connection(Connection &&) noexcept;
    Connection &operator=(Connection &&) noexcept;
    Connection(const Connection &) = delete;
    static Connection connect(const std::string &numeric_address, std::uint16_t port, const Key &host_key,
                              Continue allowed);
    Key exporter() const;
    void authorize(Continue allowed);
    void send(const Message &message);
    // Bounded nonblocking output for media loops. The connection still has one owner.
    void enqueue(Message message);
    bool flush();
    bool outputPending() const;
    // False means no complete message is ready; the caller's message is unchanged.
    // Partial reads retain their state and share a 3-second total deadline across
    // polls. idle_ms bounds waiting; cancellation is checked at least every 100ms.
    bool receive(Message &message, std::uint32_t idle_ms = 100);

  private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
    explicit Connection(std::unique_ptr<Impl>);
    friend class Listener;
};

class Listener final {
  public:
    Listener(const std::string &numeric_address, std::uint16_t port, const Key &device_seed,
             Continue allowed);
    ~Listener();
    Listener(const Listener &) = delete;
    std::uint16_t port() const;
    // Listen socket is bound only to the supplied interface address.
    std::unique_ptr<Connection> accept(std::uint32_t idle_ms = 100);

  private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};
} // namespace netgo::remote
