#include "connection.hpp"
#include <openssl/ssl.h>
#include <wincrypt.h>
#include <iphlpapi.h>
#undef X509_NAME
#include <openssl/pem.h>
#include <openssl/x509v3.h>
#include <algorithm>
#include <cctype>
#include <regex>
#include <sstream>
#include <condition_variable>
#include <bit>

namespace netgo {
struct Transport::Link {
 std::mutex mutex;
 SOCKET socket=INVALID_SOCKET;
 std::unique_ptr<SSL,decltype(&SSL_free)> tls{nullptr,SSL_free};
 Clock::time_point used{};
 void reset(){tls.reset();if(socket!=INVALID_SOCKET){closesocket(socket);socket=INVALID_SOCKET;}}
 ~Link(){reset();}
};
static std::string lower(std::string s){for(char& c:s)c=char(std::tolower(static_cast<unsigned char>(c)));return s;}
static bool protectControlSocket(SOCKET socket,const sockaddr* target,const std::string& gatewayIP){
 sockaddr_in gateway{};gateway.sin_family=AF_INET;
 if(InetPtonA(AF_INET,gatewayIP.c_str(),&gateway.sin_addr)!=1)return true;
 DWORD vpn=0,current=0;
 if(GetBestInterfaceEx(reinterpret_cast<sockaddr*>(&gateway),&vpn)!=NO_ERROR||GetBestInterfaceEx(const_cast<sockaddr*>(target),&current)!=NO_ERROR||current!=vpn)return true;
 MIB_IPFORWARD_TABLE2* raw=nullptr;
 if(GetIpForwardTable2(target->sa_family,&raw)!=NO_ERROR)return false;
 std::unique_ptr<MIB_IPFORWARD_TABLE2,decltype(&FreeMibTable)> routes(raw,FreeMibTable);
 DWORD selected=0;int longest=-1;uint64_t cost=UINT64_MAX;
 for(ULONG i=0;i<raw->NumEntries;++i){const auto& route=raw->Table[i];int bits=route.DestinationPrefix.PrefixLength;
  if(route.InterfaceIndex==vpn||route.DestinationPrefix.Prefix.si_family!=target->sa_family)continue;
  bool matches=false;
  if(target->sa_family==AF_INET&&bits<=32){auto ip=ntohl(reinterpret_cast<const sockaddr_in*>(target)->sin_addr.s_addr),prefix=ntohl(route.DestinationPrefix.Prefix.Ipv4.sin_addr.s_addr);auto mask=bits?UINT32_MAX<<(32-bits):0;matches=(ip&mask)==(prefix&mask);}
  if(target->sa_family==AF_INET6&&bits<=128){auto ip=reinterpret_cast<const uint8_t*>(&reinterpret_cast<const sockaddr_in6*>(target)->sin6_addr),prefix=reinterpret_cast<const uint8_t*>(&route.DestinationPrefix.Prefix.Ipv6.sin6_addr);matches=std::equal(ip,ip+bits/8,prefix);if(matches&&bits%8){auto mask=uint8_t(0xff<<(8-bits%8));matches=(ip[bits/8]&mask)==(prefix[bits/8]&mask);}}
  if(!matches)continue;
  MIB_IF_ROW2 iface{};iface.InterfaceLuid=route.InterfaceLuid;if(GetIfEntry2(&iface)!=NO_ERROR||iface.OperStatus!=IfOperStatusUp)continue;
  MIB_IPINTERFACE_ROW metric{};InitializeIpInterfaceEntry(&metric);metric.Family=target->sa_family;metric.InterfaceLuid=route.InterfaceLuid;
  uint64_t total=route.Metric;if(GetIpInterfaceEntry(&metric)==NO_ERROR)total+=metric.Metric;
  if(bits>longest||(bits==longest&&total<cost)){selected=route.InterfaceIndex;longest=bits;cost=total;}
 }
 if(!selected)return true; // Before VPN startup there may be no separate VPN interface.
 DWORD index=target->sa_family==AF_INET?htonl(selected):selected;
 return setsockopt(socket,target->sa_family==AF_INET?IPPROTO_IP:IPPROTO_IPV6,target->sa_family==AF_INET?IP_UNICAST_IF:IPV6_UNICAST_IF,reinterpret_cast<const char*>(&index),sizeof(index))==0;
}
static void windowsRoots(X509_STORE* store) {
 for(DWORD location:{DWORD(CERT_SYSTEM_STORE_CURRENT_USER),DWORD(CERT_SYSTEM_STORE_LOCAL_MACHINE)}) {
  HCERTSTORE roots=CertOpenStore(CERT_STORE_PROV_SYSTEM_W,0,0,location|CERT_STORE_READONLY_FLAG,L"ROOT");
  if(!roots)continue;
  PCCERT_CONTEXT c=nullptr;
  while((c=CertEnumCertificatesInStore(roots,c))!=nullptr){
   const unsigned char* data=c->pbCertEncoded;X509* x=d2i_X509(nullptr,&data,c->cbCertEncoded);
   if(x){X509_STORE_add_cert(store,x);X509_free(x);}
  }CertCloseStore(roots,0);
 }
}
Transport::Transport(Json p,std::chrono::milliseconds cooldown):profile_(std::move(p)),context_(SSL_CTX_new(TLS_client_method()),SSL_CTX_free),cooldown_(cooldown) {
 validateProfile(profile_);if(!context_)throw Error("tls_initialization_failed");
 for(size_t i=0;i<profile_["Candidates"].size();++i)links_.push_back(std::make_shared<Link>());
 SSL_CTX_set_min_proto_version(context_.get(),TLS1_2_VERSION);SSL_CTX_set_verify(context_.get(),SSL_VERIFY_PEER,nullptr);
 SSL_CTX_set_purpose(context_.get(),X509_PURPOSE_SSL_SERVER);auto store=SSL_CTX_get_cert_store(context_.get());windowsRoots(store);
 auto ca=profile_.value("CA","");
 if(!ca.empty()){
  std::unique_ptr<BIO,decltype(&BIO_free)> bio(BIO_new_mem_buf(ca.data(),int(ca.size())),BIO_free);
  X509* x=nullptr;int count=0;
  while((x=PEM_read_bio_X509(bio.get(),nullptr,nullptr,nullptr))!=nullptr){X509_STORE_add_cert(store,x);X509_free(x);++count;}
  if(!count)throw Error("invalid_ca");
 }
}
Json Transport::requestAt(size_t index,const std::string& method,const std::string& path,const std::string& body,const std::string& headers) {
 auto deadline=Clock::now()+std::chrono::seconds(path=="/netgo/v2/health"?3:20);
 auto& link=*links_.at(index);
 std::lock_guard serial(link.mutex);
 struct Failure { Link& link; bool reusable=false; ~Failure(){if(!reusable)link.reset();} } failure{link};
 if(Clock::now()-link.used>std::chrono::seconds(30))link.reset();
 auto waitTLS=[&](int error){
  if(error!=SSL_ERROR_WANT_READ&&error!=SSL_ERROR_WANT_WRITE)throw Error("control_tls_failed");
  auto left=std::chrono::duration_cast<std::chrono::microseconds>(deadline-Clock::now()).count();
  if(left<=0)throw Error("control_timeout");
  fd_set set;FD_ZERO(&set);FD_SET(link.socket,&set);timeval wait{long(left/1000000),long(left%1000000)};
  if(::select(0,error==SSL_ERROR_WANT_READ?&set:nullptr,error==SSL_ERROR_WANT_WRITE?&set:nullptr,nullptr,&wait)<=0)throw Error("control_timeout");
 };
 if(!link.tls){
 auto target=address(profile_["Candidates"].at(index).at("Address"));
 ADDRINFOEXW hints{},*list=nullptr;hints.ai_family=AF_UNSPEC;hints.ai_socktype=SOCK_STREAM;hints.ai_protocol=IPPROTO_TCP;
 Handle completion(CreateEventW(nullptr,TRUE,FALSE,nullptr));if(!completion.value)throw Error("control_dns_failed");
 OVERLAPPED operation{};operation.hEvent=completion;HANDLE query=nullptr;
 using CancelQuery=INT (WSAAPI *)(LPHANDLE);using QueryResult=INT (WSAAPI *)(LPOVERLAPPED);
 auto winsock=GetModuleHandleW(L"ws2_32.dll");
 auto cancelQuery=std::bit_cast<CancelQuery>(GetProcAddress(winsock,"GetAddrInfoExCancel"));
 auto queryResult=std::bit_cast<QueryResult>(GetProcAddress(winsock,"GetAddrInfoExOverlappedResult"));
 if(!cancelQuery||!queryResult)throw Error("control_dns_failed");
 auto dnsHost=wide(target.host),dnsPort=wide(std::to_string(target.port));
 int resolved=GetAddrInfoExW(dnsHost.c_str(),dnsPort.c_str(),NS_DNS,nullptr,&hints,&list,nullptr,&operation,nullptr,&query);
 if(resolved==WSA_IO_PENDING){
  if(WaitForSingleObject(completion,2000)!=WAIT_OBJECT_0){cancelQuery(&query);WaitForSingleObject(completion,INFINITE);resolved=WSAETIMEDOUT;}
  else resolved=queryResult(&operation);
 }
 std::unique_ptr<ADDRINFOEXW,decltype(&FreeAddrInfoExW)> addrs(list,FreeAddrInfoExW);
 if(resolved!=0)throw Error("control_dns_failed");
 SOCKET raw=INVALID_SOCKET;
 for(auto a=list;a;a=a->ai_next) {
  SOCKET candidate=socket(a->ai_family,a->ai_socktype,a->ai_protocol);if(candidate==INVALID_SOCKET)continue;
  if(!protectControlSocket(candidate,a->ai_addr,profile_.value("GatewayIP",""))){closesocket(candidate);continue;}
  u_long nonblock=1;ioctlsocket(candidate,FIONBIO,&nonblock);int e=::connect(candidate,a->ai_addr,int(a->ai_addrlen));
  if(e==SOCKET_ERROR&&WSAGetLastError()!=WSAEWOULDBLOCK){closesocket(candidate);continue;}
  auto remaining=std::chrono::duration_cast<std::chrono::microseconds>(deadline-Clock::now()).count();
  if(remaining<=0){closesocket(candidate);break;}
  fd_set write,except;FD_ZERO(&write);FD_ZERO(&except);FD_SET(candidate,&write);FD_SET(candidate,&except);timeval wait{long(remaining/1000000),long(remaining%1000000)};
  if(::select(0,nullptr,&write,&except,&wait)>0&&!FD_ISSET(candidate,&except)){
   int error=0,len=sizeof(error);getsockopt(candidate,SOL_SOCKET,SO_ERROR,reinterpret_cast<char*>(&error),&len);
   if(!error){raw=candidate;break;}
  }closesocket(candidate);
 }
 if(raw==INVALID_SOCKET)throw Error("control_unreachable");
 link.socket=raw;
 std::unique_ptr<SSL,decltype(&SSL_free)> tls(SSL_new(context_.get()),SSL_free);auto host=originHost(profile_.at("Origin"));
 if(!tls||SSL_set_fd(tls.get(),int(raw))!=1||SSL_set_tlsext_host_name(tls.get(),host.c_str())!=1)throw Error("tls_initialization_failed");
 in_addr v4{};in6_addr v6{};bool ip=InetPtonA(AF_INET,host.c_str(),&v4)==1||InetPtonA(AF_INET6,host.c_str(),&v6)==1;
 auto param=SSL_get0_param(tls.get());X509_VERIFY_PARAM_set_hostflags(param,X509_CHECK_FLAG_NO_PARTIAL_WILDCARDS);
 if((ip?X509_VERIFY_PARAM_set1_ip_asc(param,host.c_str()):X509_VERIFY_PARAM_set1_host(param,host.c_str(),0))!=1)throw Error("tls_identity_failed");
 link.tls=std::move(tls);
 for(;;){int result=SSL_connect(link.tls.get());if(result==1)break;waitTLS(SSL_get_error(link.tls.get(),result));}
 if(SSL_get_verify_result(link.tls.get())!=X509_V_OK)throw Error("tls_certificate_failed");
 }
 auto* tls=link.tls.get();
 std::string authority=profile_.at("Origin").get<std::string>().substr(8);if(authority.back()=='/')authority.pop_back();
 std::string req=method+" "+path+" HTTP/1.1\r\nHost: "+authority+"\r\nConnection: keep-alive\r\nAccept: application/json\r\nAccept-Encoding: identity\r\nContent-Type: application/json\r\n"+headers+"Content-Length: "+std::to_string(body.size())+"\r\n\r\n"+body;
 size_t pos=0;
 try {
  while(pos<req.size()){
   int result=SSL_write(tls,req.data()+pos,int(req.size()-pos));
   if(result>0)pos+=size_t(result);else waitTLS(SSL_get_error(tls,result));
  }
 }catch(...){wipe(req);throw;}
 wipe(req);
 std::string rawResponse;char buffer[8192];bool framed=false,closed=false;
 while(Clock::now()<deadline){
  int count=SSL_read(tls,buffer,sizeof(buffer));
  if(count<=0){
   int error=SSL_get_error(tls,count);
   if(error==SSL_ERROR_ZERO_RETURN||(error==SSL_ERROR_SYSCALL&&count==0)){closed=true;break;}
   waitTLS(error);continue;
  }
  rawResponse.append(buffer,count);
  if(rawResponse.size()>4*maxJson+32768)throw Error("http_response_too_large");
  auto end=rawResponse.find("\r\n\r\n");
  if(end!=std::string::npos){
   auto header=lower(rawResponse.substr(0,end));
   if(header.find("content-length:")!=std::string::npos||header.find("transfer-encoding:")!=std::string::npos){
    try{decodeHttp(rawResponse);framed=true;break;}catch(const std::exception&){}
   }
  }
 }
 if(!framed&&!closed)throw Error("control_timeout");
 auto response=decodeHttp(rawResponse);
 auto header=lower(rawResponse.substr(0,rawResponse.find("\r\n\r\n")));
 failure.reusable=framed&&!closed&&header.starts_with("http/1.1")&&header.find("connection: close")==std::string::npos;
 link.used=Clock::now();
 if(response.status<200||response.status>=300) {
  auto v=Json::parse(response.body,nullptr,false);std::string e=v.is_object()?v.value("error","remote_rejected"):"remote_rejected";
  if(!std::regex_match(e,std::regex(R"(^[a-zA-Z0-9_]{1,80}$)")))e="remote_rejected";
  throw Error(e);
 }
 if(response.body.empty())return Json::object();
 return Json::parse(response.body);
}
bool Transport::probe(size_t i) {
 try{auto h=requestAt(i,"GET","/netgo/v2/health","","");validateHealth(h,profile_.at("ID"));
  {std::lock_guard lock(clockMutex_);clocks_.resize(profile_["Candidates"].size());clocks_[i]={std::chrono::system_clock::time_point(std::chrono::seconds(h["server_unix"].get<int64_t>())),Clock::now(),true};}
  return true;}
 catch(const std::exception& e){if(std::string(e.what())=="protocol_incompatible")incompatible_=true;return false;}
}
void Transport::select() {
 incompatible_=false;
 std::mutex mutex;std::condition_variable ready;size_t winner=SIZE_MAX,finished=0;
 std::vector<std::thread> probes;
 for(size_t i=0;i<profile_["Candidates"].size();++i)probes.emplace_back([&,i]{
  if(profile_["Candidates"][i]["Kind"]=="auxiliary"){
   std::unique_lock lock(mutex);if(ready.wait_for(lock,std::chrono::milliseconds(800),[&]{return winner!=SIZE_MAX;})){++finished;ready.notify_all();return;}
  }
  bool success=probe(i);
  {std::lock_guard lock(mutex);if(success&&winner==SIZE_MAX)winner=i;++finished;}ready.notify_all();
 });
 {std::unique_lock lock(mutex);ready.wait_for(lock,std::chrono::seconds(10),[&]{return winner!=SIZE_MAX||finished==probes.size();});}
 for(auto& thread:probes)thread.join();
 if(winner==SIZE_MAX)throw Error(incompatible_?"protocol_incompatible":"no_trusted_control_entry");
 selected_=winner;switched_=Clock::now();directSuccess_=0;directCandidate_=SIZE_MAX;
}
void Transport::recover() {
 if(!probe(selected_)){select();directSuccess_=0;return;}
 if(kind()=="auxiliary"&&Clock::now()-switched_>=cooldown_){
  std::vector<unsigned char> healthy(profile_["Candidates"].size(),0);std::vector<std::thread> probes;
  for(size_t i=0;i<healthy.size();++i)if(profile_["Candidates"][i]["Kind"]=="direct")probes.emplace_back([&,i]{healthy[i]=probe(i)?1:0;});
  for(auto& thread:probes)thread.join();
  size_t chosen=SIZE_MAX;
  for(size_t i=0;i<healthy.size();++i)if(healthy[i]&&(chosen==SIZE_MAX||i==directCandidate_))chosen=i;
  if(chosen==SIZE_MAX){directSuccess_=0;directCandidate_=SIZE_MAX;return;}
  if(directCandidate_!=chosen){directSuccess_=0;directCandidate_=chosen;}
  if(++directSuccess_>=3){selected_=chosen;switched_=Clock::now();directSuccess_=0;directCandidate_=SIZE_MAX;}
 }
}
std::string Transport::kind() const{return profile_["Candidates"][selected_.load()]["Kind"];}
std::string Transport::candidate() const{return profile_["Candidates"][selected_]["ID"];}
std::chrono::system_clock::time_point Transport::now() const {
 std::lock_guard lock(clockMutex_);
 auto selected=selected_.load();
 if(selected>=clocks_.size()||!clocks_[selected].valid)return std::chrono::system_clock::now();
 const auto& sample=clocks_[selected];return sample.server+std::chrono::duration_cast<std::chrono::system_clock::duration>(Clock::now()-sample.received);
}
Json Transport::call(const std::string& method,const std::string& path,const Json& value,const std::string& seed,const std::string& token,const std::string& idem) {
 std::string body=value.is_null()?"":value.dump(),headers="X-NetGo-Protocol: 2\r\n";
 if(!idem.empty())headers+="Idempotency-Key: "+idem+"\r\n";
 if(!token.empty()){
  auto stamp=std::to_string(std::chrono::duration_cast<std::chrono::seconds>(now().time_since_epoch()).count()),nonce=randomID();
  headers+="Authorization: Bearer "+token+"\r\nX-NetGo-Time: "+stamp+"\r\nX-NetGo-Nonce: "+nonce+"\r\nX-NetGo-Signature: "+signature(seed,signedMessage(method,path,stamp,nonce,body,token))+"\r\n";
 }
 try{auto result=requestAt(selected_,method,path,body,headers);wipe(body);wipe(headers);return result;}catch(...){wipe(body);wipe(headers);throw;}
}
}
