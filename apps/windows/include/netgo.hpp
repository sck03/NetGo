#pragma once
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <nlohmann/json.hpp>
#include <openssl/ssl.h>
#include <atomic>
#include <chrono>
#include <filesystem>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

namespace netgo {
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
std::string base64url(const unsigned char* p, size_t n);
std::string randomID();
std::string sha256(const std::string& s, bool url = false);
std::string signature(const std::string& seed, const std::string& s);
std::string publicKey(const std::string& seed);
std::string signedMessage(const std::string& method,const std::string& path,const std::string& stamp,const std::string& nonce,const std::string& body,const std::string& token);
std::chrono::system_clock::time_point parseTime(const std::string& s);
std::wstring pipeName();
void testPipe(const std::filesystem::path& root);
Json pipeCall(const Json& request);
void servePipe(std::atomic_bool& stop, const std::function<Json(const Json&)>& dispatch);
Json engineCall(const Json& request, const std::atomic_bool& cancel);
void routeConflicts(const Json& resources);
Json safeDiagnostic(const Json& status, const Json& logs);

struct Address { std::string host; unsigned short port; };
Address address(const std::string& s);
std::string originHost(const std::string& origin);
void validateProfile(const Json& p);
struct HttpResponse { int status; std::string body; };
HttpResponse decodeHttp(const std::string& raw);
class Transport {
 Json profile_;
 std::shared_ptr<SSL_CTX> context_;
 size_t selected_ = 0;
 int directSuccess_ = 0;
 Clock::time_point switched_ = Clock::now();
 Json requestAt(size_t index,const std::string& method,const std::string& path,const std::string& body,const std::string& headers);
 bool probe(size_t index);
public:
 explicit Transport(Json profile);
 void select();
 void recover();
 std::string kind() const;
 Json call(const std::string& method,const std::string& path,const Json& body,const std::string& seed = {},const std::string& token = {},const std::string& idempotency = {});
};

class Controller {
 std::filesystem::path root_;
 std::mutex mutex_, operation_;
 Json status_ = {{"state","disconnected"},{"generation",0}};
 Json logs_ = Json::array();
 std::thread worker_;
 std::atomic_bool cancel_{false};
 std::atomic_bool cleanupFailed_{false};
 bool engineEnabled_ = true;
 uint64_t generation_ = 0;
 void state(uint64_t gen,const std::string& name,const std::string& error = {},const Json& fields = Json::object());
 void run(uint64_t gen,Json profile,std::string alias,std::string password,bool replace,bool remember);
 void disconnectLocked();
public:
 explicit Controller(std::filesystem::path root,bool engineEnabled = true);
 ~Controller();
 Json dispatch(const Json& request);
 Json status();
 Json profiles();
 void importProfile(const Json& p);
 void disconnect();
 void connect(const Json& request);
};
}
