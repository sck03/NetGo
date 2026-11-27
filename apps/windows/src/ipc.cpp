#include "common.hpp"
#include <sddl.h>
#include <array>

namespace netgo {
static std::string testSuffix;
void testPipe(const std::filesystem::path& root){testSuffix="-test-"+sha256(utf8(root.wstring())).substr(0,16);}
std::wstring pipeName() {
 auto suffix=testSuffix;
 if(suffix.empty()&&std::filesystem::is_regular_file(executableDir()/L"netgo-portable.json"))suffix="-portable-"+sha256(utf8(dataRoot().wstring())).substr(0,16);
 return wide("\\\\.\\pipe\\netgo-native-"+sid()+suffix);
}
static bool io(HANDLE h,bool write,void* data,DWORD count,std::atomic_bool* stop=nullptr,DWORD timeout=45000,const std::function<bool()>& active={}) {
 auto p=static_cast<unsigned char*>(data);auto deadline=Clock::now()+std::chrono::milliseconds(timeout);
 while(count) {
  if((stop&&*stop)||(active&&!active()))return false;
  Handle event(CreateEventW(nullptr,TRUE,FALSE,nullptr));OVERLAPPED ov{};ov.hEvent=event;
  DWORD n=0;BOOL ok=write?WriteFile(h,p,count,&n,&ov):ReadFile(h,p,count,&n,&ov);
  if(!ok&&GetLastError()!=ERROR_IO_PENDING) return false;
  if(!ok) {
   while(WaitForSingleObject(event,100)==WAIT_TIMEOUT) {
    if((stop&&*stop)||(active&&!active())||Clock::now()>deadline) {CancelIoEx(h,&ov);WaitForSingleObject(event,INFINITE);return false;}
   }
   if(!GetOverlappedResult(h,&ov,&n,FALSE))return false;
  }
  if(!n)return false;
  p+=n;count-=n;
 }return true;
}
Json pipeCall(const Json& request,const std::function<bool()>& active) {
 auto name=pipeName();auto ready=Clock::now()+std::chrono::seconds(3);
 while(!WaitNamedPipeW(name.c_str(),100)){if(active&&!active())throw Error("cancelled");if(Clock::now()>ready)throw Error("agent_unavailable");Sleep(25);}
 Handle h(CreateFileW(name.c_str(),GENERIC_READ|GENERIC_WRITE,0,nullptr,OPEN_EXISTING,FILE_FLAG_OVERLAPPED,nullptr));
 if(h.value==INVALID_HANDLE_VALUE)throw Error("agent_unavailable");
 ULONG pid=0;if(!GetNamedPipeServerProcessId(h,&pid))throw Error("agent_identity_failed");
 Handle process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION,FALSE,pid));if(!process.value||sid(process)!=sid())throw Error("agent_identity_failed");
 std::string b=request.dump();if(b.size()>maxJson)throw Error("ipc_too_large");DWORD size=DWORD(b.size());
 if(!io(h,true,&size,4,nullptr,45000,active)||!io(h,true,b.data(),size,nullptr,45000,active)||!io(h,false,&size,4,nullptr,45000,active)||!size||size>maxResponseJson)throw Error("ipc_failed");
 b.assign(size,'\0');if(!io(h,false,b.data(),size,nullptr,45000,active))throw Error("ipc_failed");
 BYTE ack=1;io(h,true,&ack,1,nullptr,3000);
 auto out=Json::parse(b);wipe(b);
 // Status and diagnostics include their last connection error, including an
 // empty error during a healthy connection. Only a standalone error object
 // is an IPC operation failure; otherwise the UI must receive the state.
 if(out.is_object()&&out.size()==1&&out.contains("error"))throw Error(out["error"].get<std::string>());
 return out;
}
void servePipe(std::atomic_bool& stop,const std::function<Json(const Json&)>& dispatch) {
 PSECURITY_DESCRIPTOR desc=nullptr;auto s=wide("D:P(A;;GA;;;"+sid()+")");
 if(!ConvertStringSecurityDescriptorToSecurityDescriptorW(s.c_str(),SDDL_REVISION_1,&desc,nullptr))throw Error("ipc_acl_failed");
 std::unique_ptr<void,decltype(&LocalFree)> guard(desc,LocalFree);SECURITY_ATTRIBUTES sa{sizeof(sa),desc,FALSE};
 // Keep the first instance alive for the entire server lifetime so another
 // process cannot replace the pipe name between requests. A bounded pool lets
 // status/disconnect proceed while the authenticated worker handles an RPC.
 std::array<Handle,4> pipes;
 for(std::size_t i=0;i<pipes.size();++i){
  pipes[i].value=CreateNamedPipeW(pipeName().c_str(),PIPE_ACCESS_DUPLEX|FILE_FLAG_OVERLAPPED|(i==0?FILE_FLAG_FIRST_PIPE_INSTANCE:0),PIPE_TYPE_BYTE|PIPE_READMODE_BYTE|PIPE_WAIT|PIPE_REJECT_REMOTE_CLIENTS,DWORD(pipes.size()),65536,65536,0,&sa);
  if(pipes[i].value==INVALID_HANDLE_VALUE)throw Error("ipc_create_failed");
 }
 auto serve=[&](HANDLE pipe){while(!stop) {
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
    if(n>maxResponseJson){output=Json({{"error","ipc_response_too_large"}}).dump();n=DWORD(output.size());}
    if(n<=maxResponseJson){io(pipe,true,&n,4,nullptr,5000);io(pipe,true,output.data(),n,nullptr,5000);BYTE ack=0;io(pipe,false,&ack,1,nullptr,3000);}
    wipe(output);
   }
  }
  DisconnectNamedPipe(pipe);
 }};
 std::vector<std::thread> workers;
 try{for(std::size_t i=1;i<pipes.size();++i)workers.emplace_back(serve,pipes[i].value);serve(pipes[0]);}
 catch(...){stop=true;for(auto& worker:workers)worker.join();throw;}
 for(auto& worker:workers)worker.join();
}
}
