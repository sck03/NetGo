#include "netgo_remote_client.h"
#include "remote_input.hpp"
#include <cstring>

struct ng_remote_input { netgo::remote::InputQueue queue; };
extern "C" ng_remote_input* ng_remote_input_create(void) {
 try { return new ng_remote_input; } catch (...) { return nullptr; }
}
extern "C" void ng_remote_input_destroy(ng_remote_input* input) { delete input; }
extern "C" void ng_remote_input_clear(ng_remote_input* input) { if (input) input->queue.clear(); }
extern "C" int32_t ng_remote_input_push(ng_remote_input* input, const char* json, size_t size) {
 if (!input || !json || !size || size > netgo::remote::input_batch_limit) return NG_REMOTE_CLIENT_INVALID;
 try { return input->queue.push(nlohmann::json::parse(json, json + size)) ? NG_REMOTE_CLIENT_OK : NG_REMOTE_CLIENT_INVALID; }
 catch (...) { return NG_REMOTE_CLIENT_INVALID; }
}
extern "C" int32_t ng_remote_input_read(ng_remote_input* input, char* output, size_t capacity, size_t* length) {
 if (length) *length = 0;
 if (!input || !output || !length || capacity < netgo::remote::input_batch_limit) return NG_REMOTE_CLIENT_INVALID;
 try {
  auto text = input->queue.take().dump();
  std::memcpy(output, text.data(), text.size()); *length = text.size(); return NG_REMOTE_CLIENT_OK;
 } catch (...) { return NG_REMOTE_CLIENT_FAILED; }
}
