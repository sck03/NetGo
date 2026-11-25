#include "connection.hpp"
#include <iostream>
#include <openssl/evp.h>
#include <sddl.h>
#include <future>
using namespace netgo;
void require(bool b,const char* message){if(!b)throw Error(message);}
template<class F>void rejects(F f,const char* message){bool failed=false;try{f();}catch(...){failed=true;}require(failed,message);}
Json profile(){return {{"Protocol",2},{"ID","test"},{"Name","实验站点"},{"Origin","https://site.example"},{"Candidates",Json::array({{{"ID","direct"},{"Address","127.0.0.1:443"},{"Kind","direct"}}})},{"CA",""},{"GatewayIP","100.96.0.1"},{"GatewayPeerID","gateway"},{"LocalRelay","relay.example"},{"AuxiliaryRelay",""}};}
int main(int argc,char** argv){
 try{
  require(OpenSSL_version_num()==OPENSSL_VERSION_NUMBER,"OpenSSL header/library version mismatch");
  std::cout<<"Linked "<<OpenSSL_version(OPENSSL_VERSION)<<'\n';
  WSADATA wsa{};WSAStartup(MAKEWORD(2,2),&wsa);
  if(argc==3&&std::string(argv[1])=="--profile-contract"){
   auto fixture=Json::parse(readFile(wide(argv[2])));
   for(const auto& test:fixture.at("cases")){
    auto old=fixture.at("base"),next=old;old.update(test.at("old"));next.update(test.at("next"));
    bool accepted=true;try{validateProfileUpdate(old,next);}catch(...){accepted=false;}
    if(accepted!=test.at("accept").get<bool>())throw Error("profile contract: "+test.at("name").get<std::string>());
   }
   std::cout<<"PASS shared profile revision, rollback, identity and WAN contract\n";return 0;
  }
  if(argc==3&&std::string(argv[1])=="--tls-multiwan"){
   auto p=Json::parse(readFile(wide(argv[2])));Transport transport(p,std::chrono::milliseconds(0));
   transport.select();require(transport.kind()=="auxiliary","initial fallback not selected");
   std::string seed(32,'a');transport.call("POST","/enable-wan",{{"enable",true}},seed,"fixture-token");
   for(int i=0;i<3;++i){transport.recover();if(i<2)require(transport.kind()=="auxiliary","unstable direct entry accepted");}
   require(transport.kind()=="direct"&&transport.candidate()=="wan2","healthy WAN2 was not selected");
   require(transport.call("POST","/fixture",{{"native",true}},seed,"fixture-token").at("verified")==true,"WAN2 request failed");
   std::cout<<"PASS multi-WAN recovery: unavailable WAN1/WAN3, three WAN2 confirmations and canonical signed request\n";return 0;
  }
  if(argc==3&&(std::string(argv[1])=="--tls"||std::string(argv[1])=="--tls-reject")){
   auto p=Json::parse(readFile(wide(argv[2])));Transport transport(p);
   if(std::string(argv[1])=="--tls-reject"){rejects([&]{transport.select();},"untrusted TLS accepted");std::cout<<"PASS TLS rejection\n";return 0;}
   transport.select();require(transport.kind()=="auxiliary","fallback not selected");
   std::string seed(32,'a');
   auto out=transport.call("POST","/fixture",{{"native",true}},seed,"fixture-token",randomID());
   require(out.at("verified")==true,"native signature rejected");std::cout<<"PASS canonical TLS identity, fallback and signed request\n";return 0;
  }
  if(argc==3&&std::string(argv[1])=="--ipc"){
   auto root=std::filesystem::temp_directory_path()/wide("netgo-ipc-"+randomID());secureDirectory(root);
   testPipe(root);
   auto binary=wide(argv[2]);std::wstring command=L"\""+binary+L"\" --agent --test-root \""+root.wstring()+L"\"";
   STARTUPINFOW si{};si.cb=sizeof(si);PROCESS_INFORMATION pi{};
   require(CreateProcessW(binary.c_str(),command.data(),nullptr,nullptr,FALSE,CREATE_NO_WINDOW,nullptr,nullptr,&si,&pi),"agent did not start");
   Handle process(pi.hProcess),thread(pi.hThread);bool available=false;
   for(int i=0;i<40;++i){try{auto s=pipeCall({{"action","status"}});require(s.at("state")=="disconnected","unexpected state");available=true;break;}catch(...){Sleep(100);}}
   require(available,"native IPC unavailable");
   {
    // A stalled local sender must not monopolize status and disconnect IPC.
    require(WaitNamedPipeW(pipeName().c_str(),1000),"IPC stall test unavailable");
    Handle stalled(CreateFileW(pipeName().c_str(),GENERIC_READ|GENERIC_WRITE,0,nullptr,OPEN_EXISTING,0,nullptr));
    require(stalled.value!=INVALID_HANDLE_VALUE,"IPC stall test open");
    DWORD count=1024,written=0;require(WriteFile(stalled,&count,4,&written,nullptr),"IPC stall header");
    auto pending=std::async(std::launch::async,[]{return pipeCall({{"action","status"}});});
    require(pending.wait_for(std::chrono::seconds(2))==std::future_status::ready,"stalled sender blocked status");
    require(pending.get().at("state")=="disconnected","concurrent status failed");
   }
   auto p=profile();pipeCall({{"action","import"},{"profile",p}});
   require(pipeCall({{"action","profiles"}}).size()==1,"profile IPC roundtrip");
   rejects([]{pipeCall({{"action","run"},{"command","cmd.exe"}});},"arbitrary command accepted");
   {
    auto limit=Clock::now()+std::chrono::seconds(3);
    while(!WaitNamedPipeW(pipeName().c_str(),100)){require(Clock::now()<limit,"IPC bound test could not connect");Sleep(25);}
    Handle pipe(CreateFileW(pipeName().c_str(),GENERIC_READ|GENERIC_WRITE,0,nullptr,OPEN_EXISTING,0,nullptr));
    require(pipe.value!=INVALID_HANDLE_VALUE,"IPC bound test open");
    DWORD oversize=0xffffffff,written=0;
    require(WriteFile(pipe,&oversize,4,&written,nullptr),"IPC bound test write");
   }
   require(pipeCall({{"action","status"}}).at("state")=="disconnected","oversized frame blocked agent");
   rejects([]{pipeCall({{"action","delete"},{"site","../outside"}});},"path traversal accepted");
   rejects([]{pipeCall({{"action","import"},{"profile",{{"Password","must-not-save"}}}});},"secret config accepted");
   p["Candidates"][0]["Address"]="127.0.0.1:1";
   pipeCall({{"action","import"},{"profile",p}});
   pipeCall({{"action","connect"},{"site","test"},{"alias","fixture"},{"password","fixture-password"}});
   Json connection;
   for(int i=0;i<50;++i){connection=pipeCall({{"action","status"}});if(connection.at("state")=="failed")break;Sleep(100);}
   require(connection.at("state")=="failed"&&!connection.value("error","").empty(),"connection error was lost in IPC");
   require(pipeCall({{"action","diagnostic"}}).at("state")=="failed","diagnostic error confused with IPC failure");
   pipeCall({{"action","disconnect"}});
   pipeCall({{"action","delete"},{"site","test"}});
   pipeCall({{"action","shutdown"}});require(WaitForSingleObject(process,5000)==WAIT_OBJECT_0,"agent shutdown blocked");
   std::filesystem::remove_all(root);std::cout<<"PASS native IPC actions, bounds, path rejection and shutdown\n";return 0;
  }
  auto p=profile();validateProfile(p);require(originHost("https://site.example:8443")=="site.example","origin host");
  auto endpoints=profileEndpoints(p,"192.168.18.220:10443 wan1.example:20443 wan2.example:30443","vps.example:24443");
  require(endpoints.at("Origin")==p.at("Origin")&&endpoints.at("Candidates").size()==4,"entry update changed site identity");
  require(profileEndpoints(endpoints,"192.168.18.220:10443 wan1.example:20443 wan2.example:30443","vps.example:24443")==endpoints,"entry IDs changed without edits");
  auto interleaved=endpoints;std::swap(interleaved["Candidates"][0],interleaved["Candidates"][3]);interleaved["ConfigRevision"]=3;
  auto unchanged=profileEndpoints(interleaved,"wan1.example:20443 wan2.example:30443 192.168.18.220:10443","vps.example:24443");
  require(unchanged==interleaved,"unchanged form reordered imported entries");validateProfileUpdate(interleaved,unchanged);
  rejects([&]{profileEndpoints(p,"192.168.18.220:10443","192.168.18.220:10443");},"duplicate endpoints");
  rejects([&]{profileEndpoints(p,"","vps.example:24443");},"missing direct entry");
  auto health=Json{{"protocol",2},{"site_id","test"},{"capabilities",{"native-auth-v1","device-signature-v1","leases-v1","profile-revision-v1"}}};
  validateHealth(health,"test");
  auto timed=health;timed["server_unix"]=int64_t(std::time(nullptr));validateHealth(timed,"test");
  timed["server_unix"]=int64_t(std::time(nullptr))+60;
  validateHealth(timed,"test");timed["server_unix"]=-1;rejects([&]{validateHealth(timed,"test");},"invalid server clock accepted");
  auto incompatible=health;incompatible["capabilities"].erase(3);rejects([&]{validateHealth(incompatible,"test");},"missing capability accepted");
  incompatible=health;incompatible["protocol"]=3;rejects([&]{validateHealth(incompatible,"test");},"future protocol accepted");
  rejects([&]{validateHealth(health,"other");},"wrong health identity accepted");
  require(address("[::1]:443").host=="::1","IPv6");
  for(const auto& value:{"site:0","site:65536","https://site:443","site:443\r\nInjected:1","../site:443"})rejects([&]{address(value);},"unsafe candidate");
  auto bad=p;bad["ID"]="../escape";rejects([&]{validateProfile(bad);},"unsafe site ID");
  bad=p;bad["Password"]="secret";rejects([&]{validateProfile(bad);},"config stores password");
  bad=p;bad["Candidates"][0]["Kind"]="auxiliary";rejects([&]{validateProfile(bad);},"no local direct entry");
  bad=p;bad["CA"]="garbage";rejects([&]{validateProfile(bad);},"invalid CA");
  require(sha256("abc")=="ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad","SHA256 fixture");
  require(sha256("abc",true)=="ungWv48Bz-pBQUDeXa4iI7ADYaOWF3qctBD_YfIAFa0","PKCE fixture");
  require(sha256("")=="e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","empty SHA256 fixture");
  require(accountScope("site"," ALICE \t")==accountScope("site","alice"),"account normalization");
  require(accountScope("site","alice")!=accountScope("site","bob")&&accountScope("site","alice")!=accountScope("another","alice"),"account identity isolation");
  const unsigned char binary[]={0xfb,0xff,0xff};
  require(base64url(binary,0).empty()&&base64url(binary,1)=="-w"&&base64url(binary,2)=="-_8"&&base64url(binary,3)=="-___","binary URL-safe encoding and padding");
  auto random=randomID();require(random.size()==43&&random.find_first_not_of("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")==std::string::npos&&random!=randomID(),"OS random ID format");
  require(parseTime("2026-11-22T08:00:00+08:00")==parseTime("2026-11-22T00:00:00Z"),"RFC3339 offset");
  rejects([]{parseTime("2026-02-31T00:00:00Z");},"invalid date accepted");
  // RFC8032 test vector: empty message, deterministic Ed25519 seed.
  const std::string seedHex="9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60";
  std::string seed;for(size_t i=0;i<seedHex.size();i+=2)seed.push_back(char(std::stoi(seedHex.substr(i,2),nullptr,16)));
  require(publicKey(seed)=="11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo","Ed25519 public key");
  const auto actual=signature(seed,"");
  const std::string sigHex="e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b";
  std::string sigBytes;for(size_t i=0;i<sigHex.size();i+=2)sigBytes.push_back(char(std::stoi(sigHex.substr(i,2),nullptr,16)));
  require(actual==base64url(reinterpret_cast<const unsigned char*>(sigBytes.data()),sigBytes.size()),"Ed25519 RFC signature");
  auto body=std::string("{}"),message=signedMessage("POST","/netgo/v2/sessions","123","nonce",body,"token");
  require(message=="POST\n/netgo/v2/sessions\n123\nnonce\n"+sha256(body)+"\n"+sha256("token"),"request signing format");
  require(decodeHttp("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}").body=="{}","content length");
  require(decodeHttp("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\n").body=="{}","chunked response");
  rejects([]{decodeHttp("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\n{}");},"truncated body");
  rejects([]{decodeHttp("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n8\r\n{}\r\n0\r\n\r\n");},"truncated chunk");
  for(const auto& header:{"Content-Length: 2junk","Content-Length: +2","Content-Length: 2\r\nContent-Length: 2","Content-Length: 2\r\nTransfer-Encoding: chunked","Transfer-Encoding: notchunked"}){
   rejects([&]{decodeHttp(std::string("HTTP/1.1 200 OK\r\n")+header+"\r\n\r\n{}");},"ambiguous response framing");
  }
  rejects([]{decodeHttp("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n");},"missing chunk terminator");
  require(decodeHttp("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\nX-Trace: done\r\n\r\n").body=="{}","legal chunk trailer");
  auto target="test/"+randomID();credentialWrite(target,"native-fixture");require(credentialRead(target)=="native-fixture","Credential Manager roundtrip");credentialDelete(target);require(credentialRead(target).empty(),"credential deletion");
  auto prefix="test/"+randomID()+"/";credentialWrite(prefix+"a","fixture-a");credentialWrite(prefix+"b","fixture-b");
  require(credentialTargets(prefix).size()==2,"account credential enumeration");for(const auto& name:credentialTargets(prefix))credentialDelete(name);require(credentialTargets(prefix).empty(),"account credential cleanup");
  auto root=std::filesystem::temp_directory_path()/wide("netgo-test-"+randomID());secureDirectory(root);
  {
   Controller c(root);c.importProfile(p);require(c.profiles().size()==1,"profile persistence");
   bad=p;bad["Origin"]="https://other.example";rejects([&]{c.importProfile(bad);},"trust change reused identity");
   require(c.dispatch({{"action","connect"},{"site","../bad"}}).contains("error"),"path traversal");
   require(c.dispatch({{"action","run"}}).contains("error"),"unknown action");
   auto diagnostic=safeDiagnostic({{"token","sensitive"},{"data",{{"ip","sensitive"},{"relay","sensitive"},{"path","direct"}}}},Json::array());
   require(diagnostic.dump().find("sensitive")==std::string::npos,"diagnostic secret leak");
  }
  uint64_t generation=0;
  p["Candidates"][0]["Address"]="127.0.0.1:1";
  {
   Controller c(root);c.importProfile(p);c.connect({{"site","test"},{"alias","fixture"},{"password","fixture-password"}});
   for(int i=0;i<40&&c.status().at("state")!="failed";++i)Sleep(100);
   require(c.status().at("state")=="failed","untrusted endpoint did not stop login");
   generation=c.status().at("generation");c.disconnect();
  }
  {
   Controller c(root);require(c.status().at("generation").get<uint64_t>()>=generation,"restart lost generation");
   c.connect({{"site","test"},{"alias","fixture"},{"password","fixture-password"}});
   require(c.status().at("generation").get<uint64_t>()>generation,"restart reused generation");
   c.disconnect();
  }
  std::filesystem::remove_all(root);WSACleanup();std::cout<<"PASS native profile, TLS parser, crypto, credentials, storage, generation restart and diagnostic checks\n";return 0;
 }catch(const std::exception& e){std::cerr<<"FAIL "<<e.what()<<"\n";return 1;}
}
