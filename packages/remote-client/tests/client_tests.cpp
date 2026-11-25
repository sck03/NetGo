#include "netgo_remote_client.h"
#include "netgo_remote_ticket.h"
#include "netgo_remote_clock.hpp"
#include "remote_transport.hpp"
#include <nlohmann/json.hpp>
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <atomic>
#include <chrono>
#include <cstring>
#include <iostream>
#include <mutex>
#include <thread>
#ifdef _WIN32
#include <winsock2.h>
#endif
namespace {
using Json=nlohmann::json;using Bytes=std::vector<uint8_t>;using Key=netgo::remote::Key;
using netgo::remote::Kind;using netgo::remote::Message;
void check(bool ok,const char* why){if(!ok)throw std::runtime_error(why);}
std::string encode(const uint8_t* bytes,size_t size){std::string out(4*((size+2)/3),'\0');auto n=EVP_EncodeBlock(reinterpret_cast<uint8_t*>(out.data()),bytes,int(size));out.resize(size_t(n));for(auto& c:out){if(c=='+')c='-';if(c=='/')c='_';}while(!out.empty()&&out.back()=='=')out.pop_back();return out;}
Key publicKey(const Key& seed){auto k=EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519,nullptr,seed.data(),32);Key pub{};size_t n=32;check(k&&EVP_PKEY_get_raw_public_key(k,pub.data(),&n)==1&&n==32,"key");EVP_PKEY_free(k);return pub;}
void sign(const Key& seed,const uint8_t* message,size_t size,uint8_t signature[64]){auto key=EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519,nullptr,seed.data(),32);auto ctx=EVP_MD_CTX_new();size_t n=64;auto ok=key&&ctx&&EVP_DigestSignInit(ctx,nullptr,nullptr,nullptr,key)==1&&EVP_DigestSign(ctx,signature,&n,message,size)==1&&n==64;EVP_MD_CTX_free(ctx);EVP_PKEY_free(key);check(ok,"sign");}
void put(uint8_t* bytes,uint64_t value,unsigned size){for(unsigned i=0;i<size;++i)bytes[size-1-i]=uint8_t(value>>(8*i));}
Message message(Kind kind,const Json& value){auto text=value.dump();return {kind,{text.begin(),text.end()}};}
struct Fixture {
 Key siteSeed{},hostSeed{},controllerSeed{},siteKey{},hostKey{},controllerKey{},binding{};Bytes wire;Json authorization;std::mutex mutex;
 std::atomic_bool active=true,serverActive=true,accepted=false;std::atomic_uint connections=0,frames=0,inputCalls=0,deletes=0,renewalCalls=0;
 std::string hostStatus="ready";bool denied=false;uint32_t caps=1,advertised=0,requested=0,optionCaps=3;bool exerciseInput=false,invalidInput=false;std::atomic_bool inputVerified=false;
 bool wrongDevice=false,badProof=false,cancelOnIssue=false,missingClock=false,exerciseRenewal=false,slowControl=false,stalledControl=false;int64_t serverOffsetMS=0;uint16_t port=0;std::thread server;std::atomic_bool proofVerified=false;
 Fixture(){siteSeed.fill(1);hostSeed.fill(2);controllerSeed.fill(3);siteKey=publicKey(siteSeed);hostKey=publicKey(hostSeed);controllerKey=publicKey(controllerSeed);
  std::string site="site",user="user",controller="controller",host="host";ng_remote_ticket_binding fields{};fields.struct_size=sizeof(fields);fields.abi_version=NG_REMOTE_ABI;
  auto id=[](const std::string& s){return ng_remote_ticket_id{reinterpret_cast<const uint8_t*>(s.data()),s.size()};};fields.site_id=id(site);fields.user_id=id(user);fields.controller_id=id(controller);fields.host_id=id(host);std::copy(siteKey.begin(),siteKey.end(),fields.site_key);std::copy(hostKey.begin(),hostKey.end(),fields.host_key);std::copy(controllerKey.begin(),controllerKey.end(),fields.controller_key);std::fill(std::begin(fields.host_boot),std::end(fields.host_boot),4);check(ng_remote_ticket_binding_digest(&fields,binding.data())==0,"binding");
 }
 void issue(uint64_t generation,uint64_t sequence){
  wire.assign(168,0);std::memcpy(wire.data(),"NGRMTK01",8);std::copy(binding.begin(),binding.end(),wire.begin()+8);std::fill(wire.begin()+40,wire.begin()+56,5);put(wire.data()+56,generation,8);put(wire.data()+64,1,8);put(wire.data()+72,sequence,8);
  auto now=uint64_t(std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count()+serverOffsetMS);put(wire.data()+80,now,8);put(wire.data()+88,now+30000,8);put(wire.data()+96,caps,4);put(wire.data()+100,1,4);sign(siteSeed,wire.data(),104,wire.data()+104);
  uint8_t boot[16];std::fill(std::begin(boot),std::end(boot),4);uint8_t session[16];std::fill(std::begin(session),std::end(session),5);
  authorization={{"session",{{"id",encode(session,16)},{"user_id","user"},{"controller_id","controller"},{"host_id","host"},{"host_boot",encode(boot,16)},{"binding",encode(binding.data(),32)},{"generation",generation},{"sequence",sequence},{"state",accepted?"active":"issued"},{"capabilities",caps}}},{"ticket",encode(wire.data(),wire.size())},{"site_key",encode(siteKey.data(),32)},{"host_key",encode(hostKey.data(),32)},{"controller_key",encode(controllerKey.data(),32)},{"endpoint","127.0.0.1:"+std::to_string(port)}};
  authorization["server_unix_ms"]=now;
  if(missingClock)authorization.erase("server_unix_ms");
 }
 void listen(){
  auto listener=std::make_shared<netgo::remote::Listener>("127.0.0.1",0,hostSeed,[this]{return serverActive.load();});port=listener->port();
  server=std::thread([this,listener]{try{auto connection=listener->accept(3000);if(!connection)return;++connections;Message hello;check(connection->receive(hello,1000)&&hello.kind==Kind::hello,"hello");Key challenge{};check(RAND_bytes(challenge.data(),32)==1,"challenge");connection->send({Kind::challenge,{challenge.begin(),challenge.end()}});Message proof;check(connection->receive(proof,1000)&&proof.kind==Kind::proof,"proof");Bytes ticket;{std::lock_guard lock(mutex);ticket=wire;}auto channel=connection->exporter();if(proof.body.size()!=64||ng_remote_possession_verify(controllerKey.data(),ticket.data(),ticket.size(),challenge.data(),channel.data(),proof.body.data())!=0)return;
   proofVerified=true;accepted=true;connection->send(message(Kind::accepted,{{"width",640},{"height",360},{"fps",15},{"capabilities",caps}}));connection->send({Kind::video,{0,0,0,1,9,16}});
   while(serverActive&&active){Message incoming;if(!connection->receive(incoming,100))continue;if(incoming.kind==Kind::renew&&exerciseRenewal)connection->send({Kind::video,{0,0,0,1,9,16}});if(incoming.kind==Kind::input){auto batch=Json::parse(incoming.body);check(!invalidInput&&batch.size()==2&&batch[0].at("down")==true&&batch[1].at("down")==false,"input batch order");inputVerified=true;active=false;}}
  }catch(...){}});
 }
 ~Fixture(){active=false;serverActive=false;if(server.joinable())server.join();}
 ng_remote_client_options options(){
  ng_remote_client_options o{};o.struct_size=sizeof(o);o.abi_version=NG_REMOTE_CLIENT_ABI;o.site_id="site";o.host_id="host";o.capabilities=optionCaps;o.context=this;
  o.active=[](void* p)->int32_t{return static_cast<Fixture*>(p)->active?1:0;};
  o.control=[](void* p,const char* method,const char* path,const char* body,const char*,char* output,size_t capacity,size_t* length)->int32_t{try{
   auto& f=*static_cast<Fixture*>(p);
   if(std::string(path).find("/renew")!=std::string::npos)++f.renewalCalls;
   if(f.slowControl&&std::string(path).rfind("/netgo/v2/remote/sessions/",0)==0&&std::string(method)!="DELETE")std::this_thread::sleep_for(std::chrono::milliseconds(f.stalledControl?7000:1800));
   std::lock_guard lock(f.mutex);Json value;
   if(std::string(method)=="CONTEXT")value={{"site_id","site"},{"user_id","user"},{"device_id",f.wrongDevice?"other":"controller"},{"public_key",encode(f.controllerKey.data(),32)}};
   else if(std::string(path)=="/netgo/v2/remote")value={{"next_generation",uint64_t(17)},{"hosts",Json::array({{{"id","host"},{"enabled",true},{"capabilities",3},{"status",f.hostStatus},{"available_capabilities",f.denied?0:f.advertised?f.advertised:f.caps}}})},{"grants",Json::array({{{"host_id","host"},{"user_id","user"},{"capabilities",f.caps}}})}};
   else if(std::string(path)=="/netgo/v2/remote/key")value={{"public_key",encode(f.siteKey.data(),32)}};
   else if(std::string(path)=="/netgo/v2/remote/sessions"){auto request=Json::parse(body);check(request.at("capabilities")== (f.requested?f.requested:f.caps),"view-only negotiation");check(request.at("generation")==17,"server generation ignored");f.issue(request.at("generation"),1);value=f.authorization;if(f.cancelOnIssue)f.active=false;}
   else if(std::string(method)=="DELETE"){++f.deletes;value={{"revoked",true}};}
   else {if(std::string(path).find("/renew")!=std::string::npos)f.issue(f.authorization.at("session").at("generation"),f.authorization.at("session").at("sequence").get<uint64_t>()+1);else f.authorization["session"]["state"]=f.accepted?"active":"issued";value=f.authorization;}
   auto text=value.dump();if(text.size()>capacity)return -1;std::copy(text.begin(),text.end(),output);*length=text.size();return 0;
  }catch(...){return -1;}};
  o.sign=[](void* p,const uint8_t* bytes,size_t size,uint8_t output[64])->int32_t{try{auto& f=*static_cast<Fixture*>(p);sign(f.badProof?f.hostSeed:f.controllerSeed,bytes,size,output);return 0;}catch(...){return -1;}};
  o.input=[](void* p,char* output,size_t,size_t* length)->int32_t{auto& f=*static_cast<Fixture*>(p);auto count=++f.inputCalls;auto text=count==1?Json::array({{{"type","key"},{"code",65},{"down",true}},{{"type","key"},{"code",f.invalidInput?0:65},{"down",false}}}).dump():"[]";std::memcpy(output,text.data(),text.size());*length=text.size();return 0;};
  o.format=[](void* p,uint32_t w,uint32_t h,uint32_t fps,uint32_t caps)->int32_t{return w==640&&h==360&&fps==15&&caps==static_cast<Fixture*>(p)->caps?0:-1;};
  o.video=[](void* p,const uint8_t*,size_t size,int64_t)->int32_t{auto& f=*static_cast<Fixture*>(p);if(size!=6)return -1;auto count=++f.frames;if(f.exerciseRenewal&&count==1){std::lock_guard lock(f.mutex);f.serverOffsetMS+=12000;}else if(!f.exerciseInput&&(!f.slowControl||count>=3))f.active=false;return 0;};return o;
 }
};
}
int main(){try{
#ifdef _WIN32
 WSADATA ws{};check(WSAStartup(MAKEWORD(2,2),&ws)==0,"winsock");
#endif
 for(int scenario=0;scenario<18;++scenario){Fixture f;f.exerciseInput=scenario==8||scenario==9;f.hostStatus=scenario==10?"offline":scenario==11?"busy":"ready";f.denied=scenario==12;f.invalidInput=scenario==9;f.caps=f.exerciseInput||scenario==14||scenario==15?3:1;if(scenario>=13&&scenario<=15){f.advertised=scenario==15?1:3;f.requested=1;f.optionCaps=scenario==15?3:1;}f.wrongDevice=scenario==1;f.badProof=scenario==2;f.cancelOnIssue=scenario==5;f.missingClock=scenario==6;f.exerciseRenewal=scenario==7||scenario>=16;f.slowControl=scenario>=16;f.stalledControl=scenario==17;f.serverOffsetMS=scenario==3?86400000:scenario==4?-86400000:0;f.listen();auto options=f.options();ng_remote_client* client=nullptr;check(ng_remote_client_create(&options,&client)==0,"create");const auto started=std::chrono::steady_clock::now();auto result=ng_remote_client_run(client);const auto elapsed=std::chrono::steady_clock::now()-started;ng_remote_client_cancel(client);ng_remote_client_destroy(client);
  if(scenario==0||scenario==3||scenario==4||scenario==13)check(result==0&&f.proofVerified&&f.frames==1&&f.inputCalls==0,"authenticated view-only session and clock offset");
  else if(scenario==7||scenario==16)check(result==0&&f.frames==(f.slowControl?3u:2u)&&f.inputCalls==0,"authenticated renewal clock recalibration / slow control continuity");
  else if(scenario==17)check(f.frames==1&&f.renewalCalls==0&&elapsed>=std::chrono::seconds(6)&&elapsed<std::chrono::seconds(12),"unreachable control must still close without an extra renewal");
  else if(scenario==5)check(result==0&&f.frames==0&&!f.proofVerified,"cancellation reported as failure");
  else if(scenario==8)check(result==0&&f.inputVerified,"batched input not delivered");
  else if(scenario==9)check(result==NG_REMOTE_CLIENT_FAILED&&!f.inputVerified,"invalid batch partially delivered");
  else check(result==NG_REMOTE_CLIENT_FAILED&&f.frames==0&&!f.proofVerified,"unauthorized media");
  if(scenario==1||scenario==5||scenario==6)check(f.connections==0,"invalid/cancelled client reached transport");
  check(f.deletes==(scenario>=10&&scenario<=12?0u:1u),"issued session cleanup or unavailable host allocation");
  if((scenario>=10&&scenario<=12)||scenario==14||scenario==15)check(f.connections==0,"unavailable host or excess capabilities reached transport");
 }
 {auto now=netgo::remote::monotonic_time_ms();check(now>=31000,"test requires 31s uptime");netgo::remote::ServerClock delayed(100000,now-31000);check(delayed.now()>=131000,"slow response extended lease");}
 ng_remote_client* invalid=nullptr;check(ng_remote_client_create(nullptr,&invalid)==NG_REMOTE_CLIENT_INVALID&&invalid==nullptr,"ABI validation");std::cout<<"PASS shared client TLS pin, possession, view-only negotiation, cancellation and unauthorized media rejection\n";return 0;
 }catch(const std::exception& e){std::cerr<<"FAIL "<<e.what()<<'\n';return 1;}}
