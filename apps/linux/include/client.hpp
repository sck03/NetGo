#pragma once
#include <nlohmann/json.hpp>
#include <atomic>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <vector>
namespace netgo::desktop_linux {
using Json=nlohmann::json;
struct Frame {uint32_t width{},height{};std::vector<uint8_t> bgra;};
Json ipc(const std::string& socket,const std::string& method,const std::string& path,const Json& body,std::atomic_bool& active);
class Decoder {
public:
 Decoder();~Decoder();Decoder(const Decoder&)=delete;
 void decode(const uint8_t* bytes,size_t size,const std::function<void(Frame)>& present);
private:
 struct Impl;std::unique_ptr<Impl> impl;
};
}
