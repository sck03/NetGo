#include "common.hpp"
#include <wincrypt.h>
#include <algorithm>
#include <cctype>
#include <regex>

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
 std::string host=m[1];if(host.front()=='[')host=host.substr(1,host.size()-2);std::transform(host.begin(),host.end(),host.begin(),[](unsigned char c){return char(std::tolower(c));});return host;
}
void validateProfile(const Json& p) {
 static const std::vector<std::string> allowed={"Protocol","ConfigRevision","ID","Name","Origin","Candidates","CA","GatewayPeerID","GatewayIP","LocalRelay","AuxiliaryRelay","InternetDNS"};
 if(!p.is_object())throw Error("invalid_profile");
 for(auto i=p.begin();i!=p.end();++i)if(std::find(allowed.begin(),allowed.end(),i.key())==allowed.end())throw Error("unknown_profile_field");
 std::string id=p.at("ID");
 if(p.contains("ConfigRevision")&&(!p["ConfigRevision"].is_number_unsigned()&&!p["ConfigRevision"].is_number_integer()))throw Error("invalid_profile_revision");
 if(p.contains("ConfigRevision")&&(p["ConfigRevision"]<0||p["ConfigRevision"]>9007199254740991ULL))throw Error("invalid_profile_revision");
 if(!std::regex_match(id,std::regex(R"(^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$)"))||p.at("Protocol")!=2)throw Error("unsupported_profile");
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
 auto dns=p.value("InternetDNS",Json::array());if(!dns.is_array()||dns.size()>3)throw Error("invalid_profile");
 for(const auto& value:dns){in_addr address{};if(!value.is_string()||InetPtonA(AF_INET,value.get<std::string>().c_str(),&address)!=1)throw Error("invalid_profile");auto number=ntohl(address.s_addr);if((number>>24)==0||(number>>24)==127||number>=0xe0000000||(number&0xffff0000)==0xa9fe0000)throw Error("invalid_profile");}
 auto ca=p.value("CA","");if(ca.size()>32*1024)throw Error("invalid_ca");
 if(!ca.empty()){
  DWORD size=0;
  if(!CryptStringToBinaryA(ca.data(),DWORD(ca.size()),CRYPT_STRING_BASE64HEADER,nullptr,&size,nullptr,nullptr))throw Error("invalid_ca");
  std::vector<BYTE> encoded(size);
  if(!CryptStringToBinaryA(ca.data(),DWORD(ca.size()),CRYPT_STRING_BASE64HEADER,encoded.data(),&size,nullptr,nullptr))throw Error("invalid_ca");
  auto cert=CertCreateCertificateContext(X509_ASN_ENCODING,encoded.data(),size);
  if(!cert)throw Error("invalid_ca");
  CertFreeCertificateContext(cert);
 }
}
void validateProfileUpdate(const Json& old,const Json& next) {
 validateProfile(next);
 for(const auto& key:{"ID","Protocol"})if(old.at(key)!=next.at(key))throw Error("site_trust_changed_use_new_id");
 if(originHost(old.at("Origin"))!=originHost(next.at("Origin")))throw Error("site_trust_changed_use_new_id");
 if(old.value("CA","")!=next.value("CA",""))throw Error("site_trust_changed_use_new_id");
 auto previous=old.value("ConfigRevision",uint64_t(0)),revision=next.value("ConfigRevision",uint64_t(0));
 if(revision<previous)throw Error("profile_revision_stale");
 if(previous&&revision==previous){
  for(const auto& key:{"Origin","Candidates","GatewayPeerID","GatewayIP","LocalRelay","AuxiliaryRelay","InternetDNS"})if(old.value(key,Json(""))!=next.value(key,Json("")))throw Error("profile_revision_conflict");
 }
}
std::chrono::system_clock::time_point parseTime(const std::string& s) {
 // Require an explicit RFC3339 timezone; servers may use UTC or local offsets.
 static const std::regex form(R"(^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$)");
 std::smatch m;if(!std::regex_match(s,m,form)) throw Error("invalid_server_time");
 std::tm t{};t.tm_year=std::stoi(m[1])-1900;t.tm_mon=std::stoi(m[2])-1;t.tm_mday=std::stoi(m[3]);t.tm_hour=std::stoi(m[4]);t.tm_min=std::stoi(m[5]);t.tm_sec=std::stoi(m[6]);
 auto original=t;auto epoch=_mkgmtime64(&t);
 if(epoch<0||t.tm_year!=original.tm_year||t.tm_mon!=original.tm_mon||t.tm_mday!=original.tm_mday||t.tm_hour!=original.tm_hour||t.tm_min!=original.tm_min||t.tm_sec!=original.tm_sec)throw Error("invalid_server_time");
 std::string zone=m[8];if(zone!="Z"){int hours=std::stoi(zone.substr(1,2)),minutes=std::stoi(zone.substr(4,2));if(hours>23||minutes>59)throw Error("invalid_server_time");epoch-=(zone[0]=='+'?1:-1)*(hours*3600+minutes*60);}
 return std::chrono::system_clock::from_time_t(epoch);
}
}
