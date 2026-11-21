#include "netgo.hpp"
#include <wincred.h>
#include <sddl.h>
#include <aclapi.h>
#include <iphlpapi.h>
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <fstream>
#include <regex>
#include <sstream>
#include <iomanip>

namespace netgo {
std::wstring wide(const std::string& s) {
 if(s.empty()) return {};
 int n=MultiByteToWideChar(CP_UTF8,MB_ERR_INVALID_CHARS,s.data(),int(s.size()),nullptr,0);
 if(!n) throw Error("invalid_utf8");
 std::wstring v(n,L'\0'); MultiByteToWideChar(CP_UTF8,MB_ERR_INVALID_CHARS,s.data(),int(s.size()),v.data(),n); return v;
}
std::string utf8(const std::wstring& s) {
 if(s.empty()) return {};
 int n=WideCharToMultiByte(CP_UTF8,WC_ERR_INVALID_CHARS,s.data(),int(s.size()),nullptr,0,nullptr,nullptr);
 if(!n) throw Error("invalid_unicode");
 std::string v(n,'\0'); WideCharToMultiByte(CP_UTF8,WC_ERR_INVALID_CHARS,s.data(),int(s.size()),v.data(),n,nullptr,nullptr); return v;
}
std::string sid(HANDLE process) {
 HANDLE raw=nullptr;
 if(!OpenProcessToken(process,TOKEN_QUERY,&raw)) throw Error("user_identity_unavailable");
 Handle token(raw); DWORD bytes=0; GetTokenInformation(token,TokenUser,nullptr,0,&bytes);
 std::vector<unsigned char> b(bytes);
 if(!GetTokenInformation(token,TokenUser,b.data(),bytes,&bytes)) throw Error("user_identity_unavailable");
 LPWSTR value=nullptr;
 if(!ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(b.data())->User.Sid,&value)) throw Error("user_identity_unavailable");
 std::string result=utf8(value); LocalFree(value); return result;
}
std::filesystem::path dataRoot() {
 wchar_t p[32768]; DWORD n=GetEnvironmentVariableW(L"LOCALAPPDATA",p,32768);
 if(!n || n>=32768) throw Error("local_appdata_unavailable");
 return std::filesystem::path(p)/L"NetGoNative";
}
std::filesystem::path executableDir() {
 std::wstring p(32768,L'\0'); DWORD n=GetModuleFileNameW(nullptr,p.data(),DWORD(p.size()));
 if(!n || n==p.size()) throw Error("executable_path_unavailable");
 p.resize(n); return std::filesystem::path(p).parent_path();
}
void secureDirectory(const std::filesystem::path& p) {
 std::filesystem::create_directories(p);
 PSECURITY_DESCRIPTOR desc=nullptr;
 auto s=wide("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;"+sid()+")");
 if(!ConvertStringSecurityDescriptorToSecurityDescriptorW(s.c_str(),SDDL_REVISION_1,&desc,nullptr)) throw Error("storage_acl_failed");
 BOOL present=FALSE,defaulted=FALSE; PACL acl=nullptr; GetSecurityDescriptorDacl(desc,&present,&acl,&defaulted);
 DWORD e=SetNamedSecurityInfoW(const_cast<wchar_t*>(p.c_str()),SE_FILE_OBJECT,DACL_SECURITY_INFORMATION|PROTECTED_DACL_SECURITY_INFORMATION,nullptr,nullptr,acl,nullptr);
 LocalFree(desc); if(e!=ERROR_SUCCESS) throw Error("storage_acl_failed");
}
std::string readFile(const std::filesystem::path& p,size_t limit) {
 std::ifstream in(p,std::ios::binary); if(!in) throw Error("file_unavailable");
 std::string result; char b[4096];
 while(in) {in.read(b,sizeof(b)); result.append(b,size_t(in.gcount())); if(result.size()>limit) throw Error("file_too_large");}
 return result;
}
void atomicFile(const std::filesystem::path& p,const std::string& value) {
 auto temp=p; temp+=wide("."+randomID()+".tmp");
 {std::ofstream out(temp,std::ios::binary|std::ios::trunc); if(!out) throw Error("storage_write_failed"); out.write(value.data(),std::streamsize(value.size())); out.flush(); if(!out) {std::filesystem::remove(temp);throw Error("storage_write_failed");}}
 if(!MoveFileExW(temp.c_str(),p.c_str(),MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)) {std::filesystem::remove(temp);throw Error("storage_commit_failed");}
}
void wipe(std::string& s) {if(!s.empty()) SecureZeroMemory(s.data(),s.size());s.clear();}
std::string credentialRead(const std::string& target) {
 PCREDENTIALW c=nullptr;
 if(!CredReadW(wide("NetGo2/"+target).c_str(),CRED_TYPE_GENERIC,0,&c)) {
  if(GetLastError()==ERROR_NOT_FOUND) return {};
  throw Error("credential_read_failed");
 }
 std::string result(reinterpret_cast<char*>(c->CredentialBlob),c->CredentialBlobSize); CredFree(c); return result;
}
void credentialWrite(const std::string& target,const std::string& value) {
 if(value.size()>CRED_MAX_CREDENTIAL_BLOB_SIZE) throw Error("credential_too_large");
 std::wstring name=wide("NetGo2/"+target); CREDENTIALW c{};
 c.Type=CRED_TYPE_GENERIC;c.TargetName=name.data();c.CredentialBlobSize=DWORD(value.size());
 c.CredentialBlob=reinterpret_cast<LPBYTE>(const_cast<char*>(value.data()));c.Persist=CRED_PERSIST_LOCAL_MACHINE;
 if(!CredWriteW(&c,0)) throw Error("credential_write_failed");
}
void credentialDelete(const std::string& target) {
 if(!CredDeleteW(wide("NetGo2/"+target).c_str(),CRED_TYPE_GENERIC,0)&&GetLastError()!=ERROR_NOT_FOUND) throw Error("credential_delete_failed");
}
std::string base64url(const unsigned char* p,size_t n) {
 std::string s(4*((n+2)/3),'\0'); EVP_EncodeBlock(reinterpret_cast<unsigned char*>(s.data()),p,int(n));
 for(char& c:s) {if(c=='+') c='-';else if(c=='/') c='_';} while(!s.empty()&&s.back()=='=') s.pop_back();return s;
}
std::string randomID() {unsigned char b[32];if(RAND_bytes(b,sizeof(b))!=1) throw Error("random_failed");return base64url(b,sizeof(b));}
std::string sha256(const std::string& s,bool url) {
 unsigned char digest[32];unsigned int n=0;
 if(EVP_Digest(s.data(),s.size(),digest,&n,EVP_sha256(),nullptr)!=1) throw Error("hash_failed");
 if(url) return base64url(digest,n);
 std::ostringstream v;v<<std::hex<<std::setfill('0');for(unsigned char c:digest)v<<std::setw(2)<<int(c);return v.str();
}
static std::unique_ptr<EVP_PKEY,decltype(&EVP_PKEY_free)> key(const std::string& seed) {
 if(seed.size()!=32) throw Error("invalid_device_key");
 auto p=EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519,nullptr,reinterpret_cast<const unsigned char*>(seed.data()),seed.size());
 if(!p) throw Error("device_key_failed");
 return {p,EVP_PKEY_free};
}
std::string publicKey(const std::string& seed) {
 auto k=key(seed);unsigned char b[32];size_t n=sizeof(b);
 if(EVP_PKEY_get_raw_public_key(k.get(),b,&n)!=1) throw Error("device_key_failed");
 return base64url(b,n);
}
std::string signature(const std::string& seed,const std::string& s) {
 auto k=key(seed);std::unique_ptr<EVP_MD_CTX,decltype(&EVP_MD_CTX_free)> ctx(EVP_MD_CTX_new(),EVP_MD_CTX_free);
 unsigned char b[64];size_t n=sizeof(b);
 if(!ctx||EVP_DigestSignInit(ctx.get(),nullptr,nullptr,nullptr,k.get())!=1||EVP_DigestSign(ctx.get(),b,&n,reinterpret_cast<const unsigned char*>(s.data()),s.size())!=1) throw Error("signature_failed");
 return base64url(b,n);
}
std::string signedMessage(const std::string& method,const std::string& path,const std::string& stamp,const std::string& nonce,const std::string& body,const std::string& token) {
 return method+"\n"+path+"\n"+stamp+"\n"+nonce+"\n"+sha256(body)+"\n"+sha256(token);
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
static std::string testSuffix;
void testPipe(const std::filesystem::path& root){testSuffix="-test-"+sha256(utf8(root.wstring())).substr(0,16);}
std::wstring pipeName() {return wide("\\\\.\\pipe\\netgo-native-"+sid()+testSuffix);}
static bool io(HANDLE h,bool write,void* data,DWORD count,std::atomic_bool* stop=nullptr,DWORD timeout=45000) {
 auto p=static_cast<unsigned char*>(data);auto deadline=Clock::now()+std::chrono::milliseconds(timeout);
 while(count) {
  Handle event(CreateEventW(nullptr,TRUE,FALSE,nullptr));OVERLAPPED ov{};ov.hEvent=event;
  DWORD n=0;BOOL ok=write?WriteFile(h,p,count,&n,&ov):ReadFile(h,p,count,&n,&ov);
  if(!ok&&GetLastError()!=ERROR_IO_PENDING) return false;
  if(!ok) {
   while(WaitForSingleObject(event,100)==WAIT_TIMEOUT) {
    if((stop&&*stop)||Clock::now()>deadline) {CancelIoEx(h,&ov);WaitForSingleObject(event,INFINITE);return false;}
   }
   if(!GetOverlappedResult(h,&ov,&n,FALSE))return false;
  }
  if(!n)return false;
  p+=n;count-=n;
 }return true;
}
Json pipeCall(const Json& request) {
 auto name=pipeName();auto ready=Clock::now()+std::chrono::seconds(3);
 while(!WaitNamedPipeW(name.c_str(),100)){if(Clock::now()>ready)throw Error("agent_unavailable");Sleep(25);}
 Handle h(CreateFileW(name.c_str(),GENERIC_READ|GENERIC_WRITE,0,nullptr,OPEN_EXISTING,FILE_FLAG_OVERLAPPED,nullptr));
 if(h.value==INVALID_HANDLE_VALUE)throw Error("agent_unavailable");
 ULONG pid=0;if(!GetNamedPipeServerProcessId(h,&pid))throw Error("agent_identity_failed");
 Handle process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION,FALSE,pid));if(!process.value||sid(process)!=sid())throw Error("agent_identity_failed");
 std::string b=request.dump();if(b.size()>maxJson)throw Error("ipc_too_large");DWORD size=DWORD(b.size());
 if(!io(h,true,&size,4)||!io(h,true,b.data(),size)||!io(h,false,&size,4)||!size||size>maxJson)throw Error("ipc_failed");
 b.assign(size,'\0');if(!io(h,false,b.data(),size))throw Error("ipc_failed");
 BYTE ack=1;io(h,true,&ack,1,nullptr,3000);
 auto out=Json::parse(b);wipe(b);if(out.contains("error"))throw Error(out["error"].get<std::string>());return out;
}
void servePipe(std::atomic_bool& stop,const std::function<Json(const Json&)>& dispatch) {
 PSECURITY_DESCRIPTOR desc=nullptr;auto s=wide("D:P(A;;GA;;;"+sid()+")");
 if(!ConvertStringSecurityDescriptorToSecurityDescriptorW(s.c_str(),SDDL_REVISION_1,&desc,nullptr))throw Error("ipc_acl_failed");
 std::unique_ptr<void,decltype(&LocalFree)> guard(desc,LocalFree);SECURITY_ATTRIBUTES sa{sizeof(sa),desc,FALSE};
 while(!stop) {
  Handle pipe(CreateNamedPipeW(pipeName().c_str(),PIPE_ACCESS_DUPLEX|FILE_FLAG_OVERLAPPED|FILE_FLAG_FIRST_PIPE_INSTANCE,PIPE_TYPE_BYTE|PIPE_READMODE_BYTE|PIPE_WAIT|PIPE_REJECT_REMOTE_CLIENTS,1,65536,65536,0,&sa));
  if(pipe.value==INVALID_HANDLE_VALUE)throw Error("ipc_create_failed");
  Handle event(CreateEventW(nullptr,TRUE,FALSE,nullptr));OVERLAPPED ov{};ov.hEvent=event;
  BOOL connected=ConnectNamedPipe(pipe,&ov);DWORD e=connected?ERROR_SUCCESS:GetLastError();
  if(e==ERROR_IO_PENDING) {
   while(!stop&&WaitForSingleObject(event,100)==WAIT_TIMEOUT){}
   if(stop){CancelIoEx(pipe,&ov);WaitForSingleObject(event,INFINITE);break;}
   DWORD n=0;connected=GetOverlappedResult(pipe,&ov,&n,FALSE);
  } else if(e==ERROR_PIPE_CONNECTED)connected=TRUE;
  if(!connected)continue;
  DWORD count=0;
  if(io(pipe,false,&count,4,&stop,5000)&&count&&count<=maxJson) {
   std::string b(count,'\0');Json result;
   if(io(pipe,false,b.data(),count,&stop,5000)) {
    try{result=dispatch(Json::parse(b));}catch(const std::exception&){result={{"error","invalid_request"}};}
    wipe(b);auto output=result.dump();DWORD n=DWORD(output.size());
    if(n<=maxJson){io(pipe,true,&n,4,nullptr,5000);io(pipe,true,output.data(),n,nullptr,5000);BYTE ack=0;io(pipe,false,&ack,1,nullptr,3000);}
    wipe(output);
   }
  }
  DisconnectNamedPipe(pipe);
 }
}
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
  } else if(WaitForSingleObject(process,20)==WAIT_OBJECT_0)break;
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
Json safeDiagnostic(const Json& status,const Json& logs) {
 Json safe={{"version","0.2.0-dev"},{"platform","Windows native C++"},{"state",status.value("state","disconnected")},{"error",status.value("error","")},{"control",status.value("control","")},{"generation",status.value("generation",uint64_t(0))}};
 auto d=status.value("data",Json::object());safe["data"]={{"state",d.value("state","")},{"path",d.value("path","negotiating")},{"rx",d.value("rx",int64_t(0))},{"tx",d.value("tx",int64_t(0))}};
 safe["events"]=logs;return safe;
}
}
