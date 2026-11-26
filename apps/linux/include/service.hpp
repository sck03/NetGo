#pragma once
#include <gio/gio.h>
#include <string>
namespace netgo::desktop_linux {
GSubprocess *launchService(const std::string &root, const std::string &socket);
// Returns true only when installation changed the service to restart.
bool ensureNetwork();
}
