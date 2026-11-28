#pragma once
#include <gio/gio.h>
#include <atomic>
#include <memory>
#include <string>
namespace netgo::desktop_linux {
// Called by the serialized connection worker; completion only touches the exit
// flag. Owns its child, never a service started by another client instance.
class ServiceSupervisor {
    GSubprocess *process_ = nullptr;
    std::shared_ptr<std::atomic_bool> exited_;
    unsigned failures_ = 0;
    gint64 retryAt_ = 0;
public:
    ServiceSupervisor() = default;
    ServiceSupervisor(const ServiceSupervisor &) = delete;
    ServiceSupervisor &operator=(const ServiceSupervisor &) = delete;
    ~ServiceSupervisor();
    bool ensure(const std::string &root, const std::string &socket);
    void stop();
    void restart();
};
// Returns true only when installation changed the service to restart.
bool ensureNetwork();
}
