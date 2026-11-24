#include "entry.hpp"
#include <shellapi.h>
int WINAPI wWinMain(HINSTANCE instance,HINSTANCE,PWSTR command,int show){
 int argc=0;auto raw=CommandLineToArgvW(GetCommandLineW(),&argc);if(!raw)return 2;
 std::vector<std::wstring> args;for(int i=1;i<argc;++i)args.emplace_back(raw[i]);LocalFree(raw);
 try{
  if(args.size()==1&&args[0]==L"--version"){
   auto text=netgo::Json({{"version",netgo::appVersion},{"platform","windows"},{"arch","amd64"}}).dump()+"\n";
   DWORD count=0;return WriteFile(GetStdHandle(STD_OUTPUT_HANDLE),text.data(),DWORD(text.size()),&count,nullptr)&&count==text.size()?0:1;
  }
  if(args.empty()||(args.size()==1&&args[0]==L"--tray"))return desktopMain(instance,command,show);
  if(args[0]==L"--agent"){
   if(args.size()==1)return agentMain(netgo::dataRoot(),false);
   if(args.size()==3&&args[1]==L"--test-root")return agentMain(args[2],true);
   return 2;
  }
  if(args.size()==3&&args[0]==L"--remote"&&args[1]==L"--site")return remoteMain(instance,show,args[2]);
  return 2;
 }catch(...){return 1;}
}
