#pragma once
#include "common.hpp"
#include <functional>

namespace netgo::desktop {
struct Frame {unsigned width{},height{};std::vector<unsigned char> bgra;};
struct Callbacks {
 std::function<bool()> running;
 std::function<void(std::string)> status;
 std::function<void(Frame)> present;
 std::function<bool(const std::string&,uint32_t)> consent;
 std::function<std::vector<Json>()> inputs;
};
void host(const std::string& site,Callbacks callbacks);
void view(const std::string& site,const std::string& host,Callbacks callbacks);
Json inventory(const std::string& site);
}
