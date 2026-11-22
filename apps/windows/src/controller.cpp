#include "netgo.hpp"
#include <openssl/rand.h>
#include <regex>

namespace netgo {
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
Json Controller::status(){std::lock_guard lock(mutex_);return status_;}
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
void Controller::importProfile(const Json& p) {
 std::lock_guard op(operation_);validateProfile(p);auto s=status();
 if(cleanupFailed_)throw Error("engine_cleanup_failed");
 if(s["state"]!="disconnected"&&s["state"]!="failed")throw Error("disconnect_before_edit");
 std::string id=p.at("ID");auto file=root_/wide(id+".json");
 if(std::filesystem::exists(file)){
  auto old=Json::parse(readFile(file));
  validateProfileUpdate(old,p);
 }atomicFile(file,p.dump(2));
}
void Controller::disconnectLocked() {
 cancel_=true;
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
void Controller::connect(const Json& r) {
 std::lock_guard op(operation_);std::string id=r.at("site");
 if(!std::regex_match(id,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)")))throw Error("invalid_site");
 auto p=Json::parse(readFile(root_/wide(id+".json")));validateProfile(p);
 std::string alias=r.value("alias",""),password=r.value("password","");
 bool remember=r.value("remember",false);
 if(password.empty()){
  auto saved=credentialRead("password/"+id);
  if(!saved.empty()){auto c=Json::parse(saved);if(alias.empty()||alias==c.at("alias").get<std::string>()){alias=c.at("alias");password=c.at("password");}wipe(saved);}
 }
 if(alias.empty()||password.empty()||alias.size()>256||password.size()>2048)throw Error("credentials_required");
 disconnectLocked();cancel_=false;uint64_t gen;
 {std::lock_guard lock(mutex_);gen=++generation_;atomicFile(root_/L".generation",std::to_string(gen));status_={{"generation",gen},{"state","probing"},{"site_id",id}};}
 worker_=std::thread([this,gen,p,alias,password=std::move(password),remember,replace=r.value("replace",false)]() mutable{
  try{run(gen,p,alias,std::move(password),replace,remember);}
  catch(const std::exception& e){
   std::string error=e.what();if(!std::regex_match(error,std::regex(R"(^[a-zA-Z0-9_]{1,80}$)")))error="connection_failed";
   if(!cancel_)state(gen,"failed",cleanupFailed_?"engine_cleanup_failed":error);
  }wipe(password);
 });
 wipe(password);
}
void Controller::run(uint64_t gen,Json p,std::string alias,std::string password,bool replace,bool remember) {
 std::string seed,token,refresh,jwt;Json lease=Json::object();std::unique_ptr<Transport> transport;
 bool prepared=false;
 auto check=[&]{if(cancel_)throw Error("cancelled");};
 auto engine=[&](const std::string& action,const Json& fields=Json::object()){
  if(!engineEnabled_)throw Error("engine_disabled_in_test");
  check();Json request={{"action",action},{"profile",p}};request.update(fields);return engineCall(request,cancel_);
 };
 auto cleanup=[&]{
  std::atomic_bool cleanupCancel=false;
  if(transport&&!token.empty()&&lease.contains("id"))try{transport->call("DELETE","/netgo/v1/sessions/"+lease["id"].get<std::string>(),nullptr,seed,token);}catch(...){}
  if(prepared)try{engineCall({{"action","down"}},cleanupCancel);cleanupFailed_=false;}catch(...){cleanupFailed_=true;}
  wipe(password);wipe(seed);wipe(token);wipe(refresh);wipe(jwt);
 };
 try {
  transport=std::make_unique<Transport>(p);transport->select();check();
  state(gen,"authenticating","",{{"control",transport->kind()},{"control_candidate",transport->candidate()}});
  std::string id=p.at("ID");seed=credentialRead("device/"+id);
  if(seed.empty()){seed.resize(32);if(RAND_bytes(reinterpret_cast<unsigned char*>(seed.data()),32)!=1)throw Error("random_failed");credentialWrite("device/"+id,seed);}
  // Prepare disconnects old engine state before producing this profile's WireGuard key.
  prepared=true;auto wg=engine("prepare").at("wireguard_key");
  auto verifier=randomID();
  auto tx=transport->call("POST","/netgo/v1/auth/transactions",{{"site_id",id},{"public_key",publicKey(seed)},{"wireguard_key",wg},{"challenge",sha256(verifier,true)}});
  check();std::string proof="netgo-native-v1\n"+id+"\n"+tx.at("id").get<std::string>()+"\n"+tx.at("nonce").get<std::string>();
  std::string base="/netgo/v1/auth/transactions/"+tx.at("id").get<std::string>();
  transport->call("POST",base+"/complete",{{"Alias",alias},{"Password",password},{"Signature",signature(seed,proof)}});check();
  auto auth=transport->call("POST",base+"/exchange",{{"Verifier",verifier},{"Signature",signature(seed,proof+"\nexchange\n"+verifier)}});
  wipe(verifier);token=auth.at("token");refresh=auth.value("refresh_token","");
  auto authExpires=parseTime(auth.at("expires_at"));jwt=auth.at("id_token");auth=Json::object();
  auto next=transport->call("GET","/netgo/v1/site",nullptr,seed,token);validateProfileUpdate(p,next);next["Name"]=p["Name"];check();
  if(next!=p){
   auto updated=std::make_unique<Transport>(next);updated->select();check();
   atomicFile(root_/wide(id+".json"),next.dump(2));p=std::move(next);transport=std::move(updated);
   state(gen,"authenticating","",{{"control",transport->kind()},{"control_candidate",transport->candidate()}});
  }
  engine("login",{{"id_token",jwt}});engine("up");
  state(gen,"negotiating");
  auto timeout=Clock::now()+std::chrono::seconds(45);
  bool connected=false;
  while(Clock::now()<timeout){check();auto d=engine("status");if(d.value("state","")=="Connected"||d.value("state","")=="connected"){connected=true;break;}Sleep(200);}
  if(!connected)throw Error("engine_connect_timeout");
  check();auto peer=transport->call("GET","/netgo/v1/peer",nullptr,seed,token);
  lease=transport->call("POST","/netgo/v1/sessions",{{"peer_id",peer.at("ID")},{"generation",gen},{"replace",replace}},seed,token,randomID());
  auto resources=lease.at("resources");routeConflicts(resources);
  engine("routes",{{"resources",resources}});
  if(remember){auto saved=Json({{"alias",alias},{"password",password}}).dump();credentialWrite("password/"+id,saved);wipe(saved);}else credentialDelete("password/"+id);
  wipe(password);state(gen,"negotiating","",{{"lease",lease}});
  auto renewed=Clock::now();int failed=0;
  while(!cancel_) {
   for(int i=0;i<25&&!cancel_;++i)Sleep(200);
   check();
   if(std::chrono::system_clock::now()>=parseTime(lease.at("expires_at")))throw Error("lease_expired");
   try{
    transport->recover();check();
    if(authExpires-std::chrono::system_clock::now()<std::chrono::minutes(2)&&!refresh.empty()){
     auto authNew=transport->call("POST","/netgo/v1/auth/refresh",{{"refresh_token",refresh}},seed,token);
     wipe(token);wipe(refresh);wipe(jwt);token=authNew.at("token");refresh=authNew.value("refresh_token","");jwt=authNew.at("id_token");authExpires=parseTime(authNew.at("expires_at"));
     engine("login",{{"id_token",jwt}});engine("up");engine("routes",{{"resources",lease.at("resources")}});
    }
    if(Clock::now()-renewed>=std::chrono::seconds(30)||parseTime(lease.at("expires_at"))-std::chrono::system_clock::now()<std::chrono::seconds(40)){
     lease=transport->call("POST","/netgo/v1/sessions/"+lease.at("id").get<std::string>()+"/renew",{{"Generation",gen}},seed,token);renewed=Clock::now();
    }
    auto data=engine("status");
    auto engineState=data.value("state","");
    if(engineState!="Connected"&&engineState!="connected"){
     engine("login",{{"id_token",jwt}});engine("up");engine("routes",{{"resources",lease.at("resources")}});
     throw Error("engine_recovering");
    }
    failed=0;
    auto path=data.value("path","negotiating");state(gen,path=="negotiating"?"negotiating":"connected","",{{"data",data},{"lease",lease},{"control",transport->kind()},{"control_candidate",transport->candidate()}});
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
  if(action=="profiles")return profiles();
  if(action=="import"){importProfile(r.at("profile"));return {{"imported",true}};}
  if(action=="connect"){connect(r);return status();}
  if(action=="disconnect"){disconnect();return status();}
  if(action=="logs"){std::lock_guard lock(mutex_);return logs_;}
  if(action=="diagnostic"){std::lock_guard lock(mutex_);return safeDiagnostic(status_,logs_);}
  if(action=="clear-logs"){std::lock_guard lock(mutex_);logs_=Json::array();return {{"cleared",true}};}
  if(action=="credential-info"||action=="forget"||action=="delete"){
   std::string id=r.at("site");if(!std::regex_match(id,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)")))throw Error("invalid_site");
   if(action=="credential-info"){auto saved=credentialRead("password/"+id);Json c=saved.empty()?Json::object():Json::parse(saved);wipe(saved);return {{"alias",c.value("alias","")},{"remembered",c.contains("password")}};}
   std::lock_guard op(operation_);auto s=status();if(s["state"]!="disconnected"&&s["state"]!="failed")throw Error("disconnect_before_edit");
   if(cleanupFailed_)throw Error("engine_cleanup_failed");
   if(action=="delete"&&engineEnabled_){
    auto file=root_/wide(id+".json");
    if(std::filesystem::exists(file)){
     auto p=Json::parse(readFile(file));std::atomic_bool stop=false;
     try{engineCall({{"action","remove"},{"profile",p}},stop);}catch(const Error& e){if(std::string(e.what())!="engine_missing")throw;}
    }
   }
   credentialDelete("password/"+id);
   if(action=="delete"){credentialDelete("device/"+id);std::filesystem::remove(root_/wide(id+".json"));}
   return {{"done",true}};
  }
  throw Error("unsupported_action");
 }catch(const Error& e){return {{"error",e.what()}};}
 catch(const std::exception&){return {{"error","invalid_request"}};}
}
}
