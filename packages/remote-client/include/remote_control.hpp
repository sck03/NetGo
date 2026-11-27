#pragma once
#include <exception>
#include <string_view>

namespace netgo::remote {
// Retry transport failures only within the existing watchdog window. A rejected
// authorization, malformed response or cancelled operation remains terminal.
inline bool transient_control(const std::exception& error) {
 const std::string_view code(error.what());
 return code == "remote_control_failed" || code == "control_timeout" ||
        code == "control_unreachable" || code == "control_dns_failed" ||
        code == "control_tls_failed" || code == "ipc_failed" ||
        code == "context deadline exceeded" || code == "remote_busy";
}
}
