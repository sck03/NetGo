#include "remote_control.hpp"
#include "remote_control_reply.hpp"
#include <stdexcept>
#include <cstdlib>

int main() {
    for (const auto* code : {"unauthorized", "forbidden", "session_conflict", "remote_user_inactive"}) {
        const std::runtime_error error(code);
        char output[256]{};
        size_t length = 0;
        if (netgo::remote::control_error(error, output, sizeof(output), &length) != 0) std::abort();
        const auto reply = nlohmann::json::parse(output, output + length);
        if (reply.at("error") != code || netgo::remote::transient_control(error)) std::abort();
        if (netgo::remote::control_error(error, output, 1, &length) != -1) std::abort();
    }
    for (const auto* code : {"control_timeout", "control_unreachable", "ipc_failed", "context deadline exceeded", "remote_busy"})
        if (!netgo::remote::transient_control(std::runtime_error(code))) std::abort();
}
