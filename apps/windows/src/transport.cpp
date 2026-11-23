#include "connection.hpp"
#include <openssl/ssl.h>
#include <wincrypt.h>
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
static std::string lower(std::string s){for(char& c:s)c=char(std::tolower(static_cast<unsigned char>(c)));return s;}
void validateHealth(const Json& h,const std::string& siteID) {
 if(h.value("site_id","")!=siteID)throw Error("site_identity_mismatch");
 if(h.value("protocol",0)!=2||!h.contains("capabilities")||!h["capabilities"].is_array())throw Error("protocol_incompatible");
 for(const auto& c:{"native-auth-v1","device-signature-v1","leases-v1","profile-revision-v1"}){
  if(std::find(h["capabilities"].begin(),h["capabilities"].end(),Json(c))==h["capabilities"].end())throw Error("protocol_incompatible");
 }
 if(h.contains("server_unix")){
  if(!h["server_unix"].is_number_integer())throw Error("invalid_control_health");
  auto remote=h["server_unix"].get<int64_t>(),local=int64_t(std::time(nullptr));
  if(remote<local-30||remote>local+30)throw Error("clock_out_of_sync");
 }
}
HttpResponse decodeHttp(const std::string& raw) {
 auto end=raw.find("\r\n\r\n");if(end==std::string::npos||end>32768)throw Error("invalid_http_response");
 std::istringstream lines(raw.substr(0,end));std::string version,line;int code=0;lines>>version>>code;std::getline(lines,line);
 if((version!="HTTP/1.1"&&version!="HTTP/1.0")||code<100||code>599)throw Error("invalid_http_response");
 bool chunked=false;size_t length=SIZE_MAX;
 while(std::getline(lines,line)) {
  auto colon=line.find(':');if(colon==std::string::npos||colon==0)throw Error("invalid_http_header");
  auto k=lower(line.substr(0,colon));auto v=lower(line.substr(colon+1));
  auto first=v.find_first_not_of(" \t\r");auto last=v.find_last_not_of(" \t\r");v=first==std::string::npos?"":v.substr(first,last-first+1);
  if(k=="transfer-encoding"){if(chunked||v!="chunked")throw Error("unsupported_http_encoding");chunked=true;}
  if(k=="content-encoding"&&v!="identity")throw Error("unsupported_http_encoding");
  if(k=="content-length"){
   if(v.empty()||v.find_first_not_of("0123456789")!=std::string::npos||length!=SIZE_MAX)throw Error("invalid_http_length");
   unsigned long long n=std::stoull(v);
   if(n>4*maxJson)throw Error("http_response_too_large");
   length=size_t(n);
  }
 }
 std::string b=raw.substr(end+4);
 if(b.size()>4*maxJson||(chunked&&length!=SIZE_MAX))throw Error("invalid_http_framing");
 if(chunked) {
  size_t at=0;std::string decoded;
  for(;;) {
   auto next=b.find("\r\n",at);if(next==std::string::npos||next-at>128)throw Error("invalid_http_chunk");
   auto text=b.substr(at,next-at);text=text.substr(0,text.find(';'));
   if(text.empty()||text.find_first_not_of("0123456789abcdefABCDEF")!=std::string::npos)throw Error("invalid_http_chunk");
   auto n=std::stoull(text,nullptr,16);at=next+2;
   if(n==0){
    // Require the terminal empty line, including when trailers are present.
    for(;;){
     auto trailer=b.find("\r\n",at);if(trailer==std::string::npos||trailer-at>32768)throw Error("invalid_http_chunk");
     if(trailer==at){at+=2;break;}
     auto colon=b.find(':',at);if(colon==std::string::npos||colon==at||colon>=trailer)throw Error("invalid_http_chunk");
     auto name=lower(b.substr(at,colon-at));if(name=="content-length"||name=="transfer-encoding")throw Error("invalid_http_framing");
     at=trailer+2;
    }
    if(at!=b.size())throw Error("invalid_http_chunk");
    break;
   }
   if(n>4*maxJson||at+n+2>b.size()||b.substr(at+size_t(n),2)!="\r\n")throw Error("invalid_http_chunk");
   decoded.append(b,at,size_t(n));at+=size_t(n)+2;if(decoded.size()>4*maxJson)throw Error("http_response_too_large");
  }b=std::move(decoded);
 }else if(length!=SIZE_MAX){if(b.size()!=length)throw Error("invalid_http_length");}
 return {code,b};
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
 struct SocketGuard{SOCKET s;~SocketGuard(){closesocket(s);}} guard{raw};
 struct Deadline {
  std::mutex mutex;std::condition_variable ready;bool stopped=false;std::thread worker;
  Deadline(SOCKET s,Clock::time_point until):worker([this,s,until]{std::unique_lock lock(mutex);if(!ready.wait_until(lock,until,[this]{return stopped;}))shutdown(s,SD_BOTH);}){}
  ~Deadline(){{std::lock_guard lock(mutex);stopped=true;}ready.notify_one();worker.join();}
 } timeoutGuard(raw,deadline);
 u_long nonblock=0;ioctlsocket(raw,FIONBIO,&nonblock);
 auto remainingMs=std::chrono::duration_cast<std::chrono::milliseconds>(deadline-Clock::now()).count();
 if(remainingMs<=0)throw Error("control_unreachable");
 DWORD timeout=DWORD(std::max<int64_t>(1,remainingMs));
 setsockopt(raw,SOL_SOCKET,SO_RCVTIMEO,reinterpret_cast<char*>(&timeout),sizeof(timeout));setsockopt(raw,SOL_SOCKET,SO_SNDTIMEO,reinterpret_cast<char*>(&timeout),sizeof(timeout));
 std::unique_ptr<SSL,decltype(&SSL_free)> tls(SSL_new(context_.get()),SSL_free);auto host=originHost(profile_.at("Origin"));
 if(!tls||SSL_set_fd(tls.get(),int(raw))!=1||SSL_set_tlsext_host_name(tls.get(),host.c_str())!=1)throw Error("tls_initialization_failed");
 in_addr v4{};in6_addr v6{};bool ip=InetPtonA(AF_INET,host.c_str(),&v4)==1||InetPtonA(AF_INET6,host.c_str(),&v6)==1;
 auto param=SSL_get0_param(tls.get());X509_VERIFY_PARAM_set_hostflags(param,X509_CHECK_FLAG_NO_PARTIAL_WILDCARDS);
 if((ip?X509_VERIFY_PARAM_set1_ip_asc(param,host.c_str()):X509_VERIFY_PARAM_set1_host(param,host.c_str(),0))!=1)throw Error("tls_identity_failed");
 if(SSL_connect(tls.get())!=1||SSL_get_verify_result(tls.get())!=X509_V_OK)throw Error("tls_certificate_failed");
 std::string authority=profile_.at("Origin").get<std::string>().substr(8);if(authority.back()=='/')authority.pop_back();
 std::string req=method+" "+path+" HTTP/1.1\r\nHost: "+authority+"\r\nConnection: close\r\nAccept: application/json\r\nAccept-Encoding: identity\r\nContent-Type: application/json\r\n"+headers+"Content-Length: "+std::to_string(body.size())+"\r\n\r\n"+body;
 size_t pos=0;while(pos<req.size()){int n=SSL_write(tls.get(),req.data()+pos,int(req.size()-pos));if(n<=0){wipe(req);throw Error("control_write_failed");}pos+=size_t(n);}wipe(req);
 std::string rawResponse;char buffer[8192];
 while(Clock::now()<deadline) {
  int n=SSL_read(tls.get(),buffer,sizeof(buffer));if(n<=0) {
   int e=SSL_get_error(tls.get(),n);
   if(e==SSL_ERROR_ZERO_RETURN||(e==SSL_ERROR_SYSCALL&&n==0))break;
   // A complete Content-Length response is accepted even without close_notify.
   try {decodeHttp(rawResponse);break;}catch(const std::exception&){throw Error("control_read_failed");}
  }
  rawResponse.append(buffer,n);if(rawResponse.size()>4*maxJson+32768)throw Error("http_response_too_large");
  auto h=rawResponse.find("\r\n\r\n");
  if(h!=std::string::npos&&lower(rawResponse.substr(0,h)).find("content-length:")!=std::string::npos){
   try{decodeHttp(rawResponse);break;}catch(const std::exception&){}
  }
 }
 auto response=decodeHttp(rawResponse);
 if(response.status<200||response.status>=300) {
  auto v=Json::parse(response.body,nullptr,false);std::string e=v.is_object()?v.value("error","remote_rejected"):"remote_rejected";
  if(!std::regex_match(e,std::regex(R"(^[a-zA-Z0-9_]{1,80}$)")))e="remote_rejected";
  throw Error(e);
 }
 if(response.body.empty())return Json::object();
 return Json::parse(response.body);
}
bool Transport::probe(size_t i) {
 try{auto h=requestAt(i,"GET","/netgo/v2/health","","");validateHealth(h,profile_.at("ID"));return true;}
 catch(const std::exception& e){if(std::string(e.what())=="protocol_incompatible")incompatible_=true;if(std::string(e.what())=="clock_out_of_sync")clockSkew_=true;return false;}
}
void Transport::select() {
 incompatible_=false;clockSkew_=false;
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
 if(winner==SIZE_MAX)throw Error(clockSkew_?"clock_out_of_sync":incompatible_?"protocol_incompatible":"no_trusted_control_entry");
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
std::string Transport::kind() const{return profile_["Candidates"][selected_]["Kind"];}
std::string Transport::candidate() const{return profile_["Candidates"][selected_]["ID"];}
Json Transport::call(const std::string& method,const std::string& path,const Json& value,const std::string& seed,const std::string& token,const std::string& idem) {
 std::string body=value.is_null()?"":value.dump(),headers="X-NetGo-Protocol: 2\r\n";
 if(!idem.empty())headers+="Idempotency-Key: "+idem+"\r\n";
 if(!token.empty()){
  auto stamp=std::to_string(std::time(nullptr)),nonce=randomID();
  headers+="Authorization: Bearer "+token+"\r\nX-NetGo-Time: "+stamp+"\r\nX-NetGo-Nonce: "+nonce+"\r\nX-NetGo-Signature: "+signature(seed,signedMessage(method,path,stamp,nonce,body,token))+"\r\n";
 }
 try{auto result=requestAt(selected_,method,path,body,headers);wipe(body);wipe(headers);return result;}catch(...){wipe(body);wipe(headers);throw;}
}
}
