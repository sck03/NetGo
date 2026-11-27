#include "connection.hpp"
#include "engine_channel.hpp"
#include <algorithm>

namespace netgo {
namespace {
// One authenticated child per controller process. Framed stdin/stdout keep
// secrets out of arguments and amortize process startup across status polling.
class EngineRPC {
 Handle process, job;
 EngineChannel input{true}, output{false};
public:
 EngineRPC() {
 wchar_t programFiles[32768]{};DWORD size=GetEnvironmentVariableW(L"ProgramFiles",programFiles,32768);
 auto installed=size&&size<32768?std::filesystem::path(programFiles)/L"NetGoNative"/L"engine"/L"netbird.exe":std::filesystem::path();
 auto binary=!installed.empty()&&std::filesystem::exists(installed)?installed:executableDir()/L"netbird.exe";
 if(!std::filesystem::exists(binary))throw Error("engine_missing");
 SECURITY_ATTRIBUTES sa{sizeof(sa),nullptr,TRUE};
 Handle null(CreateFileW(L"NUL",GENERIC_WRITE,FILE_SHARE_READ|FILE_SHARE_WRITE,&sa,OPEN_EXISTING,0,nullptr));
 STARTUPINFOEXW si{};si.StartupInfo.cb=sizeof(si);si.StartupInfo.dwFlags=STARTF_USESTDHANDLES;
 si.StartupInfo.hStdInput=input.child();si.StartupInfo.hStdOutput=output.child();si.StartupInfo.hStdError=null;
 SIZE_T bytes=0;InitializeProcThreadAttributeList(nullptr,1,0,&bytes);std::vector<unsigned char> attrs(bytes);
 si.lpAttributeList=reinterpret_cast<LPPROC_THREAD_ATTRIBUTE_LIST>(attrs.data());
 if(!InitializeProcThreadAttributeList(si.lpAttributeList,1,0,&bytes))throw Error("engine_spawn_failed");
 HANDLE inherited[]={input.child(),output.child(),null};
 if(!UpdateProcThreadAttribute(si.lpAttributeList,0,PROC_THREAD_ATTRIBUTE_HANDLE_LIST,inherited,sizeof(inherited),nullptr,nullptr)){DeleteProcThreadAttributeList(si.lpAttributeList);throw Error("engine_spawn_failed");}
 std::wstring cmd=L"\""+binary.wstring()+L"\" netgo-rpc --daemon-addr npipe://netgo-netbird";PROCESS_INFORMATION pi{};
 BOOL ok=CreateProcessW(binary.c_str(),cmd.data(),nullptr,nullptr,TRUE,CREATE_NO_WINDOW|CREATE_SUSPENDED|EXTENDED_STARTUPINFO_PRESENT,nullptr,binary.parent_path().c_str(),&si.StartupInfo,&pi);
 DeleteProcThreadAttributeList(si.lpAttributeList);if(!ok)throw Error("engine_spawn_failed");
 process.value=pi.hProcess;Handle thread(pi.hThread);input.closeChild();output.closeChild();
 JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
 limits.BasicLimitInformation.LimitFlags=JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
 job.value=CreateJobObjectW(nullptr,nullptr);
 if(!job.value||!SetInformationJobObject(job,JobObjectExtendedLimitInformation,&limits,sizeof(limits))||!AssignProcessToJobObject(job,process)||ResumeThread(thread)==DWORD(-1)){
  TerminateProcess(process,1);throw Error("engine_spawn_failed");
 }

 }
 ~EngineRPC() {
  if(process.value && WaitForSingleObject(process,0)!=WAIT_OBJECT_0) TerminateProcess(process,1);
 }
 Json call(const Json& request,const std::atomic_bool& cancel,Clock::time_point deadline) {
  if(cancel)throw Error("cancelled");
  std::string b=request.dump()+"\n";
  if(b.size()>maxEngineRequest){wipe(b);throw Error("engine_request_too_large");}
  try { for(size_t sent=0;sent<b.size();)sent+=input.transfer(true,b.data()+sent,DWORD(b.size()-sent),deadline,cancel); }
  catch(...) {wipe(b);throw;}
  wipe(b);
  std::string out;char buffer[4096];
  for(;;){
   auto n=output.transfer(false,buffer,sizeof(buffer),deadline,cancel);
   auto begin=out.size();
   out.append(buffer,n);
   if(out.size()>maxResponseJson)throw Error("engine_protocol_failed");
   auto newline=out.find('\n',begin);
   if(newline!=std::string::npos){if(newline!=out.size()-1)throw Error("engine_protocol_failed");break;}
  }
  try{auto result=Json::parse(out);if(result.contains("error"))throw Error(result["error"].get<std::string>());return result;}
  catch(const Json::exception&){throw Error("engine_protocol_failed");}
 }
};
}
Json engineCall(const Json& request,const std::atomic_bool& cancel) {
 static std::timed_mutex mutex;
 static std::unique_ptr<EngineRPC> engine;
 auto deadline=Clock::now()+std::chrono::seconds(25);
 std::unique_lock lock(mutex,std::defer_lock);
 while(!lock.try_lock_for(std::chrono::milliseconds(20))){
  if(cancel||Clock::now()>=deadline)throw Error(cancel?"cancelled":"engine_timeout");
 }
 try {
  if(cancel)throw Error("cancelled");
  if(!engine)engine=std::make_unique<EngineRPC>();
  auto result=engine->call(request,cancel,deadline);
  if(request.value("action","")=="down")engine.reset();
  return result;
 } catch(...) {engine.reset();throw;}
}
}
