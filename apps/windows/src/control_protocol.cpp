#include "connection.hpp"
#include <algorithm>
#include <cctype>
#include <sstream>

namespace netgo {
namespace {
std::string lower(std::string s) { for(char& c:s)c=char(std::tolower(static_cast<unsigned char>(c)));return s; }
}
void validateHealth(const Json& h,const std::string& siteID) {
 if(h.value("site_id","")!=siteID)throw Error("site_identity_mismatch");
 if(h.value("protocol",0)!=2||!h.contains("capabilities")||!h["capabilities"].is_array())throw Error("protocol_incompatible");
 for(const auto& c:{"native-auth-v1","device-signature-v1","leases-v1","profile-revision-v1"}){
  if(std::find(h["capabilities"].begin(),h["capabilities"].end(),Json(c))==h["capabilities"].end())throw Error("protocol_incompatible");
 }
	{
  if(!h.contains("server_unix")||!h["server_unix"].is_number_integer())throw Error("invalid_control_health");
  auto remote=h["server_unix"].get<int64_t>();
  if(remote<946684800||remote>4102444800)throw Error("invalid_control_health");
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
}
