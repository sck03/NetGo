#include "netgo.hpp"
#include <wincrypt.h>
#undef X509_NAME
#include <openssl/pem.h>
#include <openssl/x509v3.h>
#include <algorithm>
#include <cctype>
#include <regex>
#include <sstream>

namespace netgo {
Address address(const std::string& s) {
 static const std::regex r(R"(^(\[[0-9a-fA-F:.]+\]|[a-zA-Z0-9_.-]+):([0-9]{1,5})$)");
 std::smatch m;if(!std::regex_match(s,m,r))throw Error("invalid_candidate");
 int p=std::stoi(m[2]);if(p<1||p>65535)throw Error("invalid_candidate");
 std::string host=m[1];if(host.front()=='[')host=host.substr(1,host.size()-2);return {host,static_cast<unsigned short>(p)};
}
std::string originHost(const std::string& s) {
 static const std::regex r(R"(^https://(\[[0-9a-fA-F:.]+\]|[a-zA-Z0-9.-]+)(:[0-9]{1,5})?$)");
 std::smatch m;if(!std::regex_match(s,m,r))throw Error("invalid_https_origin");
 if(m[2].matched)address(m[1].str()+m[2].str());
 std::string host=m[1];if(host.front()=='[')host=host.substr(1,host.size()-2);return host;
}
void validateProfile(const Json& p) {
 static const std::vector<std::string> allowed={"Protocol","ID","Name","Origin","Candidates","CA","GatewayPeerID","GatewayIP","LocalRelay","AuxiliaryRelay"};
 if(!p.is_object())throw Error("invalid_profile");
 for(auto i=p.begin();i!=p.end();++i)if(std::find(allowed.begin(),allowed.end(),i.key())==allowed.end())throw Error("unknown_profile_field");
 std::string id=p.at("ID");
 if(!std::regex_match(id,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)"))||p.at("Protocol")!=1)throw Error("unsupported_profile");
 auto name=p.at("Name").get<std::string>();if(name.empty()||name.size()>256)throw Error("invalid_profile_name");
 wide(name);originHost(p.at("Origin"));
 const auto& candidates=p.at("Candidates");if(!candidates.is_array()||candidates.empty()||candidates.size()>8)throw Error("invalid_candidates");
 bool direct=false;std::vector<std::string> ids;
 for(const auto& c:candidates) {
  if(!c.is_object()||c.size()!=3||!c.contains("ID")||!c.contains("Address")||!c.contains("Kind"))throw Error("invalid_candidate");
  std::string ci=c.at("ID");if(!std::regex_match(ci,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)"))||std::find(ids.begin(),ids.end(),ci)!=ids.end())throw Error("duplicate_candidate");
  ids.push_back(ci);address(c.at("Address"));std::string kind=c.at("Kind");
  if(kind!="direct"&&kind!="auxiliary")throw Error("invalid_candidate_kind");
  direct|=kind=="direct";
 }
 if(!direct)throw Error("direct_candidate_required");
 in_addr ip{};if(InetPtonA(AF_INET,p.at("GatewayIP").get<std::string>().c_str(),&ip)!=1)throw Error("invalid_gateway_ip");
 auto ca=p.value("CA","");if(ca.size()>32*1024)throw Error("invalid_ca");
 if(!ca.empty()){
  std::unique_ptr<BIO,decltype(&BIO_free)> bio(BIO_new_mem_buf(ca.data(),int(ca.size())),BIO_free);
  X509* cert=PEM_read_bio_X509(bio.get(),nullptr,nullptr,nullptr);if(!cert)throw Error("invalid_ca");X509_free(cert);
 }
}
static std::string lower(std::string s){for(char& c:s)c=char(std::tolower(static_cast<unsigned char>(c)));return s;}
HttpResponse decodeHttp(const std::string& raw) {
 auto end=raw.find("\r\n\r\n");if(end==std::string::npos||end>32768)throw Error("invalid_http_response");
 std::istringstream lines(raw.substr(0,end));std::string version,line;int code=0;lines>>version>>code;std::getline(lines,line);
 if(version!="HTTP/1.1"&&version!="HTTP/1.0")throw Error("invalid_http_response");
 bool chunked=false;size_t length=SIZE_MAX;
 while(std::getline(lines,line)) {
  auto colon=line.find(':');if(colon==std::string::npos)continue;
  auto k=lower(line.substr(0,colon));auto v=lower(line.substr(colon+1));
  if(k=="transfer-encoding"){if(v.find("chunked")==std::string::npos)throw Error("unsupported_http_encoding");chunked=true;}
  if(k=="content-encoding"&&v.find("identity")==std::string::npos)throw Error("unsupported_http_encoding");
  if(k=="content-length"){
   size_t parsed=0;unsigned long long n=std::stoull(v,&parsed);
   if(n>4*maxJson)throw Error("http_response_too_large");
   length=size_t(n);
  }
 }
 std::string b=raw.substr(end+4);
 if(chunked) {
  size_t at=0;std::string decoded;
  for(;;) {
   auto next=b.find("\r\n",at);if(next==std::string::npos||next-at>128)throw Error("invalid_http_chunk");
   auto text=b.substr(at,next-at);text=text.substr(0,text.find(';'));
   if(text.empty()||text.find_first_not_of("0123456789abcdefABCDEF")!=std::string::npos)throw Error("invalid_http_chunk");
   auto n=std::stoull(text,nullptr,16);at=next+2;if(n==0)break;
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
Transport::Transport(Json p):profile_(std::move(p)),context_(SSL_CTX_new(TLS_client_method()),SSL_CTX_free) {
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
 auto target=address(profile_["Candidates"].at(index).at("Address"));
 addrinfo hints{},*list=nullptr;hints.ai_family=AF_UNSPEC;hints.ai_socktype=SOCK_STREAM;hints.ai_protocol=IPPROTO_TCP;
 if(getaddrinfo(target.host.c_str(),std::to_string(target.port).c_str(),&hints,&list)!=0)throw Error("control_dns_failed");
 std::unique_ptr<addrinfo,decltype(&freeaddrinfo)> addrs(list,freeaddrinfo);SOCKET raw=INVALID_SOCKET;
 for(auto a=list;a;a=a->ai_next) {
  SOCKET candidate=socket(a->ai_family,a->ai_socktype,a->ai_protocol);if(candidate==INVALID_SOCKET)continue;
  u_long nonblock=1;ioctlsocket(candidate,FIONBIO,&nonblock);int e=::connect(candidate,a->ai_addr,int(a->ai_addrlen));
  if(e==SOCKET_ERROR&&WSAGetLastError()!=WSAEWOULDBLOCK){closesocket(candidate);continue;}
  fd_set write,except;FD_ZERO(&write);FD_ZERO(&except);FD_SET(candidate,&write);FD_SET(candidate,&except);timeval wait{3,0};
  if(::select(0,nullptr,&write,&except,&wait)>0&&!FD_ISSET(candidate,&except)){
   int error=0,len=sizeof(error);getsockopt(candidate,SOL_SOCKET,SO_ERROR,reinterpret_cast<char*>(&error),&len);
   if(!error){raw=candidate;break;}
  }closesocket(candidate);
 }
 if(raw==INVALID_SOCKET)throw Error("control_unreachable");
 struct SocketGuard{SOCKET s;~SocketGuard(){closesocket(s);}} guard{raw};
 u_long nonblock=0;ioctlsocket(raw,FIONBIO,&nonblock);DWORD timeout=3000;
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
 std::string rawResponse;char buffer[8192];auto deadline=Clock::now()+std::chrono::seconds(12);
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
 try{auto h=requestAt(i,"GET","/netgo/v1/health","","");return h.at("site_id")==profile_.at("ID")&&h.at("protocol")==1;}catch(const std::exception&){return false;}
}
void Transport::select() {
 for(const std::string kind:{"direct","auxiliary"})for(size_t i=0;i<profile_["Candidates"].size();++i){
  if(profile_["Candidates"][i]["Kind"]==kind&&probe(i)){selected_=i;switched_=Clock::now();return;}
 }throw Error("no_trusted_control_entry");
}
void Transport::recover() {
 if(!probe(selected_)){select();directSuccess_=0;return;}
 if(kind()=="auxiliary"&&Clock::now()-switched_>std::chrono::seconds(15)){
  for(size_t i=0;i<profile_["Candidates"].size();++i)if(profile_["Candidates"][i]["Kind"]=="direct"&&probe(i)){
   if(++directSuccess_>=2){selected_=i;switched_=Clock::now();directSuccess_=0;}return;
  }directSuccess_=0;
 }
}
std::string Transport::kind() const{return profile_["Candidates"][selected_]["Kind"];}
Json Transport::call(const std::string& method,const std::string& path,const Json& value,const std::string& seed,const std::string& token,const std::string& idem) {
 std::string body=value.is_null()?"":value.dump(),headers;
 if(!idem.empty())headers+="Idempotency-Key: "+idem+"\r\n";
 if(!token.empty()){
  auto stamp=std::to_string(std::time(nullptr)),nonce=randomID();
  headers+="Authorization: Bearer "+token+"\r\nX-NetGo-Time: "+stamp+"\r\nX-NetGo-Nonce: "+nonce+"\r\nX-NetGo-Signature: "+signature(seed,signedMessage(method,path,stamp,nonce,body,token))+"\r\n";
 }
 try{auto result=requestAt(selected_,method,path,body,headers);wipe(body);wipe(headers);return result;}catch(...){wipe(body);wipe(headers);throw;}
}
}
