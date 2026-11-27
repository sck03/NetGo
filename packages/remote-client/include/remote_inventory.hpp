#pragma once
#include <nlohmann/json.hpp>
#include <string>
#include <stdexcept>
#include <algorithm>

namespace netgo::remote {
// Pages keep HTTP and IPC bounded. The cursor must strictly advance, including
// when the server state changes during a refresh.
template<class Call>
nlohmann::json inventory(Call&& call, const std::string& wanted = {}) {
 using Json = nlohmann::json;
 Json result;
 std::string after;
 for (unsigned page = 0; page < 200; ++page) {
  auto value = call(after.empty() ? "/netgo/v2/remote" : "/netgo/v2/remote?after=" + after);
  if (result.is_null()) result = value;
  else {
   result["next_generation"] = value.at("next_generation");
   for (auto& host : value.at("hosts")) result["hosts"].push_back(std::move(host));
  }
  if (!wanted.empty()) for (const auto& host : result.at("hosts")) if (host.at("id") == wanted) return result;
  auto next = value.value("next_after", std::string());
  if (next.empty()) { result["next_after"] = ""; return result; }
  if (next <= after || next.size() > 128 || !std::all_of(next.begin(),next.end(),[](unsigned char c){return (c>='a'&&c<='z')||(c>='A'&&c<='Z')||(c>='0'&&c<='9')||c=='_'||c=='-';})) throw std::runtime_error("remote_invalid_page");
  after = next;
 }
 throw std::runtime_error("remote_inventory_too_large");
}
}
