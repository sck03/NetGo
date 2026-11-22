#include "netgo.hpp"
#include <commctrl.h>
#include <commdlg.h>
#include <shellapi.h>
#include <uxtheme.h>
#include <condition_variable>
#include <deque>
#include <map>

using namespace netgo;
namespace {
constexpr UINT resultMessage=WM_APP+1,trayMessage=WM_APP+2;
enum { Servers=100,Alias,Password,Remember,Replace,Connect,Import,Export,Edit,Remove,Info,Resources,StatusText,Detail,Log,MenuNew=200,MenuImport,MenuExport,MenuEdit,MenuDelete,MenuForget,MenuExit,MenuShow,MenuDisconnect,MenuAutoStart,MenuAutoConnect,MenuTray,MenuAbout };
struct Result{std::string action,site;Json value;std::string error;};
HWND window=nullptr;HFONT font=nullptr,titleFont=nullptr;HBRUSH background=nullptr,sidebar=nullptr;
UINT dpi=96;
int scale(int value){return MulDiv(value,int(dpi),96);}
void fonts(){
 HFONT old=font,oldTitle=titleFont;
 font=CreateFontW(-scale(16),0,0,0,FW_NORMAL,FALSE,FALSE,FALSE,DEFAULT_CHARSET,OUT_DEFAULT_PRECIS,CLIP_DEFAULT_PRECIS,CLEARTYPE_QUALITY,DEFAULT_PITCH,L"Microsoft YaHei UI");
 titleFont=CreateFontW(-scale(30),0,0,0,FW_SEMIBOLD,FALSE,FALSE,FALSE,DEFAULT_CHARSET,OUT_DEFAULT_PRECIS,CLIP_DEFAULT_PRECIS,CLEARTYPE_QUALITY,DEFAULT_PITCH,L"Microsoft YaHei UI");
 if(old)DeleteObject(old);
 if(oldTitle)DeleteObject(oldTitle);
}
std::map<int,HWND> controls;Json sites=Json::array(),currentStatus=Json::object(),preferences=Json::object();
std::string chosen;bool saved=false,exiting=false,firstLoad=true;std::atomic_bool closing=false,polling=false;
std::mutex queueMutex;std::condition_variable queueReady;std::deque<Json> jobs;std::thread pump;
std::wstring text(HWND h){int n=GetWindowTextLengthW(h);std::wstring s(n+1,L'\0');GetWindowTextW(h,s.data(),n+1);s.resize(n);return s;}
std::wstring errorText(const std::string& e) {
 static const std::map<std::string,std::wstring> messages={
  {"agent_unavailable",L"连接后台未启动。请重新打开客户端，或重新安装程序。"},
  {"engine_missing",L"缺少 NetBird 网络核心。请使用完整安装包。"},
  {"engine_rpc_failed",L"无法操作网络服务。请确认已安装本套装的 NetBird 服务并授权当前 Windows 用户。"},
  {"engine_cleanup_failed",L"尚未确认网络服务已断开。请恢复 NetGoNetBird 服务后再次断开，暂时不要切换服务器。"},
  {"engine_connect_timeout",L"网络服务未在规定时间内连接。请检查服务端 Management、Signal 和网络核心日志后重试。"},
  {"no_trusted_control_entry",L"未找到可验证的服务器入口。请检查服务器地址、网络和证书。"},
  {"tls_certificate_failed",L"服务器证书验证失败。请核对域名、公共 CA 和系统时间。"},
  {"unauthorized",L"账号认证失败或已失效，请检查账号密码。"},
  {"forbidden",L"账号没有访问权限或已禁用。"},
  {"session_conflict",L"该账号已有连接。如需替换，请勾选“替换旧连接”后重试。"},
  {"lease_expired",L"无法续期，连接已断开。请检查服务器入口后重新连接。"},
  {"route_conflict",L"当前局域网与远端网段重叠。请联系管理员调整网段或使用单主机资源。"},
  {"credentials_required",L"请填写账号和密码；已记住密码时可留空。"},
  {"site_trust_changed_use_new_id",L"该站点的域名或 CA 已改变。请使用新的站点 ID，重新核对服务器身份。"},
  {"profile_revision_stale",L"站点配置版本过旧。请向管理员取得最新站点文件。"},
  {"profile_revision_conflict",L"同一版本的站点配置内容不一致。请由管理员递增配置版本后重新导出。"},
  {"protocol_incompatible",L"客户端与服务端协议不兼容，请更新为匹配版本后重新连接。"},
  {"disconnect_before_edit",L"请先断开连接，再修改服务器或清理凭据。"},
 };
 auto i=messages.find(e);return i!=messages.end()?i->second:L"操作未完成（"+wide(e)+L"）。";
}
std::wstring stateText(const std::string& s) {
 static const std::map<std::string,std::wstring> names={{"disconnected",L"未连接"},{"disconnecting",L"正在断开"},{"probing",L"正在寻找服务器"},{"authenticating",L"正在验证账号"},{"negotiating",L"正在建立数据连接"},{"connected",L"已连接"},{"recovering",L"正在恢复连接"},{"failed",L"连接失败"}};
 auto i=names.find(s);return i==names.end()?L"等待状态":i->second;
}
void submit(Json request){std::lock_guard lock(queueMutex);jobs.push_back(std::move(request));queueReady.notify_one();}
void set(int id,const std::wstring& s){SetWindowTextW(controls.at(id),s.c_str());}
void startPump(){
 pump=std::thread([]{
  while(!closing){
   Json job;
   {std::unique_lock lock(queueMutex);queueReady.wait(lock,[]{return closing||!jobs.empty();});if(closing)break;job=std::move(jobs.front());jobs.pop_front();}
   auto r=std::make_unique<Result>();r->action=job.at("action");r->site=job.value("site","");
   try{r->value=pipeCall(job);}catch(const std::exception& e){r->error=e.what();}
   job=Json::object();
   if(!PostMessageW(window,resultMessage,0,reinterpret_cast<LPARAM>(r.get())))continue;
   r.release();
  }
 });
}
std::filesystem::path fileDialog(bool save,const wchar_t* title) {
 wchar_t path[32768]{};OPENFILENAMEW f{};f.lStructSize=sizeof(f);f.hwndOwner=window;f.lpstrFile=path;f.nMaxFile=32768;
 f.lpstrFilter=L"NetGo 站点或诊断 (*.json)\0*.json\0所有文件\0*.*\0";f.lpstrDefExt=L"json";f.lpstrTitle=title;
 f.Flags=OFN_NOCHANGEDIR|OFN_PATHMUSTEXIST|(save?OFN_OVERWRITEPROMPT:OFN_FILEMUSTEXIST);
 if(save?GetSaveFileNameW(&f):GetOpenFileNameW(&f))return path;
 return {};
}
const Json* selected(){for(const auto& p:sites)if(p.at("ID")==chosen)return &p;return nullptr;}
struct Editor {HWND edit=nullptr;Json value;bool accepted=false,done=false;};
LRESULT CALLBACK editorProc(HWND h,UINT m,WPARAM w,LPARAM l){
 auto data=reinterpret_cast<Editor*>(GetWindowLongPtrW(h,GWLP_USERDATA));
 if(m==WM_NCCREATE){data=static_cast<Editor*>(reinterpret_cast<CREATESTRUCTW*>(l)->lpCreateParams);SetWindowLongPtrW(h,GWLP_USERDATA,reinterpret_cast<LONG_PTR>(data));}
 if(m==WM_COMMAND&&LOWORD(w)==IDOK){
  try{auto p=Json::parse(utf8(text(data->edit)));validateProfile(p);data->value=std::move(p);data->accepted=true;DestroyWindow(h);}
  catch(const std::exception&){MessageBoxW(h,L"配置无效。请核对站点 ID、HTTPS 域名、直达入口、路由节点 IP 和公共 CA；文件不应包含账号密码。",L"检查站点配置",MB_ICONWARNING);}
  return 0;
 }
 if(m==WM_COMMAND&&LOWORD(w)==IDCANCEL){DestroyWindow(h);return 0;}
 if(m==WM_CLOSE){DestroyWindow(h);return 0;}
 if(m==WM_DESTROY){data->done=true;return 0;}return DefWindowProcW(h,m,w,l);
}
void editProfile(bool create) {
 Json p=create?Json{{"Protocol",1},{"ID","site1"},{"Name","我的服务器"},{"Origin","https://control.example.com:10443"},{"Candidates",Json::array({{{"ID","direct"},{"Address","control.example.com:10443"},{"Kind","direct"}}})},{"GatewayPeerID",""},{"GatewayIP","100.96.0.1"},{"CA",""},{"LocalRelay","relay.example.com"},{"AuxiliaryRelay",""}}:(selected()?*selected():Json());
 if(p.is_null())return;
 WNDCLASSW c{};c.lpfnWndProc=editorProc;c.hInstance=GetModuleHandleW(nullptr);c.lpszClassName=L"NetGoProfileEditor";c.hCursor=LoadCursorW(nullptr,IDC_ARROW);c.hbrBackground=reinterpret_cast<HBRUSH>(COLOR_WINDOW+1);RegisterClassW(&c);
 Editor data;auto h=CreateWindowExW(WS_EX_DLGMODALFRAME,c.lpszClassName,create?L"新建服务器配置":L"编辑服务器配置",WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU,200,120,720,590,window,nullptr,c.hInstance,&data);
 HWND label=CreateWindowW(L"STATIC",L"建议从管理员导入配置。高级编辑用于维护域名、直达 / 备用入口及公共 CA。\n导出仅包含连接信息；域名或 CA 改变时，请使用新的站点 ID。",WS_CHILD|WS_VISIBLE,20,16,670,44,h,nullptr,c.hInstance,nullptr);SendMessageW(label,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);
 data.edit=CreateWindowExW(WS_EX_CLIENTEDGE,L"EDIT",wide(p.dump(2)).c_str(),WS_CHILD|WS_VISIBLE|WS_VSCROLL|WS_HSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_AUTOHSCROLL,20,72,670,410,h,nullptr,c.hInstance,nullptr);
 SendMessageW(data.edit,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);SendMessageW(data.edit,EM_SETLIMITTEXT,maxJson,0);
 for(auto pair:{std::pair{IDOK,L"保存配置"},std::pair{IDCANCEL,L"取消"}}){auto b=CreateWindowW(L"BUTTON",pair.second,WS_CHILD|WS_VISIBLE|WS_TABSTOP,pair.first==IDOK?474:584,498,104,34,h,reinterpret_cast<HMENU>(intptr_t(pair.first)),c.hInstance,nullptr);SendMessageW(b,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);}
 EnableWindow(window,FALSE);ShowWindow(h,SW_SHOW);MSG msg{};
 while(!data.done&&GetMessageW(&msg,nullptr,0,0)>0){if(!IsDialogMessageW(h,&msg)){TranslateMessage(&msg);DispatchMessageW(&msg);}}
 EnableWindow(window,TRUE);SetForegroundWindow(window);if(data.accepted){chosen=data.value.at("ID");submit({{"action","import"},{"profile",data.value}});}
}
void logsDialog(const Json& events) {
 std::wstring value;
 for(const auto& e:events){std::time_t t=e.value("time",std::time_t(0));std::tm local{};localtime_s(&local,&t);wchar_t time[64];wcsftime(time,64,L"%m-%d %H:%M:%S",&local);value+=time;value+=L"  "+stateText(e.value("state",""));auto error=e.value("error","");if(!error.empty())value+=L" · "+errorText(error);value+=L"\r\n";}
 if(value.empty())value=L"暂无连接记录。";
 WNDCLASSW c{};c.lpfnWndProc=DefWindowProcW;c.hInstance=GetModuleHandleW(nullptr);c.lpszClassName=L"NetGoLogs";c.hCursor=LoadCursorW(nullptr,IDC_ARROW);c.hbrBackground=reinterpret_cast<HBRUSH>(COLOR_WINDOW+1);RegisterClassW(&c);
 auto h=CreateWindowW(c.lpszClassName,L"NetGo · 连接日志",WS_OVERLAPPEDWINDOW,220,140,760,440,window,nullptr,c.hInstance,nullptr);
 auto edit=CreateWindowExW(WS_EX_CLIENTEDGE,L"EDIT",value.c_str(),WS_CHILD|WS_VISIBLE|WS_VSCROLL|ES_MULTILINE|ES_READONLY|ES_AUTOVSCROLL,12,12,716,370,h,nullptr,c.hInstance,nullptr);SendMessageW(edit,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);ShowWindow(h,SW_SHOW);
}
void persist(){secureDirectory(dataRoot());atomicFile(dataRoot()/L".preferences",preferences.dump(2));}
void startup(bool enabled){
 HKEY key=nullptr;if(RegCreateKeyExW(HKEY_CURRENT_USER,L"Software\\Microsoft\\Windows\\CurrentVersion\\Run",0,nullptr,0,KEY_SET_VALUE,nullptr,&key,nullptr)!=ERROR_SUCCESS)throw Error("startup_setting_failed");
 if(enabled){std::wstring value=L"\""+(executableDir()/L"netgo-desktop.exe").wstring()+L"\" --tray";auto e=RegSetValueExW(key,L"NetGoNative",0,REG_SZ,reinterpret_cast<const BYTE*>(value.c_str()),DWORD((value.size()+1)*sizeof(wchar_t)));RegCloseKey(key);if(e!=ERROR_SUCCESS)throw Error("startup_setting_failed");}
 else{RegDeleteValueW(key,L"NetGoNative");RegCloseKey(key);}
}
void tray(bool add) {
 NOTIFYICONDATAW n{};n.cbSize=sizeof(n);n.hWnd=window;n.uID=1;n.uFlags=NIF_ICON|NIF_MESSAGE|NIF_TIP;n.uCallbackMessage=trayMessage;n.hIcon=LoadIconW(nullptr,IDI_APPLICATION);
 std::wstring tip=L"NetGo · "+stateText(currentStatus.value("state","disconnected"));wcsncpy_s(n.szTip,tip.c_str(),_TRUNCATE);Shell_NotifyIconW(add?NIM_ADD:NIM_MODIFY,&n);
}
void refreshSelection() {
 const auto* p=selected();set(Detail,p?wide(p->value("Name",""))+L"\n"+wide(p->value("Origin","")):L"导入管理员提供的服务器配置，即可开始连接");
 SendMessageW(controls[Servers],LB_SETCURSEL,-1,0);
 for(size_t i=0;i<sites.size();++i)if(sites[i]["ID"]==chosen)SendMessageW(controls[Servers],LB_SETCURSEL,i,0);
 set(Password,L"");saved=false;set(Alias,L"");SendMessageW(controls[Remember],BM_SETCHECK,BST_UNCHECKED,0);
 if(p)submit({{"action","credential-info"},{"site",chosen}});
 for(int id:{Connect,Export,Edit,Remove})EnableWindow(controls[id],p!=nullptr);
}
void drawStatus(Json status) {
 currentStatus=std::move(status);auto s=currentStatus.value("state","disconnected");set(StatusText,stateText(s));
 bool active=(s!="disconnected"&&s!="failed")||currentStatus.value("error","")=="engine_cleanup_failed";set(Connect,active?L"断开连接":L"连接局域网");EnableWindow(controls[Servers],!active);EnableWindow(controls[Alias],!active);EnableWindow(controls[Password],!active);EnableWindow(controls[Remember],!active);EnableWindow(controls[Replace],!active);EnableWindow(controls[Edit],!active&&selected()!=nullptr);EnableWindow(controls[Remove],!active&&selected()!=nullptr);
 auto d=currentStatus.value("data",Json::object());auto path=d.value("path","negotiating");std::wstring route=path=="direct"?L"直连":path=="local_relay"?L"本地中继":path=="auxiliary_relay"?L"VPS 中继":path=="relay_unknown"?L"中继（待识别）":L"等待连接";
 auto control=currentStatus.value("control","");std::wstring entrance=control=="direct"?L"服务器直达入口":control=="auxiliary"?L"VPS 备用入口":L"等待探测";
 auto candidate=currentStatus.value("control_candidate","");if(!candidate.empty())entrance+=L"（"+wide(candidate)+L"）";
 std::wstring details=L"控制入口   "+entrance+L"\r\n\r\n数据路径   "+route+L"\r\n\r\n虚拟地址   "+wide(d.value("ip","—"))+L"\r\n\r\n接收 / 发送   "+std::to_wstring(d.value("rx",int64_t(0))/1024)+L" / "+std::to_wstring(d.value("tx",int64_t(0))/1024)+L" KB";
 auto error=currentStatus.value("error","");if(!error.empty())details+=L"\r\n\r\n"+errorText(error);set(Info,details);
 ListView_DeleteAllItems(controls[Resources]);
 auto lease=currentStatus.value("lease",Json::object());int row=0;
 for(const auto& r:lease.value("resources",Json::array())){
  std::wstring name=wide(r.value("name",""));LVITEMW item{};item.mask=LVIF_TEXT;item.iItem=row;item.pszText=name.data();ListView_InsertItem(controls[Resources],&item);
  std::wstring cidr=wide(r.value("cidr","")),protocol=wide(r.value("protocol",""));ListView_SetItemText(controls[Resources],row,1,cidr.data());ListView_SetItemText(controls[Resources],row,2,protocol.data());++row;
 }
 tray(false);
}
void command(int id) {
 switch(id) {
 case MenuShow:ShowWindow(window,SW_RESTORE);SetForegroundWindow(window);break;
 case Connect:{auto s=currentStatus.value("state","disconnected");if((s!="disconnected"&&s!="failed")||currentStatus.value("error","")=="engine_cleanup_failed"){submit({{"action","disconnect"}});break;}
  std::string password=utf8(text(controls[Password]));submit({{"action","connect"},{"site",chosen},{"alias",utf8(text(controls[Alias]))},{"password",password},{"remember",SendMessageW(controls[Remember],BM_GETCHECK,0,0)==BST_CHECKED},{"replace",SendMessageW(controls[Replace],BM_GETCHECK,0,0)==BST_CHECKED}});
  wipe(password);set(Password,L"");preferences["selected"]=chosen;persist();break;}
 case MenuDisconnect:submit({{"action","disconnect"}});break;
 case Import:case MenuImport:{auto file=fileDialog(false,L"导入管理员提供的站点");if(!file.empty()){auto p=Json::parse(readFile(file));validateProfile(p);chosen=p["ID"];submit({{"action","import"},{"profile",p}});}break;}
 case Export:case MenuExport:{if(!selected())break;auto file=fileDialog(true,L"导出公共站点配置");if(!file.empty())atomicFile(file,selected()->dump(2));break;}
 case Edit:case MenuEdit:editProfile(false);break;
 case MenuNew:editProfile(true);break;
 case Remove:case MenuDelete:if(!selected())break;if(MessageBoxW(window,L"删除此服务器配置及本用户保存的密码、设备密钥？",L"删除服务器",MB_YESNO|MB_ICONQUESTION)==IDYES)submit({{"action","delete"},{"site",chosen}});break;
 case MenuForget:if(selected())submit({{"action","forget"},{"site",chosen}});break;
 case Log:submit({{"action","logs"}});break;
 case MenuAutoStart:{bool enabled=!preferences.value("startup",false);startup(enabled);preferences["startup"]=enabled;persist();CheckMenuItem(GetMenu(window),MenuAutoStart,MF_BYCOMMAND|(enabled?MF_CHECKED:MF_UNCHECKED));break;}
 case MenuAutoConnect:{bool enabled=!preferences.value("auto_connect",false);preferences["auto_connect"]=enabled;persist();CheckMenuItem(GetMenu(window),MenuAutoConnect,MF_BYCOMMAND|(enabled?MF_CHECKED:MF_UNCHECKED));break;}
 case MenuTray:{bool enabled=!preferences.value("close_to_tray",true);preferences["close_to_tray"]=enabled;persist();CheckMenuItem(GetMenu(window),MenuTray,MF_BYCOMMAND|(enabled?MF_CHECKED:MF_UNCHECKED));break;}
 case MenuAbout:MessageBoxW(window,L"NetGo 0.2.3-dev\n原生 C++ / Win32 客户端\n网络核心：NetBird\n登录入口和数据路径分别显示。\n\n诊断导出不包含密码、令牌或密钥。",L"关于 NetGo",MB_OK);break;
 case MenuExit:exiting=true;EnableWindow(window,FALSE);submit({{"action","shutdown"}});break;
 default:if(id==300){submit({{"action","diagnostic"}});}else if(id==301)submit({{"action","clear-logs"}});break;
 }
}
HWND control(const wchar_t* cls,const wchar_t* caption,int id,DWORD style=0,DWORD extra=0) {
 auto h=CreateWindowExW(extra,cls,caption,WS_CHILD|WS_VISIBLE|style,0,0,10,10,window,reinterpret_cast<HMENU>(intptr_t(id)),GetModuleHandleW(nullptr),nullptr);
 SendMessageW(h,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);controls[id]=h;return h;
}
void layout(){
 RECT r{};GetClientRect(window,&r);int width=MulDiv(r.right,96,int(dpi)),height=MulDiv(r.bottom,96,int(dpi));auto move=[](int id,int x,int y,int w,int h){MoveWindow(controls[id],scale(x),scale(y),scale(w),scale(h),TRUE);};
 move(Servers,18,146,226,height-265);move(Import,18,height-102,108,34);move(Export,136,height-102,108,34);move(Edit,18,height-58,108,34);move(Remove,136,height-58,108,34);
 int x=286,right=width-30,mid=x+(right-x)/2+18;
 move(StatusText,x,36,right-x,38);move(Detail,x,86,right-x,52);
 move(400,x,159,300,26);move(401,x,198,260,22);move(Alias,x,224,mid-x-44,34);
 move(402,x,274,260,22);move(Password,x,302,mid-x-44,34);move(Remember,x,346,250,25);move(Replace,x,380,260,25);move(Connect,x,422,mid-x-44,42);
 move(403,mid,159,right-mid,26);move(Info,mid,205,right-mid,260);
 move(404,x,496,300,25);move(Resources,x,532,right-x,std::max(74,height-618));
 move(Log,x,height-60,112,34);move(300,x+124,height-60,134,34);move(301,x+270,height-60,112,34);
 ListView_SetColumnWidth(controls[Resources],0,scale(220));ListView_SetColumnWidth(controls[Resources],1,scale(240));ListView_SetColumnWidth(controls[Resources],2,scale(110));
}
void createUI(){
 dpi=GetDpiForWindow(window);fonts();
 background=CreateSolidBrush(RGB(255,255,255));sidebar=CreateSolidBrush(RGB(243,246,249));
 HMENU menu=CreateMenu(),config=CreatePopupMenu(),view=CreatePopupMenu(),options=CreatePopupMenu();
 for(auto p:{std::pair{MenuNew,L"新建配置"},std::pair{MenuImport,L"导入站点…"},std::pair{MenuExport,L"导出配置…"},std::pair{MenuEdit,L"编辑配置…"},std::pair{MenuDelete,L"删除服务器"},std::pair{MenuForget,L"忘记保存的密码"},std::pair{MenuExit,L"退出"}})AppendMenuW(config,MF_STRING,p.first,p.second);
 AppendMenuW(view,MF_STRING,Log,L"查看日志");AppendMenuW(view,MF_STRING,300,L"导出诊断…");
 AppendMenuW(options,MF_STRING|(preferences.value("startup",false)?MF_CHECKED:0),MenuAutoStart,L"登录 Windows 后启动");
 AppendMenuW(options,MF_STRING|(preferences.value("auto_connect",false)?MF_CHECKED:0),MenuAutoConnect,L"启动后使用已保存凭据连接");
 AppendMenuW(options,MF_STRING|(preferences.value("close_to_tray",true)?MF_CHECKED:0),MenuTray,L"关闭窗口时保留托盘");
 AppendMenuW(menu,MF_POPUP,reinterpret_cast<UINT_PTR>(config),L"配置");AppendMenuW(menu,MF_POPUP,reinterpret_cast<UINT_PTR>(view),L"查看");AppendMenuW(menu,MF_POPUP,reinterpret_cast<UINT_PTR>(options),L"选项");AppendMenuW(menu,MF_STRING,MenuAbout,L"关于");SetMenu(window,menu);
 control(L"LISTBOX",L"",Servers,WS_TABSTOP|WS_VSCROLL|LBS_NOTIFY|LBS_NOINTEGRALHEIGHT,WS_EX_CLIENTEDGE);
 const std::pair<int,const wchar_t*> buttons[]={{Import,L"导入站点"},{Export,L"导出配置"},{Edit,L"编辑"},{Remove,L"删除"},{Log,L"查看日志"},{300,L"导出诊断"},{301,L"清空日志"}};
 for(auto p:buttons)control(L"BUTTON",p.second,p.first,WS_TABSTOP);
 control(L"STATIC",L"未连接",StatusText);SendMessageW(controls[StatusText],WM_SETFONT,reinterpret_cast<WPARAM>(titleFont),TRUE);
 control(L"STATIC",L"导入管理员提供的服务器配置，即可开始连接",Detail);
 control(L"STATIC",L"连接到服务器",400);
 control(L"STATIC",L"账号",401);
 control(L"EDIT",L"",Alias,WS_TABSTOP|ES_AUTOHSCROLL,WS_EX_CLIENTEDGE);
 control(L"STATIC",L"密码",402);
 control(L"EDIT",L"",Password,WS_TABSTOP|ES_AUTOHSCROLL|ES_PASSWORD,WS_EX_CLIENTEDGE);
 SendMessageW(controls[Alias],EM_SETLIMITTEXT,256,0);SendMessageW(controls[Password],EM_SETLIMITTEXT,2048,0);
 SendMessageW(controls[Password],EM_SETCUEBANNER,0,reinterpret_cast<LPARAM>(L"输入密码，已记住时可留空"));
 control(L"BUTTON",L"记住此站点的密码",Remember,WS_TABSTOP|BS_AUTOCHECKBOX);
 control(L"BUTTON",L"替换该账号的旧连接",Replace,WS_TABSTOP|BS_AUTOCHECKBOX);
 control(L"BUTTON",L"连接局域网",Connect,WS_TABSTOP|BS_OWNERDRAW);
 control(L"STATIC",L"VPN 信息",403);
 control(L"STATIC",L"控制入口   等待探测\r\n\r\n数据路径   等待连接\r\n\r\n虚拟地址   —",Info);
 control(L"STATIC",L"获准的局域网资源",404);
 control(WC_LISTVIEWW,L"",Resources,LVS_REPORT|LVS_SINGLESEL,WS_EX_CLIENTEDGE);
 ListView_SetExtendedListViewStyle(controls[Resources],LVS_EX_FULLROWSELECT|LVS_EX_DOUBLEBUFFER);
 int i=0;for(auto p:{std::pair{L"资源",220},std::pair{L"地址",240},std::pair{L"协议",110}}){LVCOLUMNW col{};col.mask=LVCF_TEXT|LVCF_WIDTH;col.cx=p.second;col.pszText=const_cast<wchar_t*>(p.first);ListView_InsertColumn(controls[Resources],i++,&col);}
 layout();refreshSelection();tray(true);
}
LRESULT CALLBACK proc(HWND h,UINT m,WPARAM w,LPARAM l) {
 try {
  switch(m){
  case WM_CREATE:window=h;createUI();return 0;
  case WM_SIZE:if(!controls.empty())layout();return 0;
  case WM_GETMINMAXINFO:{auto info=reinterpret_cast<MINMAXINFO*>(l);info->ptMinTrackSize={scale(1000),scale(750)};return 0;}
  case WM_DPICHANGED:{dpi=HIWORD(w);fonts();for(const auto& [id,control]:controls)SendMessageW(control,WM_SETFONT,reinterpret_cast<WPARAM>(id==StatusText?titleFont:font),TRUE);auto rect=reinterpret_cast<RECT*>(l);SetWindowPos(h,nullptr,rect->left,rect->top,rect->right-rect->left,rect->bottom-rect->top,SWP_NOZORDER|SWP_NOACTIVATE);layout();return 0;}
  case WM_CTLCOLORSTATIC:case WM_CTLCOLOREDIT:SetTextColor(reinterpret_cast<HDC>(w),RGB(35,49,65));SetBkColor(reinterpret_cast<HDC>(w),RGB(255,255,255));return reinterpret_cast<LRESULT>(background);
  case WM_PAINT:{PAINTSTRUCT p{};auto dc=BeginPaint(h,&p);RECT r{};GetClientRect(h,&r);FillRect(dc,&r,background);r.right=scale(262);FillRect(dc,&r,sidebar);SetBkMode(dc,TRANSPARENT);SetTextColor(dc,RGB(0,115,130));SelectObject(dc,titleFont);RECT brand{scale(22),scale(36),scale(242),scale(82)};DrawTextW(dc,L"NetGo",-1,&brand,DT_LEFT|DT_SINGLELINE);SelectObject(dc,font);SetTextColor(dc,RGB(83,104,122));RECT note{scale(24),scale(86),scale(246),scale(130)};DrawTextW(dc,L"局域网远程接入\r\n服务器配置",-1,&note,DT_LEFT);EndPaint(h,&p);return 0;}
  case WM_DRAWITEM:{auto item=reinterpret_cast<DRAWITEMSTRUCT*>(l);if(item->CtlID==Connect){HBRUSH b=CreateSolidBrush((item->itemState&ODS_DISABLED)?RGB(160,177,180):RGB(0,120,135));FillRect(item->hDC,&item->rcItem,b);DeleteObject(b);SetBkMode(item->hDC,TRANSPARENT);SetTextColor(item->hDC,RGB(255,255,255));SelectObject(item->hDC,font);auto caption=text(item->hwndItem);DrawTextW(item->hDC,caption.c_str(),-1,&item->rcItem,DT_CENTER|DT_VCENTER|DT_SINGLELINE);if(item->itemState&ODS_FOCUS)DrawFocusRect(item->hDC,&item->rcItem);return TRUE;}break;}
  case WM_COMMAND:
   if(LOWORD(w)==Servers&&HIWORD(w)==LBN_SELCHANGE){auto index=SendMessageW(controls[Servers],LB_GETCURSEL,0,0);if(index>=0&&size_t(index)<sites.size()){chosen=sites[size_t(index)].at("ID");refreshSelection();}return 0;}
   command(LOWORD(w));return 0;
  case WM_TIMER:if(!polling.exchange(true))submit({{"action","status"}});return 0;
  case WM_POWERBROADCAST:if(w==PBT_APMRESUMEAUTOMATIC||w==PBT_APMRESUMESUSPEND){if(!polling.exchange(true))submit({{"action","status"}});}return TRUE;
  case trayMessage:
   if(l==WM_LBUTTONDBLCLK){command(MenuShow);return 0;}
   if(l==WM_RBUTTONUP){auto menu=CreatePopupMenu();AppendMenuW(menu,MF_STRING,MenuShow,L"打开 NetGo");AppendMenuW(menu,MF_STRING,MenuDisconnect,L"断开连接");AppendMenuW(menu,MF_STRING,MenuExit,L"退出");POINT p{};GetCursorPos(&p);SetForegroundWindow(window);int id=TrackPopupMenu(menu,TPM_RETURNCMD|TPM_RIGHTBUTTON,p.x,p.y,0,window,nullptr);DestroyMenu(menu);if(id)command(id);return 0;}break;
  case resultMessage:{
   std::unique_ptr<Result> r(reinterpret_cast<Result*>(l));if(r->action=="status")polling=false;
   if(exiting&&r->action=="shutdown"){
    if(!r->error.empty()){exiting=false;EnableWindow(h,TRUE);MessageBoxW(h,errorText(r->error).c_str(),L"NetGo",MB_ICONWARNING);return 0;}
    DestroyWindow(h);return 0;
   }
   if(!r->error.empty()){
    if(r->action=="status"){set(StatusText,L"后台不可用");set(Info,errorText(r->error));}
    else MessageBoxW(h,errorText(r->error).c_str(),L"NetGo",MB_ICONWARNING);
    return 0;
   }
   if(r->action=="profiles"){
    sites=r->value;if(!selected())chosen=sites.empty()?"":sites[0].at("ID").get<std::string>();SendMessageW(controls[Servers],LB_RESETCONTENT,0,0);
    for(const auto& p:sites)SendMessageW(controls[Servers],LB_ADDSTRING,0,reinterpret_cast<LPARAM>(wide(p.value("Name","")).c_str()));
    refreshSelection();
   }else if(r->action=="import"||r->action=="delete"){submit({{"action","profiles"}});}
   else if(r->action=="credential-info"){
    if(r->site!=chosen)return 0;
    saved=r->value.value("remembered",false);set(Alias,wide(r->value.value("alias","")));SendMessageW(controls[Remember],BM_SETCHECK,saved?BST_CHECKED:BST_UNCHECKED,0);
    if(firstLoad){firstLoad=false;if(saved&&preferences.value("auto_connect",false))command(Connect);}
   }else if(r->action=="forget"){saved=false;refreshSelection();}
   else if(r->action=="status"||r->action=="connect"||r->action=="disconnect")drawStatus(r->value);
   else if(r->action=="logs")logsDialog(r->value);
   else if(r->action=="diagnostic"){auto path=fileDialog(true,L"导出脱敏诊断");if(!path.empty())atomicFile(path,r->value.dump(2));}
   return 0;
  }
  case WM_CLOSE:if(preferences.value("close_to_tray",true)){ShowWindow(h,SW_HIDE);return 0;}command(MenuExit);return 0;
  case WM_DESTROY:{KillTimer(h,1);NOTIFYICONDATAW n{};n.cbSize=sizeof(n);n.hWnd=h;n.uID=1;Shell_NotifyIconW(NIM_DELETE,&n);PostQuitMessage(0);return 0;}
  }
 }catch(const std::exception& e){MessageBoxW(h,errorText(e.what()).c_str(),L"NetGo",MB_ICONWARNING);}
 return DefWindowProcW(h,m,w,l);
}
void launchAgent(){
 try{pipeCall({{"action","status"}});return;}catch(...){}
 auto file=executableDir()/L"netgo-agent.exe";std::wstring command=L"\""+file.wstring()+L"\"";STARTUPINFOW start{};start.cb=sizeof(start);PROCESS_INFORMATION p{};
 if(!CreateProcessW(file.c_str(),command.data(),nullptr,nullptr,FALSE,CREATE_NO_WINDOW,nullptr,file.parent_path().c_str(),&start,&p))throw Error("agent_unavailable");
 CloseHandle(p.hThread);CloseHandle(p.hProcess);
}
}
int WINAPI wWinMain(HINSTANCE instance,HINSTANCE,PWSTR args,int show) {
 try{
  Handle mutex(CreateMutexW(nullptr,TRUE,wide("Local\\NetGoNativeUI-"+sid()).c_str()));
  if(GetLastError()==ERROR_ALREADY_EXISTS){auto h=FindWindowW(L"NetGoNativeWindow",nullptr);if(h){ShowWindow(h,SW_RESTORE);SetForegroundWindow(h);}return 0;}
  INITCOMMONCONTROLSEX common{sizeof(common),ICC_LISTVIEW_CLASSES|ICC_STANDARD_CLASSES};InitCommonControlsEx(&common);
  WSADATA data{};WSAStartup(MAKEWORD(2,2),&data);
  try{preferences=Json::parse(readFile(dataRoot()/L".preferences",8192));}catch(...){preferences=Json::object();}
  chosen=preferences.value("selected","");launchAgent();
  WNDCLASSW c{};c.hInstance=instance;c.lpfnWndProc=proc;c.lpszClassName=L"NetGoNativeWindow";c.hCursor=LoadCursorW(nullptr,IDC_ARROW);c.hIcon=LoadIconW(nullptr,IDI_APPLICATION);RegisterClassW(&c);
  dpi=GetDpiForSystem();
  window=CreateWindowW(c.lpszClassName,L"NetGo · 局域网远程接入",WS_OVERLAPPEDWINDOW,CW_USEDEFAULT,CW_USEDEFAULT,scale(1120),scale(800),nullptr,nullptr,instance,nullptr);
  if(!window)throw Error("window_failed");
  startPump();submit({{"action","profiles"}});SetTimer(window,1,2000,nullptr);
  if(std::wstring(args)!=L"--tray")ShowWindow(window,show);
  UpdateWindow(window);
  MSG msg{};while(GetMessageW(&msg,nullptr,0,0)>0){if(!IsDialogMessageW(window,&msg)){TranslateMessage(&msg);DispatchMessageW(&msg);}}
  closing=true;queueReady.notify_one();if(pump.joinable())pump.join();
  DeleteObject(font);DeleteObject(titleFont);DeleteObject(background);DeleteObject(sidebar);WSACleanup();return 0;
 }catch(const std::exception& e){MessageBoxW(nullptr,errorText(e.what()).c_str(),L"NetGo",MB_ICONERROR);return 1;}
}
