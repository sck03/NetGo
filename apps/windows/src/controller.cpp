#include "connection.hpp"
#include "ui_preferences.hpp"
#include <algorithm>
#include <openssl/rand.h>
#include <regex>
#include <shared_mutex>

namespace netgo {
bool recoverableConnectionError(const std::string& error) {
 static constexpr const char* retryable[] = {"lease_expired", "control_timeout", "control_unreachable",
  "control_dns_failed", "no_trusted_control_entry", "engine_connect_timeout", "engine_recovering"};
 return std::find(std::begin(retryable), std::end(retryable), error) != std::end(retryable);
}
void Controller::closeRemote(){
 std::lock_guard lock(mutex_);remoteReady_=false;remoteContext_=Json::object();
 for(auto& job:remoteJobs_)job->result.set_value({{"error","remote_site_disconnected"}});
 remoteJobs_.clear();
 remoteWake_.notify_all();
}
Json Controller::remoteCall(const Json& request){
 auto job=std::make_shared<RemoteJob>();job->request=request;auto future=job->result.get_future();
 {std::lock_guard lock(mutex_);if(!remoteReady_||cancel_||request.value("site","")!=remoteContext_.value("site_id",""))throw Error("remote_site_disconnected");
  if(request.at("action")=="remote-context")return remoteContext_;
  if(remoteJobs_.size()>=8)throw Error("remote_busy");
  remoteJobs_.push_back(job);}
 remoteWake_.notify_one();
 if(future.wait_for(std::chrono::seconds(25))!=std::future_status::ready){job->abandoned=true;throw Error("remote_control_timeout");}
 return future.get();
}
Controller::Controller(std::filesystem::path root,bool engineEnabled):root_(std::move(root)),engineEnabled_(engineEnabled) {
 secureDirectory(root_);
 auto file=root_/L".generation";
 if(std::filesystem::exists(file)){
  auto saved=readFile(file,32);
  if(saved.empty()||saved.find_first_not_of("0123456789")!=std::string::npos)throw Error("invalid_generation");
  generation_=std::stoull(saved);
  if(generation_>UINT64_MAX-1000000)throw Error("invalid_generation");
 }
 status_["generation"]=generation_;
}
Controller::~Controller(){try{disconnect();}catch(...){}}
Json Controller::status(){std::lock_guard lock(mutex_);auto result=status_;auto name=result.value("state","");result["remote_ready"]=remoteReady_&&!cancel_&&(name=="connected"||name=="negotiating");return result;}
void Controller::state(uint64_t gen,const std::string& name,const std::string& error,const Json& fields) {
 std::lock_guard lock(mutex_);if(gen!=generation_)return;
 if(status_.value("state","")!=name||status_.value("error","")!=error){
  logs_.push_back({{"time",std::time(nullptr)},{"state",name},{"error",error}});
  if(logs_.size()>200)logs_.erase(logs_.begin());
 }
 status_["state"]=name;status_["error"]=error;status_.update(fields);
}
Json Controller::profiles() {
 Json result=Json::array();
 for(const auto& f:std::filesystem::directory_iterator(root_))if(f.is_regular_file()&&f.path().extension()==L".json"){
  try{auto p=Json::parse(readFile(f.path()));validateProfile(p);result.push_back(p);}catch(const std::exception&){}
 }return result;
}
void Controller::importProfile(const Json& p,bool replaceTrust) {
 std::lock_guard op(operation_);validateProfile(p);auto s=status();
 if(cleanupFailed_)throw Error("engine_cleanup_failed");
 if(s["state"]!="disconnected"&&s["state"]!="failed")throw Error("disconnect_before_edit");
 std::string id=p.at("ID");auto file=root_/wide(id+".json");
 if(std::filesystem::exists(file)){
  auto old=Json::parse(readFile(file));
  try{validateProfileUpdate(old,p);}catch(const Error&){if(!replaceTrust||old.at("ID")!=p.at("ID")||old.at("Protocol")!=p.at("Protocol")||p.value("ConfigRevision",uint64_t(0))<=old.value("ConfigRevision",uint64_t(0)))throw;}
  if(profileTrustIdentity(old)!=profileTrustIdentity(p)){
   credentialDelete("password/"+id);
   UiPreferences::update([&](auto& settings) { settings.forgetSite(id); }, root_);
  }
 }atomicFile(file,p.dump(2));
}
void Controller::disconnectLocked() {
 cancel_=true;
 closeRemote();
 {std::lock_guard lock(mutex_);++generation_;status_={{"state","disconnecting"},{"generation",generation_}};}
 if(worker_.joinable())worker_.join();
 if(cleanupFailed_){
  std::atomic_bool stop=false;
  try{engineCall({{"action","down"}},stop);cleanupFailed_=false;}
  catch(...){std::lock_guard lock(mutex_);status_={{"state","failed"},{"error","engine_cleanup_failed"},{"generation",generation_}};throw Error("engine_cleanup_failed");}
 }
 {std::lock_guard lock(mutex_);status_={{"state","disconnected"},{"generation",generation_}};}
}
void Controller::disconnect(){std::lock_guard op(operation_);disconnectLocked();}
void Controller::recoverEngine(){
 std::lock_guard op(operation_);
 cleanupFailed_=true;
 disconnectLocked();
}
void Controller::connect(const Json& r) {
 std::lock_guard op(operation_);std::string id=r.at("site");
 if(!std::regex_match(id,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)")))throw Error("invalid_site");
 auto p=Json::parse(readFile(root_/wide(id+".json")));validateProfile(p);
 std::string alias=r.value("alias",""),password=r.value("password","");
 bool remember=r.value("remember",false);
 if(password.empty()){
  auto saved=rememberedLogin(p);
  if(saved.contains("password")){
   if(alias.empty()||alias==saved.at("alias").get<std::string>()){alias=saved.at("alias");password=saved.at("password");}
   wipe(saved["password"].get_ref<std::string&>());
  }
 }
 if(alias.empty()||password.empty()||alias.size()>256||password.size()>2048)throw Error("credentials_required");
 disconnectLocked();cancel_=false;uint64_t gen;
 {std::lock_guard lock(mutex_);gen=++generation_;atomicFile(root_/L".generation",std::to_string(gen));status_={{"generation",gen},{"state","probing"},{"site_id",id}};}
 worker_=std::thread([this,gen,p,alias,password=std::move(password),remember,replace=r.value("replace",false),internet=r.value("internet",false)]() mutable{
  unsigned retries = 0;
  while (!cancel_) {
   try { run(gen,p,alias,std::move(password),replace,remember,internet); break; }
   catch (const std::exception& e) {
    wipe(password);
    std::string error=e.what();if(!std::regex_match(error,std::regex(R"(^[a-zA-Z0-9_]{1,80}$)")))error="connection_failed";
    if (cancel_) break;
    if (cleanupFailed_) error = "engine_cleanup_failed";
    const auto allowed = [&] {
     auto settings = UiPreferences::load(root_);
     return settings.autoConnect && settings.autoConnectSite == p.at("ID").get<std::string>();
    };
    if (!recoverableConnectionError(error) || !allowed()) { state(gen,"failed",error); break; }
    state(gen,"recovering",error);
    const unsigned seconds = std::min(30u, 1u << std::min(retries++, 5u));
    for (unsigned i=0; i<seconds*5 && !cancel_; ++i) Sleep(200);
    if (cancel_) break;
    if (!allowed()) { state(gen,"failed",error); break; }
    try {
     auto latest = Json::parse(readFile(root_/wide(p.at("ID").get<std::string>()+".json")));
     validateProfileUpdate(p, latest);
     p = std::move(latest);
     auto saved = rememberedLogin(p);
     if (!saved.contains("password")) { state(gen,"failed","credentials_required"); break; }
     alias = saved.at("alias").get<std::string>();
     password = std::move(saved["password"].get_ref<std::string&>());
     // Recovery is a new authorization, never permission to replace another device.
     replace = false;
     remember = true;
     std::lock_guard lock(mutex_);
     if (cancel_ || gen != generation_) break;
     gen = ++generation_;
     atomicFile(root_/L".generation",std::to_string(gen));
     status_={{"generation",gen},{"state","probing"},{"site_id",p.at("ID")}};
    } catch (...) { state(gen,"failed","credentials_required"); break; }
   }
  }
  wipe(password);
 });
 wipe(password);
}
void Controller::run(uint64_t gen,Json p,std::string alias,std::string password,bool replace,bool remember,bool internet) {
 std::string seed,token,refresh,jwt;Json lease=Json::object();std::unique_ptr<Transport> transport;
 std::shared_timed_mutex authMutex;std::atomic_bool remoteStop=false;std::thread remoteWorker;
 bool prepared=false, refreshPending=false;
 const auto scope=accountScope(p.at("ID"),alias);
 auto check=[&]{if(cancel_)throw Error("cancelled");};
 auto engine=[&](const std::string& action,const Json& fields=Json::object()){
  if(!engineEnabled_)throw Error("engine_disabled_in_test");
  check();auto profile=p;profile["ID"]=scope;Json request={{"action",action},{"profile",profile},{"internet",internet}};request.update(fields);
  if (action == "routes") request["resources"] = engineRouteResources(fields.at("resources"));
  return engineCall(request,cancel_);
 };
 auto cleanup=[&]{
  remoteStop=true;
  closeRemote();
  if(remoteWorker.joinable())remoteWorker.join();
  std::atomic_bool cleanupCancel=false;
  if(transport&&!token.empty()&&lease.contains("id"))try{transport->call("DELETE","/netgo/v2/sessions/"+lease["id"].get<std::string>(),nullptr,seed,token,"",{{},true});}catch(...){}
  if(prepared)try{engineCall({{"action","down"}},cleanupCancel);cleanupFailed_=false;}catch(...){cleanupFailed_=true;}
  wipe(password);wipe(seed);wipe(token);wipe(refresh);wipe(jwt);
 };
 try {
  transport=std::make_unique<Transport>(p,std::chrono::seconds(30),[&]{return cancel_.load()||remoteStop.load();});transport->select();check();
  state(gen,"authenticating","",{{"control",transport->kind()},{"control_candidate",transport->candidate()}});
  std::string id=p.at("ID");seed=credentialRead("device/"+scope);
  if(seed.empty()){seed.resize(32);if(RAND_bytes(reinterpret_cast<unsigned char*>(seed.data()),32)!=1)throw Error("random_failed");credentialWrite("device/"+scope,seed);}
  // Prepare disconnects old engine state before producing this profile's WireGuard key.
  prepared=true;auto wg=engine("prepare").at("wireguard_key");
  auto verifier=randomID();
  auto tx=transport->call("POST","/netgo/v2/auth/transactions",{{"site_id",id},{"public_key",publicKey(seed)},{"wireguard_key",wg},{"challenge",sha256(verifier,true)}});
  check();std::string proof="netgo-native-v1\n"+id+"\n"+tx.at("id").get<std::string>()+"\n"+tx.at("nonce").get<std::string>();
  std::string base="/netgo/v2/auth/transactions/"+tx.at("id").get<std::string>();
  transport->call("POST",base+"/complete",{{"Alias",alias},{"Password",password},{"Signature",signature(seed,proof)}});check();
  auto auth=transport->call("POST",base+"/exchange",{{"Verifier",verifier},{"Signature",signature(seed,proof+"\nexchange\n"+verifier)}});
  wipe(verifier);token=auth.at("token");refresh=auth.value("refresh_token","");
  auto deviceID=auth.at("device_id").get<std::string>();
  auto userID=auth.at("user").at("id").get<std::string>();
  auto authExpires=parseTime(auth.at("expires_at"));jwt=auth.at("id_token");auth=Json::object();
  auto next=transport->call("GET","/netgo/v2/site",nullptr,seed,token);validateProfileUpdate(p,next);next["Name"]=p["Name"];check();
  if(next!=p){
   auto updated=std::make_unique<Transport>(next,std::chrono::seconds(30),[&]{return cancel_.load()||remoteStop.load();});updated->select();check();
   atomicFile(root_/wide(id+".json"),next.dump(2));p=std::move(next);transport=std::move(updated);
   state(gen,"authenticating","",{{"control",transport->kind()},{"control_candidate",transport->candidate()}});
  }
  engine("login",{{"id_token",jwt}});engine("up");
  state(gen,"negotiating");
  auto timeout=Clock::now()+std::chrono::seconds(45);
  bool connected=false;
  while(Clock::now()<timeout){check();auto d=engine("status");if(d.value("state","")=="Connected"||d.value("state","")=="connected"){connected=true;break;}Sleep(200);}
  if(!connected)throw Error("engine_connect_timeout");
  check();auto peer=transport->call("GET","/netgo/v2/peer",nullptr,seed,token);
  lease=transport->call("POST","/netgo/v2/sessions",{{"peer_id",peer.at("ID")},{"generation",gen},{"replace",replace}},seed,token,randomID());
  if(internet&&p.value("InternetDNS",Json::array()).empty())throw Error("internet_not_configured");
  auto resources=selectedResources(lease.at("resources"),internet);routeConflicts(resources);
  engine("routes",{{"resources",resources}});
  if(remember)rememberLogin(p,alias,password);else credentialDelete("password/"+id);
  wipe(password);state(gen,"negotiating","",{{"lease",lease},{"internet",internet}});
  {std::lock_guard lock(mutex_);remoteReady_=true;remoteContext_={{"site_id",id},{"credential_scope",scope},{"user_id",userID},{"device_id",deviceID},{"generation",gen},{"lease",lease},{"public_key",publicKey(seed)}};}
  auto remoteWork=[&](const std::shared_ptr<RemoteJob>& job){
   if(!job||job->abandoned)return;
   try{
    check();auto method=job->request.value("method",""),path=job->request.value("path","");
    bool allowed=(method=="GET"&&std::regex_match(path,std::regex(R"(^/netgo/v2/remote(\?after=[A-Za-z0-9_-]{1,128})?$)")))||(method=="PUT"&&path=="/netgo/v2/remote/host")||(method=="POST"&&(path=="/netgo/v2/remote/host/heartbeat"||path=="/netgo/v2/remote/sessions"));
    allowed=allowed||((method=="POST"||method=="DELETE")&&std::regex_match(path,std::regex(method=="POST"?R"(^/netgo/v2/remote/sessions/[A-Za-z0-9_-]{22}/(consume|renew)$)":R"(^/netgo/v2/remote/sessions/[A-Za-z0-9_-]{22}$)")));
    allowed=allowed||(method=="GET"&&std::regex_match(path,std::regex(R"(^/netgo/v2/remote/sessions/[A-Za-z0-9_-]{22}$)")));
    if(!allowed)throw Error("remote_invalid_operation");
    std::shared_lock authLock(authMutex, std::defer_lock);
    while (!authLock.try_lock_for(std::chrono::milliseconds(25))) {
     check();
     if (remoteStop || job->abandoned) throw Error("cancelled");
    }
    check();
    if (remoteStop || job->abandoned) throw Error("cancelled");
    if (refreshPending) throw Error("authentication_refresh_pending");
    auto value=transport->call(method,path,job->request.value("body",Json()),seed,token,job->request.value("idempotency",""),{[&]{return job->abandoned.load();}});
    check();job->result.set_value(std::move(value));
   }catch(const std::exception& e){job->result.set_value({{"error",e.what()}});}
  };
  // The bounded command worker stays responsive while WAN probing and route
  // recovery wait on the network engine. Cleanup joins it before wiping keys.
  remoteWorker=std::thread([&]{
   while(!remoteStop&&!cancel_){
    std::shared_ptr<RemoteJob> job;
    {std::unique_lock lock(mutex_);remoteWake_.wait(lock,[&]{return remoteStop||cancel_||!remoteJobs_.empty();});
     if(remoteStop||cancel_)break;
     job=remoteJobs_.front();remoteJobs_.pop_front();}
    remoteWork(job);
   }
  });
  auto renewed=Clock::now();int failed=0;
  while(!cancel_) {
   for(int i=0;i<25&&!cancel_;++i)Sleep(200);
   check();
   if(transport->now()>=parseTime(lease.at("expires_at")))throw Error("lease_expired");
   try{
    transport->recover();check();
    if(authExpires-transport->now()<std::chrono::minutes(2)&&!refresh.empty()){
     std::unique_lock authLock(authMutex, std::defer_lock);
     const auto refreshDeadline = Clock::now() + std::chrono::seconds(4);
     while (!authLock.try_lock_for(std::chrono::milliseconds(25))) {
      check();
      if (Clock::now() >= refreshDeadline) throw Error("control_timeout");
     }
     check();
     refreshPending = true;
     Json authNew;
     for (int attempt = 0; attempt < 2; ++attempt) {
      check();
      try {
       RequestOptions options;
       options.deadline = std::min(refreshDeadline, Clock::now() + std::chrono::seconds(2));
       authNew = transport->call("POST", "/netgo/v2/auth/refresh", {{"refresh_token", refresh}}, seed, token, "", options);
       if (authNew.value("token", "").empty()) throw Error("invalid_auth_response");
       break;
      } catch (const Error& e) {
       const std::string error = e.what();
       if (attempt || cancel_ || error == "unauthorized" || error == "forbidden" || error == "not_found" || Clock::now() >= refreshDeadline) throw;
      }
     }
     auto nextToken = authNew.at("token").get<std::string>();
     auto nextRefresh = authNew.value("refresh_token", "");
     auto nextJWT = authNew.at("id_token").get<std::string>();
     auto nextExpiry = parseTime(authNew.at("expires_at"));
     wipe(token); wipe(refresh); wipe(jwt);
     token = std::move(nextToken); refresh = std::move(nextRefresh); jwt = std::move(nextJWT); authExpires = nextExpiry;
     refreshPending = false;
     authLock.unlock();
     engine("login",{{"id_token",jwt}});engine("up");engine("routes",{{"resources",selectedResources(lease.at("resources"),internet)}});
    }
    if(Clock::now()-renewed>=std::chrono::seconds(30)||parseTime(lease.at("expires_at"))-transport->now()<std::chrono::seconds(40)){
     lease=transport->call("POST","/netgo/v2/sessions/"+lease.at("id").get<std::string>()+"/renew",{{"Generation",gen}},seed,token);renewed=Clock::now();
     engine("routes",{{"resources",selectedResources(lease.at("resources"),internet)}});
     {std::lock_guard lock(mutex_);remoteContext_["lease"]=lease;}
    }
    auto data=engine("status");
    auto engineState=data.value("state","");
    if(engineState!="Connected"&&engineState!="connected"){
     engine("login",{{"id_token",jwt}});engine("up");engine("routes",{{"resources",selectedResources(lease.at("resources"),internet)}});
     throw Error("engine_recovering");
    }
    failed=0;
    auto path=data.value("path","negotiating");state(gen,!lease.at("resources").empty()&&path=="negotiating"?"negotiating":"connected","",{{"data",data},{"lease",lease},{"control",transport->kind()},{"control_candidate",transport->candidate()}});
   }catch(const Error& e){
    std::string error=e.what();
    if(error=="unauthorized"||error=="forbidden"||error=="not_found"||error=="lease_expired"||error=="session_conflict")throw;
    state(gen,"recovering",error);++failed;
    // Bounded backoff remains below the server lease deadline.
    for(int i=0;i<std::min(failed,4)*5&&!cancel_;++i)Sleep(200);
   }
  }
 }catch(...){cleanup();throw;}
 cleanup();
}
Json Controller::dispatch(const Json& r) {
 try {
  if(!r.is_object()||!r.contains("action")||!r["action"].is_string())throw Error("invalid_request");
  std::string action=r["action"];
  if(action=="status")return status();
  if(action=="remote-context"||action=="remote-call")return remoteCall(r);
  if(action=="profiles")return profiles();
  if(action=="import"){importProfile(r.at("profile"),r.value("replace_trust",false));return {{"imported",true}};}
  if(action=="connect"){connect(r);return status();}
  if(action=="disconnect"){disconnect();return status();}
  if(action=="logs"){std::lock_guard lock(mutex_);return logs_;}
  if(action=="diagnostic"){std::lock_guard lock(mutex_);return safeDiagnostic(status_,logs_);}
  if(action=="clear-logs"){std::lock_guard lock(mutex_);logs_=Json::array();return {{"cleared",true}};}
  if(action=="credential-info"||action=="forget"||action=="delete"){
   std::string id=r.at("site");if(!std::regex_match(id,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)")))throw Error("invalid_site");
   if(action=="credential-info"){
    std::lock_guard op(operation_);auto p=Json::parse(readFile(root_/wide(id+".json")));auto saved=rememberedLogin(p);
    auto result=Json({{"alias",saved.value("alias","")},{"remembered",saved.contains("password")}});
    if(saved.contains("password"))wipe(saved["password"].get_ref<std::string&>());
    return result;
   }
   std::lock_guard op(operation_);auto s=status();if(s["state"]!="disconnected"&&s["state"]!="failed")throw Error("disconnect_before_edit");
   if(cleanupFailed_)throw Error("engine_cleanup_failed");
   if(action=="delete"&&engineEnabled_){
    auto file=root_/wide(id+".json");
    if(std::filesystem::exists(file)){
     auto p=Json::parse(readFile(file));std::atomic_bool stop=false;
     for(const auto& target:credentialTargets("device/"+accountPrefix(id))){
      auto profile=p;profile["ID"]=target.substr(7);
      try{engineCall({{"action","remove"},{"profile",profile}},stop);}catch(const Error& e){if(std::string(e.what())!="engine_missing")throw;}
     }
    }
   }
   credentialDelete("password/"+id);
   UiPreferences::update([&](auto& settings) { settings.forgetSite(id); }, root_);
   if(action=="delete"){for(const auto& target:credentialTargets("device/"+accountPrefix(id)))credentialDelete(target);std::filesystem::remove(root_/wide(id+".json"));}
   return {{"done",true}};
  }
  throw Error("unsupported_action");
 }catch(const Error& e){return {{"error",e.what()}};}
 catch(const std::exception&){return {{"error","invalid_request"}};}
}
}
