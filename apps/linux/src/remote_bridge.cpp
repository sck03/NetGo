#include "remote_bridge.hpp"
#include "client.hpp"
#include "remote_control_reply.hpp"

namespace netgo::desktop_linux {
int32_t remoteControl(const std::string& socket, const std::string& site, std::atomic_bool& active,
                      const char* method, const char* path, const char* body, const char* key,
                      char* output, size_t capacity, size_t* length) noexcept {
    try {
		const bool cleanup=std::string(method)=="DELETE";
		std::atomic_bool cleanupActive{true};
		auto& running=cleanup?cleanupActive:active;
		const long timeout=cleanup?2000L:4000L;
        const auto value = std::string(method) == "CONTEXT"
            ? ipc(socket, "GET", "/desktop/remote/context?site=" + site, nullptr, running, timeout)
            : ipc(socket, "POST", "/desktop/remote/call", {{"site", site}, {"method", method},
                  {"path", path}, {"body", *body ? Json::parse(body) : Json()}, {"idempotency", key}}, running, timeout);
        const auto text = value.dump();
        if (!output || !length || text.size() > capacity) return -1;
        std::copy(text.begin(), text.end(), output);
        *length = text.size();
        return 0;
    } catch (const std::exception& error) {
        return netgo::remote::control_error(error, output, capacity, length);
    } catch (...) { return -1; }
}
}
