#include "connection.hpp"
#include <iphlpapi.h>
#include <algorithm>

namespace netgo {
void routeConflicts(const Json& resources) {
 ULONG size=16384;std::vector<unsigned char> buf(size);
 auto e=GetAdaptersAddresses(AF_INET,GAA_FLAG_SKIP_ANYCAST|GAA_FLAG_SKIP_MULTICAST|GAA_FLAG_SKIP_DNS_SERVER,nullptr,reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buf.data()),&size);
 if(e==ERROR_BUFFER_OVERFLOW){buf.resize(size);e=GetAdaptersAddresses(AF_INET,GAA_FLAG_SKIP_ANYCAST|GAA_FLAG_SKIP_MULTICAST|GAA_FLAG_SKIP_DNS_SERVER,nullptr,reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buf.data()),&size);}
 if(e!=NO_ERROR)throw Error("local_network_unavailable");
 for(auto a=reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buf.data());a;a=a->Next) {
  if(a->IfType==IF_TYPE_SOFTWARE_LOOPBACK||a->OperStatus!=IfOperStatusUp||a->IfType==IF_TYPE_TUNNEL)continue;
  for(auto u=a->FirstUnicastAddress;u;u=u->Next) {
   if(u->Address.lpSockaddr->sa_family!=AF_INET)continue;
   uint32_t local=ntohl(reinterpret_cast<sockaddr_in*>(u->Address.lpSockaddr)->sin_addr.s_addr);
   if((local&0xffc00000)==0x64400000)continue; // NetBird's CGNAT overlay is not a LAN interface.
   for(const auto& r:resources) {
    if(r.value("protocol","")=="internet"&&r.value("cidr","")=="0.0.0.0/0")continue;
    std::string cidr=r.at("cidr");size_t slash=cidr.find('/');if(slash==std::string::npos)throw Error("invalid_route");
    int bits=std::stoi(cidr.substr(slash+1));in_addr ip{};
    if(bits<0||bits>32||InetPtonA(AF_INET,cidr.substr(0,slash).c_str(),&ip)!=1)throw Error("invalid_route");
    if(bits==32)continue;
    int common=std::min<int>(bits,u->OnLinkPrefixLength);uint32_t mask=common?0xffffffffu<<(32-common):0;
    if((local&mask)==(ntohl(ip.s_addr)&mask))throw Error("route_conflict");
   }
  }
 }
}
Json selectedResources(const Json& resources,bool internet){
 Json selected=Json::array();bool granted=false;
 for(const auto& r:resources){if(!r.value("enabled",false))continue;if(r.value("protocol","")=="internet"){
  if(r.value("cidr","")!="0.0.0.0/0"||!r.value("ports",Json::array()).empty())throw Error("invalid_internet_resource");
  granted=true;if(!internet)continue;
 }selected.push_back(r);}
 if(internet&&!granted)throw Error("internet_forbidden");
 return selected;
}
Json safeDiagnostic(const Json& status,const Json& logs) {
 Json safe={{"version",appVersion},{"platform","Windows native C++"},{"state",status.value("state","disconnected")},{"error",status.value("error","")},{"control",status.value("control","")},{"generation",status.value("generation",uint64_t(0))}};
 auto d=status.value("data",Json::object());safe["data"]={{"state",d.value("state","")},{"path",d.value("path","negotiating")},{"rx",d.value("rx",int64_t(0))},{"tx",d.value("tx",int64_t(0))}};
 safe["events"]=logs;return safe;
}}
