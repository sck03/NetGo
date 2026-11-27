#pragma once
#include "common.hpp"
#include <array>
#include <deque>
#include <future>
#include <condition_variable>

struct ssl_ctx_st;
namespace netgo {
std::string signature(const std::string& seed, const std::string& s);
std::string publicKey(const std::string& seed);
std::string signedMessage(const std::string& method,const std::string& path,const std::string& stamp,const std::string& nonce,const std::string& body,const std::string& token);
Json engineCall(const Json& request, const std::atomic_bool& cancel);
void routeConflicts(const Json& resources);
Json selectedResources(const Json& resources,bool internet);
Json safeDiagnostic(const Json& status, const Json& logs);
void validateHealth(const Json& health,const std::string& siteID);
struct HttpResponse { int status; std::string body; };
HttpResponse decodeHttp(const std::string& raw);
struct RequestOptions {
 std::function<bool()> cancelled;
 bool cleanup = false;
};
class Transport {
 Json profile_;
 std::shared_ptr<ssl_ctx_st> context_;
 std::function<bool()> cancelled_;
 struct Link;
 // Independent persistent connections for probes, VPN and remote control.
 std::vector<std::array<std::shared_ptr<Link>, 3>> links_;
 std::atomic_size_t selected_{0};
 int directSuccess_ = 0;
 size_t directCandidate_ = SIZE_MAX;
 std::chrono::milliseconds cooldown_ = std::chrono::seconds(30);
 Clock::time_point switched_ = Clock::now();
 std::atomic_bool incompatible_{false};
 struct ClockSample {std::chrono::system_clock::time_point server{};Clock::time_point received{};bool valid=false;};
 std::vector<ClockSample> clocks_;
 mutable std::mutex clockMutex_;
 Json requestAt(size_t index,const std::string& method,const std::string& path,const std::string& body,const std::string& headers,const RequestOptions& options = {});
 bool probe(size_t index,const std::function<bool()>& cancelled = {});
public:
 explicit Transport(Json profile,std::chrono::milliseconds cooldown=std::chrono::seconds(30),std::function<bool()> cancelled = {});
 void select();
 void recover();
 std::string kind() const;
 std::string candidate() const;
 std::chrono::system_clock::time_point now() const;
 Json call(const std::string& method,const std::string& path,const Json& body,const std::string& seed = {},const std::string& token = {},const std::string& idempotency = {},const RequestOptions& options = {});
};

class Controller {
 std::filesystem::path root_;
 std::mutex mutex_, operation_;
 Json status_ = {{"state","disconnected"},{"generation",0}};
 Json logs_ = Json::array();
 std::thread worker_;
 struct RemoteJob {Json request;std::promise<Json> result;std::atomic_bool abandoned{false};};
 std::deque<std::shared_ptr<RemoteJob>> remoteJobs_;
 std::condition_variable remoteWake_;
 bool remoteReady_=false;
 Json remoteContext_;
 Json remoteCall(const Json&);
 void closeRemote();
 std::atomic_bool cancel_{false};
 std::atomic_bool cleanupFailed_{false};
 bool engineEnabled_ = true;
 uint64_t generation_ = 0;
 void state(uint64_t gen,const std::string& name,const std::string& error = {},const Json& fields = Json::object());
 void run(uint64_t gen,Json profile,std::string alias,std::string password,bool replace,bool remember,bool internet);
 void disconnectLocked();
public:
 explicit Controller(std::filesystem::path root,bool engineEnabled = true);
 ~Controller();
 Json dispatch(const Json& request);
 Json status();
 Json profiles();
 void importProfile(const Json& p,bool replaceTrust=false);
 void disconnect();
 void connect(const Json& request);
};
}
