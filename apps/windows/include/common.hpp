#pragma once
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <nlohmann/json.hpp>
#include <atomic>
#include <chrono>
#include <filesystem>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <stdexcept>
#include <thread>
#include <vector>

namespace netgo {
#ifndef NETGO_BUILD_VERSION
#error NetGo version must be supplied by the build configuration
#endif
inline constexpr char appVersion[] = NETGO_BUILD_VERSION;
using Json = nlohmann::json;
using Clock = std::chrono::steady_clock;
inline constexpr size_t maxJson = 1024 * 1024;
struct Error : std::runtime_error { using std::runtime_error::runtime_error; };
struct Handle {
 HANDLE value = nullptr;
 explicit Handle(HANDLE h = nullptr): value(h) {}
 ~Handle() { if(value && value != INVALID_HANDLE_VALUE) CloseHandle(value); }
 Handle(const Handle&) = delete;
 Handle& operator=(const Handle&) = delete;
 operator HANDLE() const { return value; }
};
std::wstring wide(const std::string& s);
std::string utf8(const std::wstring& s);
std::string sid(HANDLE process = GetCurrentProcess());
std::filesystem::path dataRoot();
std::filesystem::path executableDir();
void secureDirectory(const std::filesystem::path& path);
std::string readFile(const std::filesystem::path& path, size_t limit = maxJson);
void atomicFile(const std::filesystem::path& path, const std::string& data);
void wipe(std::string& value);
std::string credentialRead(const std::string& target);
void credentialWrite(const std::string& target, const std::string& value);
void credentialDelete(const std::string& target);
std::vector<std::string> credentialTargets(const std::string& prefix);
std::string accountPrefix(const std::string& site);
std::string accountScope(const std::string& site,const std::string& alias);
std::string base64url(const unsigned char* p, size_t n);
std::string randomID();
std::string sha256(const std::string& s, bool url = false);
std::chrono::system_clock::time_point parseTime(const std::string& s);
std::wstring pipeName();
void testPipe(const std::filesystem::path& root);
Json pipeCall(const Json& request,const std::function<bool()>& active={});
void servePipe(std::atomic_bool& stop, const std::function<Json(const Json&)>& dispatch);

struct Address { std::string host; unsigned short port; };
Address address(const std::string& s);
std::string originHost(const std::string& origin);
void validateProfile(const Json& p);
Json profileEndpoints(const Json& profile,const std::string& direct,const std::string& auxiliary);
void validateProfileUpdate(const Json& previous,const Json& next);
std::string profileTrustIdentity(const Json& profile);
Json decodeRememberedLogin(const Json& profile,const std::string& encoded);
Json rememberedLogin(const Json& profile);
void rememberLogin(const Json& profile,const std::string& alias,const std::string& password);
}
