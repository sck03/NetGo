#include "connection.hpp"
#include <shellapi.h>

int WINAPI wWinMain(HINSTANCE,HINSTANCE,PWSTR,int) {
 try {
  WSADATA data{};if(WSAStartup(MAKEWORD(2,2),&data)!=0)return 1;
  auto root=netgo::dataRoot();int argc=0;auto argv=CommandLineToArgvW(GetCommandLineW(),&argc);
  bool test=false;
  if(argc==3&&std::wstring(argv[1])==L"--test-root"){root=argv[2];test=true;}
  LocalFree(argv);
  if(test)netgo::testPipe(root);
  auto lockName="Local\\NetGoNativeAgent-"+netgo::sid()+(test?"-"+netgo::sha256(netgo::utf8(root.wstring())).substr(0,16):"");
  netgo::Handle mutex(CreateMutexW(nullptr,TRUE,netgo::wide(lockName).c_str()));
  if(GetLastError()==ERROR_ALREADY_EXISTS)return 0;
  netgo::Controller controller(root,!test);std::atomic_bool stop=false;
  // A product restart never resumes a stale privileged engine session.
  if(!test&&WaitNamedPipeW(L"\\\\.\\pipe\\netgo-netbird",100))try{netgo::engineCall({{"action","down"}},stop);}catch(...){}
  netgo::servePipe(stop,[&](const netgo::Json& r){
   if(r.value("action","")=="shutdown"){
    try{controller.disconnect();}catch(const netgo::Error& e){return netgo::Json{{"error",e.what()}};}
    stop=true;return netgo::Json{{"stopped",true}};
   }
   return controller.dispatch(r);
  });
  WSACleanup();return 0;
 }catch(...){return 1;}
}
