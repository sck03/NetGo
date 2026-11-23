#pragma once
#include "common.hpp"
#include <deque>
#include <future>

struct ssl_ctx_st;
namespace netgo {
std::string signature(const std::string& seed, const std::string& s);
std::string publicKey(const std::string& seed);
std::string signedMessage(const std::string& method,const std::string& path,const std::string& stamp,const std::string& nonce,const std::string& body,const std::string& token);
Json engineCall(const Json& request, const std::atomic_bool& cancel);
void routeConflicts(const Json& resources);
Json safeDiagnostic(const Json& status, const Json& logs);
void validateHealth(const Json& health,const std::string& siteID);
struct HttpResponse { int status; std::string body; };
HttpResponse decodeHttp(const std::string& raw);
class Transport {
 Json profile_;
 std::shared_ptr<ssl_ctx_st> context_;
 size_t selected_ = 0;
 int directSuccess_ = 0;
 size_t directCandidate_ = SIZE_MAX;
 std::chrono::milliseconds cooldown_ = std::chrono::seconds(30);
 Clock::time_point switched_ = Clock::now();
 std::atomic_bool incompatible_{false};
 std::atomic_bool clockSkew_{false};
 Json requestAt(size_t index,const std::string& method,const std::string& path,const std::string& body,const std::string& headers);
 bool probe(size_t index);
public:
 explicit Transport(Json profile,std::chrono::milliseconds cooldown=std::chrono::seconds(30));
 void select();
 void recover();
 std::string kind() const;
 std::string candidate() const;
 Json call(const std::string& method,const std::string& path,const Json& body,const std::string& seed = {},const std::string& token = {},const std::string& idempotency = {});
};

class Controller {
 std::filesystem::path root_;
 std::mutex mutex_, operation_;
 Json status_ = {{"state","disconnected"},{"generation",0}};
 Json logs_ = Json::array();
 std::thread worker_;
 struct RemoteJob {Json request;std::promise<Json> result;std::atomic_bool abandoned{false};};
 std::deque<std::shared_ptr<RemoteJob>> remoteJobs_;
 bool remoteReady_=false;
 Json remoteContext_;
 Json remoteCall(const Json&);
 void closeRemote();
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
