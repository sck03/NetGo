#include "connection.hpp"
#include <shellapi.h>

int agentMain(const std::filesystem::path& root,bool test) {
 try {
  WSADATA data{};if(WSAStartup(MAKEWORD(2,2),&data)!=0)return 1;
  if(test)netgo::testPipe(root);
  auto lockName="Local\\NetGoNativeAgent-"+netgo::sid()+(test?"-"+netgo::sha256(netgo::utf8(root.wstring())).substr(0,16):"");
  netgo::Handle mutex(CreateMutexW(nullptr,FALSE,netgo::wide(lockName).c_str()));
  if(!mutex.value)return 1;
  auto ownership=WaitForSingleObject(mutex,0);
  if(ownership!=WAIT_OBJECT_0&&ownership!=WAIT_ABANDONED)return 0;
  netgo::Controller controller(root,!test);std::atomic_bool stop=false;
  // A product restart never resumes a stale privileged engine session.
  // A failed startup cleanup remains visible and blocks connect/shutdown until retried.
  if(!test){
   auto ready=WaitNamedPipeW(L"\\\\.\\pipe\\netgo-netbird",100);
   if(ready||GetLastError()!=ERROR_FILE_NOT_FOUND)try{controller.recoverEngine();}catch(...){}
  }
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
