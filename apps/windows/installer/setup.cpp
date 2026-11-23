#define UNICODE
#define _UNICODE
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <shellapi.h>
#include <shlobj.h>
#include <setupapi.h>
#include <sddl.h>
#include <objbase.h>
#include <filesystem>
#include <fstream>
#include <stdexcept>
#include <string>
#include <vector>
#include <set>
#include <algorithm>

namespace fs=std::filesystem;
namespace {
struct Handle { HANDLE value=INVALID_HANDLE_VALUE; ~Handle(){if(value!=INVALID_HANDLE_VALUE&&value!=nullptr)CloseHandle(value);} };
std::wstring quote(const std::wstring& s) {
 std::wstring out=L"\"";size_t slashes=0;
 for(auto c:s){if(c==L'\\'){++slashes;continue;}out.append(slashes*(c==L'"'?2:1),L'\\');slashes=0;if(c==L'"')out+=L'\\';out+=c;}
 out.append(slashes*2,L'\\');return out+L'"';
}
std::wstring executable(){std::vector<wchar_t>b(32768);DWORD n=GetModuleFileNameW(nullptr,b.data(),DWORD(b.size()));if(n==0||n>=b.size())throw std::runtime_error("Executable path unavailable");return {b.data(),n};}
std::wstring folder(REFKNOWNFOLDERID id){PWSTR p=nullptr;if(FAILED(SHGetKnownFolderPath(id,0,nullptr,&p)))throw std::runtime_error("System directory unavailable");std::wstring result(p);CoTaskMemFree(p);return result;}
bool elevated(){TOKEN_ELEVATION e{};DWORD size=0;Handle t;if(!OpenProcessToken(GetCurrentProcess(),TOKEN_QUERY,&t.value))return false;return GetTokenInformation(t.value,TokenElevation,&e,sizeof(e),&size)&&e.TokenIsElevated;}
std::wstring sid(){Handle t;if(!OpenProcessToken(GetCurrentProcess(),TOKEN_QUERY,&t.value))throw std::runtime_error("User identity unavailable");DWORD n=0;GetTokenInformation(t.value,TokenUser,nullptr,0,&n);std::vector<BYTE>b(n);if(!GetTokenInformation(t.value,TokenUser,b.data(),n,&n))throw std::runtime_error("User identity unavailable");LPWSTR s=nullptr;if(!ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(b.data())->User.Sid,&s))throw std::runtime_error("User identity unavailable");std::wstring out(s);LocalFree(s);return out;}
std::wstring randomName(){GUID id{};if(FAILED(CoCreateGuid(&id)))throw std::runtime_error("Random identifier unavailable");wchar_t text[40]{};StringFromGUID2(id,text,40);return L"NetGo-setup-"+std::wstring(text);}
void secureDirectory(const fs::path& p){
 PSECURITY_DESCRIPTOR sd=nullptr;
 std::wstring acl=L"D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)";
 if(!elevated())acl+=L"(A;OICI;FA;;;"+sid()+L")";
 if(!ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(),SDDL_REVISION_1,&sd,nullptr))throw std::runtime_error("Directory permissions unavailable");
 SECURITY_ATTRIBUTES sa{sizeof(sa),sd,FALSE};BOOL ok=CreateDirectoryW(p.c_str(),&sa);LocalFree(sd);
 if(!ok)throw std::runtime_error("Cannot create private installation directory");
}
struct Extract { fs::path root;std::set<std::wstring> seen;bool failed=false; };
UINT CALLBACK cabinet(void* context,UINT notification,UINT_PTR p1,UINT_PTR){
 auto& e=*static_cast<Extract*>(context);
 if(notification==SPFILENOTIFY_NEEDNEWCABINET){e.failed=true;return ERROR_INVALID_DATA;}
 if(notification==SPFILENOTIFY_FILEINCABINET){
  auto& f=*reinterpret_cast<FILE_IN_CABINET_INFO_W*>(p1);fs::path rel(f.NameInCabinet);
  if(rel.empty()||rel.is_absolute()||rel.has_root_name()||f.FileSize>128*1024*1024){e.failed=true;return FILEOP_ABORT;}
  for(auto& part:rel){auto s=part.wstring();if(s==L"."||s==L".."||s.find_first_of(L":*?\"<>|")!=std::wstring::npos||s.back()==L'.'||s.back()==L' '){e.failed=true;return FILEOP_ABORT;}}
  auto target=(e.root/rel).lexically_normal();auto name=target.wstring();
  if(name.size()>=MAX_PATH||!e.seen.insert(name).second){e.failed=true;return FILEOP_ABORT;}
  std::error_code error;fs::create_directories(target.parent_path(),error);if(error){e.failed=true;return FILEOP_ABORT;}
  wcscpy_s(f.FullTargetName,MAX_PATH,name.c_str());return FILEOP_DOIT;
 }
 if(notification==SPFILENOTIFY_FILEEXTRACTED){auto& f=*reinterpret_cast<FILEPATHS_W*>(p1);if(f.Win32Error){e.failed=true;return f.Win32Error;}}
 return NO_ERROR;
}
void extract(const fs::path& root){
 HRSRC r=FindResourceW(nullptr,MAKEINTRESOURCEW(201),RT_RCDATA);if(!r)throw std::runtime_error("Installation payload missing; download the complete Setup.exe");
 DWORD size=SizeofResource(nullptr,r);HGLOBAL loaded=LoadResource(nullptr,r);const char* data=static_cast<const char*>(LockResource(loaded));
 if(!data||size<36||size>256*1024*1024)throw std::runtime_error("Invalid installation payload");
 auto cab=root/L"payload.cab";{std::ofstream f(cab,std::ios::binary);f.write(data,size);if(!f)throw std::runtime_error("Cannot write installation payload");}
 Extract state{root,{},false};if(!SetupIterateCabinetW(cab.c_str(),0,cabinet,&state)||state.failed)throw std::runtime_error("Installation payload extraction failed");
 fs::remove(cab);
}
DWORD powershell(const fs::path& script,const std::vector<std::wstring>& args,const fs::path& log){
 auto ps=folder(FOLDERID_System)+L"\\WindowsPowerShell\\v1.0\\powershell.exe";
 // Do not inherit PowerShell 7's module search path into Windows PowerShell 5.1.
 // Restrict module loading to the OS-owned directory for elevated installation.
 std::vector<wchar_t> environment;std::vector<std::wstring> variables;auto inherited=GetEnvironmentStringsW();
 if(!inherited)throw std::runtime_error("Process environment unavailable");
 for(auto p=inherited;*p;p+=wcslen(p)+1){if(_wcsnicmp(p,L"PSModulePath=",13)==0)continue;variables.emplace_back(p);}
 FreeEnvironmentStringsW(inherited);
 auto modules=L"PSModulePath="+folder(FOLDERID_System)+L"\\WindowsPowerShell\\v1.0\\Modules";
 variables.push_back(modules);std::sort(variables.begin(),variables.end(),[](const auto& a,const auto& b){return _wcsicmp(a.c_str(),b.c_str())<0;});
 for(const auto& v:variables){environment.insert(environment.end(),v.begin(),v.end());environment.push_back(0);}environment.push_back(0);
 std::wstring command=quote(ps)+L" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "+quote(script.wstring());for(auto& arg:args)command+=L" "+quote(arg);
 SECURITY_ATTRIBUTES sa{sizeof(sa),nullptr,TRUE};Handle output;output.value=CreateFileW(log.c_str(),GENERIC_WRITE,FILE_SHARE_READ,&sa,CREATE_ALWAYS,FILE_ATTRIBUTE_NORMAL,nullptr);
 if(output.value==INVALID_HANDLE_VALUE)throw std::runtime_error("Cannot open installation log");
 Handle input;input.value=CreateFileW(L"NUL",GENERIC_READ,FILE_SHARE_READ|FILE_SHARE_WRITE,&sa,OPEN_EXISTING,0,nullptr);
 STARTUPINFOW startup{};startup.cb=sizeof(startup);startup.dwFlags=STARTF_USESTDHANDLES;startup.hStdOutput=output.value;startup.hStdError=output.value;startup.hStdInput=input.value;PROCESS_INFORMATION pi{};
 if(!CreateProcessW(ps.c_str(),command.data(),nullptr,nullptr,TRUE,CREATE_NO_WINDOW|CREATE_UNICODE_ENVIRONMENT,environment.data(),script.parent_path().c_str(),&startup,&pi))throw std::runtime_error("Cannot start Windows installation engine");
 Handle process{pi.hProcess},thread{pi.hThread};
 // Dispatch window messages while waiting; no unbounded UI-thread blocking.
 while(MsgWaitForMultipleObjects(1,&process.value,FALSE,100,QS_ALLINPUT)!=WAIT_OBJECT_0){MSG msg;while(PeekMessageW(&msg,nullptr,0,0,PM_REMOVE)){TranslateMessage(&msg);DispatchMessageW(&msg);}}
 DWORD code=1;GetExitCodeProcess(process.value,&code);return code;
}
int elevate(bool uninstall,bool network){
 auto exe=executable();auto parameters=std::wstring(uninstall?L"--remove ":network?L"--network ":L"--install ")+quote(sid());SHELLEXECUTEINFOW info{};info.cbSize=sizeof(info);info.fMask=SEE_MASK_NOCLOSEPROCESS;info.lpVerb=L"runas";info.lpFile=exe.c_str();info.lpParameters=parameters.c_str();info.nShow=SW_SHOWNORMAL;
 if(!ShellExecuteExW(&info)){if(GetLastError()==ERROR_CANCELLED)return 2;throw std::runtime_error("Windows could not start the installer");}Handle process{info.hProcess};return 0;
}
}
int WINAPI wWinMain(HINSTANCE instance,HINSTANCE,LPWSTR,int){
 CoInitializeEx(nullptr,COINIT_APARTMENTTHREADED);int argc=0;LPWSTR* raw=CommandLineToArgvW(GetCommandLineW(),&argc);std::vector<std::wstring> args;for(int i=1;i<argc;i++)args.emplace_back(raw[i]);LocalFree(raw);
 bool check=!args.empty()&&(args[0]==L"--check"||args[0]==L"--check-network");fs::path stage;bool retainStage=false;
 try{
  bool staged=!args.empty()&&args[0]==L"--remove-staged";
  bool remove=staged||(!args.empty()&&(args[0]==L"--uninstall"||args[0]==L"--remove"));
  bool network=!args.empty()&&(args[0]==L"--prepare-network"||args[0]==L"--network"||args[0]==L"--check-network");
#ifdef NETGO_DEFAULT_NETWORK
  if(args.empty()||check)network=true;
#endif
  bool worker=staged||(!args.empty()&&(args[0]==L"--install"||args[0]==L"--remove"||args[0]==L"--network"));
  if(!check&&!worker){if(!args.empty()&&args[0]!=L"--uninstall"&&args[0]!=L"--prepare-network")throw std::runtime_error("Unknown installer option");MSGBOXPARAMSW box{};box.cbSize=sizeof(box);box.hInstance=instance;box.lpszCaption=L"NetGo";box.lpszText=remove?L"是否卸载 NetGo？\n\n将停止网络连接并移除程序。站点配置会保留。":network?L"准备 NetGo 网络服务\n\n绿色版程序继续从当前目录运行。VPN 需要安装系统网络服务。\nWindows 将请求管理员权限。":L"安装 NetGo\n\n安装原生桌面客户端及网络服务，并创建开始菜单快捷方式。\nWindows 将请求管理员权限。";box.dwStyle=MB_YESNO|MB_USERICON;box.lpszIcon=MAKEINTRESOURCEW(101);if(MessageBoxIndirectW(&box)!=IDYES)return 2;return elevate(remove,network);}
  if(worker&&(!elevated()||args.size()!=(staged?3u:2u)||args[1]!=sid()))throw std::runtime_error("请使用当前桌面账号提升管理员权限；不支持在提示中切换到其他账号。");
  if(staged){
   stage=fs::path(executable()).parent_path();retainStage=true;
   if(fs::path(executable()).filename()!=L"NetGo-Uninstall.exe"||stage.filename().wstring().find(L"NetGo-setup-{")!=0)throw std::runtime_error("Invalid removal worker location");
   size_t end=0;auto pid=std::stoul(args[2],&end);if(end!=args[2].size()||pid>MAXDWORD||pid==GetCurrentProcessId())throw std::runtime_error("Invalid removal parent");
   Handle parent{OpenProcess(SYNCHRONIZE,FALSE,DWORD(pid))};if(parent.value&&WaitForSingleObject(parent.value,30000)!=WAIT_OBJECT_0)throw std::runtime_error("Removal parent did not exit");
  }
  else{wchar_t temp[32768]{};if(!GetTempPathW(32768,temp))throw std::runtime_error("Temporary directory unavailable");stage=fs::path(temp)/randomName();secureDirectory(stage);}
  if(remove&&!staged){
   auto helper=stage/L"NetGo-Uninstall.exe";fs::copy_file(executable(),helper);
   auto command=quote(helper.wstring())+L" --remove-staged "+quote(sid())+L" "+std::to_wstring(GetCurrentProcessId());STARTUPINFOW si{};si.cb=sizeof(si);PROCESS_INFORMATION pi{};
   if(!CreateProcessW(helper.c_str(),command.data(),nullptr,nullptr,FALSE,0,nullptr,stage.c_str(),&si,&pi))throw std::runtime_error("Cannot start removal worker");
   Handle process{pi.hProcess},thread{pi.hThread};return 0;
  }
  if(remove){auto script=fs::path(folder(FOLDERID_ProgramFiles))/L"NetGoNative/maintenance/deploy/windows/uninstall.ps1";if(powershell(script,{},stage/L"uninstall.log")!=0)throw std::runtime_error("卸载未完成，请查看日志。已保留恢复所需文件。");}
  else{extract(stage);std::vector<std::wstring> options{L"-PackageRoot",stage.wstring()};if(check)options.push_back(L"-Plan");if(network)options.push_back(L"-NetworkOnly");if(powershell(stage/L"deploy/windows/install.ps1",options,stage/L"install.log")!=0)throw std::runtime_error("安装检查或安装失败，请查看日志。原安装已按事务记录尝试恢复。");}
  // Only the random directory created by this invocation is removed.
  std::error_code error;if(!retainStage)fs::remove_all(stage,error);
  if(retainStage){
   fs::remove(stage/L"uninstall.log",error);
   // Only our running temporary image remains locked; schedule these exact paths.
   MoveFileExW(executable().c_str(),nullptr,MOVEFILE_DELAY_UNTIL_REBOOT);
   MoveFileExW(stage.c_str(),nullptr,MOVEFILE_DELAY_UNTIL_REBOOT);
  }
  if(!check)MessageBoxW(nullptr,remove?L"NetGo 已卸载。":network?L"网络服务已就绪。\n请返回绿色版 NetGo 连接站点。":L"NetGo 安装完成。\n请从开始菜单启动 NetGo。",L"NetGo",MB_OK|MB_ICONINFORMATION);
  return 0;
 }catch(const std::exception& e){
  int n=MultiByteToWideChar(CP_UTF8,0,e.what(),-1,nullptr,0);std::wstring text(n,L'\0');MultiByteToWideChar(CP_UTF8,0,e.what(),-1,text.data(),n);if(!text.empty())text.pop_back();if(!stage.empty())text+=L"\n\n日志目录："+stage.wstring();if(!check)MessageBoxW(nullptr,text.c_str(),L"NetGo",MB_OK|MB_ICONERROR);else{OutputDebugStringW(text.c_str());std::ofstream out(fs::path(executable()).parent_path()/L"setup-check-error.txt");out<<e.what()<<"\n";auto encoded=stage.u8string();out.write(reinterpret_cast<const char*>(encoded.data()),std::streamsize(encoded.size()));}return 1;
 }
}
