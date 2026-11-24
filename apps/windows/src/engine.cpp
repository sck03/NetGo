#include "connection.hpp"
#include <iphlpapi.h>
#include <algorithm>

namespace netgo {
Json engineCall(const Json& request,const std::atomic_bool& cancel) {
 wchar_t programFiles[32768]{};DWORD size=GetEnvironmentVariableW(L"ProgramFiles",programFiles,32768);
 auto installed=size&&size<32768?std::filesystem::path(programFiles)/L"NetGoNative"/L"engine"/L"netbird.exe":std::filesystem::path();
 auto binary=!installed.empty()&&std::filesystem::exists(installed)?installed:executableDir()/L"netbird.exe";
 if(!std::filesystem::exists(binary))throw Error("engine_missing");
 SECURITY_ATTRIBUTES sa{sizeof(sa),nullptr,TRUE};HANDLE inR=nullptr,inW=nullptr,outR=nullptr,outW=nullptr;
 if(!CreatePipe(&inR,&inW,&sa,65536))throw Error("engine_pipe_failed");
 Handle inputRead(inR),inputWrite(inW);
 if(!CreatePipe(&outR,&outW,&sa,65536))throw Error("engine_pipe_failed");
 Handle outputRead(outR),outputWrite(outW);
 SetHandleInformation(inW,HANDLE_FLAG_INHERIT,0);SetHandleInformation(outR,HANDLE_FLAG_INHERIT,0);
 Handle null(CreateFileW(L"NUL",GENERIC_WRITE,FILE_SHARE_READ|FILE_SHARE_WRITE,&sa,OPEN_EXISTING,0,nullptr));
 STARTUPINFOEXW si{};si.StartupInfo.cb=sizeof(si);si.StartupInfo.dwFlags=STARTF_USESTDHANDLES;
 si.StartupInfo.hStdInput=inR;si.StartupInfo.hStdOutput=outW;si.StartupInfo.hStdError=null;
 SIZE_T bytes=0;InitializeProcThreadAttributeList(nullptr,1,0,&bytes);std::vector<unsigned char> attrs(bytes);
 si.lpAttributeList=reinterpret_cast<LPPROC_THREAD_ATTRIBUTE_LIST>(attrs.data());
 if(!InitializeProcThreadAttributeList(si.lpAttributeList,1,0,&bytes))throw Error("engine_spawn_failed");
 HANDLE inherited[]={inR,outW,null};
 if(!UpdateProcThreadAttribute(si.lpAttributeList,0,PROC_THREAD_ATTRIBUTE_HANDLE_LIST,inherited,sizeof(inherited),nullptr,nullptr)){DeleteProcThreadAttributeList(si.lpAttributeList);throw Error("engine_spawn_failed");}
 std::wstring cmd=L"\""+binary.wstring()+L"\" netgo-rpc --daemon-addr npipe://netgo-netbird";PROCESS_INFORMATION pi{};
 BOOL ok=CreateProcessW(binary.c_str(),cmd.data(),nullptr,nullptr,TRUE,CREATE_NO_WINDOW|EXTENDED_STARTUPINFO_PRESENT,nullptr,binary.parent_path().c_str(),&si.StartupInfo,&pi);
 DeleteProcThreadAttributeList(si.lpAttributeList);if(!ok)throw Error("engine_spawn_failed");
 Handle process(pi.hProcess),thread(pi.hThread);CloseHandle(inputRead.value);inputRead.value=nullptr;CloseHandle(outputWrite.value);outputWrite.value=nullptr;
 std::string b=request.dump()+"\n";DWORD written=0;
 if(b.size()>65536||!WriteFile(inputWrite,b.data(),DWORD(b.size()),&written,nullptr)||written!=b.size()) {wipe(b);TerminateProcess(process,1);throw Error("engine_input_failed");}
 wipe(b);CloseHandle(inputWrite.value);inputWrite.value=nullptr;
 auto deadline=Clock::now()+std::chrono::seconds(25);std::string out;char buffer[4096];
 for(;;) {
  DWORD available=0;if(PeekNamedPipe(outputRead,nullptr,0,nullptr,&available,nullptr)&&available){
   DWORD n=0;if(ReadFile(outputRead,buffer,std::min<DWORD>(available,sizeof(buffer)),&n,nullptr))out.append(buffer,n);
  } else if(WaitForSingleObject(process,20)==WAIT_OBJECT_0){
   // The child may write its final JSON between PeekNamedPipe and the exit
   // wait. Drain those bytes before parsing, even when the process has exited.
   DWORD remaining=0;
   if(PeekNamedPipe(outputRead,nullptr,0,nullptr,&remaining,nullptr)&&remaining)continue;
   break;
  }
  if(cancel||Clock::now()>deadline||out.size()>4*maxJson){TerminateProcess(process,1);WaitForSingleObject(process,5000);throw Error(cancel?"cancelled":"engine_timeout");}
 }
 try{auto result=Json::parse(out);if(result.contains("error"))throw Error(result["error"].get<std::string>());return result;}catch(const Json::exception&){throw Error("engine_protocol_failed");}
}
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
 Json safe={{"version","0.3.0-dev"},{"platform","Windows native C++"},{"state",status.value("state","disconnected")},{"error",status.value("error","")},{"control",status.value("control","")},{"generation",status.value("generation",uint64_t(0))}};
 auto d=status.value("data",Json::object());safe["data"]={{"state",d.value("state","")},{"path",d.value("path","negotiating")},{"rx",d.value("rx",int64_t(0))},{"tx",d.value("tx",int64_t(0))}};
 safe["events"]=logs;return safe;
}}
