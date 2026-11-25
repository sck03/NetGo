#include "connection.hpp"
#include <algorithm>

namespace netgo {
namespace {
// One authenticated child per controller process. Framed stdin/stdout keep
// secrets out of arguments and amortize process startup across status polling.
class EngineRPC {
 Handle process, input, output, job;
public:
 EngineRPC() {
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
 process.value=pi.hProcess;Handle thread(pi.hThread);CloseHandle(inputRead.value);inputRead.value=nullptr;CloseHandle(outputWrite.value);outputWrite.value=nullptr;

 input.value=inputWrite.value; inputWrite.value=nullptr;
 output.value=outputRead.value; outputRead.value=nullptr;
 JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
 limits.BasicLimitInformation.LimitFlags=JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
 job.value=CreateJobObjectW(nullptr,nullptr);
 if(!job.value||!SetInformationJobObject(job,JobObjectExtendedLimitInformation,&limits,sizeof(limits))||!AssignProcessToJobObject(job,process)){
  TerminateProcess(process,1);throw Error("engine_spawn_failed");
 }

 }
 ~EngineRPC() {
  if(input.value){CloseHandle(input.value);input.value=nullptr;}
  if(process.value && WaitForSingleObject(process,100)!=WAIT_OBJECT_0) TerminateProcess(process,1);
 }
 Json call(const Json& request,const std::atomic_bool& cancel) {
  if(cancel)throw Error("cancelled");
  std::string b=request.dump()+"\n";DWORD written=0;
  if(b.size()>65536||!WriteFile(input,b.data(),DWORD(b.size()),&written,nullptr)||written!=b.size()){
   wipe(b);throw Error("engine_input_failed");
  }
  wipe(b);
  auto deadline=Clock::now()+std::chrono::seconds(25);std::string out;char buffer[4096];
  for(;;){
   if(cancel||Clock::now()>deadline||out.size()>4*maxJson)throw Error(cancel?"cancelled":"engine_timeout");
   DWORD available=0;
   if(!PeekNamedPipe(output,nullptr,0,nullptr,&available,nullptr))throw Error("engine_protocol_failed");
   if(available){
    DWORD n=0;if(!ReadFile(output,buffer,std::min<DWORD>(available,sizeof(buffer)),&n,nullptr)||!n)throw Error("engine_protocol_failed");
    out.append(buffer,n);
    if(out.find('\n')!=std::string::npos)break;
   } else if(WaitForSingleObject(process,20)==WAIT_OBJECT_0)throw Error("engine_protocol_failed");
  }
  try{auto result=Json::parse(out);if(result.contains("error"))throw Error(result["error"].get<std::string>());return result;}
  catch(const Json::exception&){throw Error("engine_protocol_failed");}
 }
};
}
Json engineCall(const Json& request,const std::atomic_bool& cancel) {
 static std::mutex mutex;
 static std::unique_ptr<EngineRPC> engine;
 std::lock_guard lock(mutex);
 try {
  if(cancel)throw Error("cancelled");
  if(!engine)engine=std::make_unique<EngineRPC>();
  auto result=engine->call(request,cancel);
  if(request.value("action","")=="down")engine.reset();
  return result;
 } catch(...) {engine.reset();throw;}
}
}
