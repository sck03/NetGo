#include "ui.hpp"
#include <commdlg.h>
#include <algorithm>
#include <map>

namespace netgo {
namespace {
enum { Name=501,SiteID,Origin,Direct,Gateway,Relay,Auxiliary,AuxRelay,Revision,CAStatus,ImportCA,ClearCA };
struct Field { HWND window; int x,y,width,height; };
struct Dialog {
 HWND window=nullptr,owner=nullptr;HFONT font=nullptr;UINT dpi=96;
 Json profile;bool create=false,done=false,accepted=false;
 std::map<int,HWND> controls;std::vector<Field> fields;
 int px(int value) const {return MulDiv(value,int(dpi),96);}
 std::string value(int id) const {
  auto h=controls.at(id);int n=GetWindowTextLengthW(h);std::wstring text(n+1,L'\0');
  GetWindowTextW(h,text.data(),n+1);text.resize(n);auto result=utf8(text);
  auto first=result.find_first_not_of(" \t\r\n"),last=result.find_last_not_of(" \t\r\n");
  return first==std::string::npos?"":result.substr(first,last-first+1);
 }
 void fonts(){
  auto previous=font;font=CreateFontW(-px(13),0,0,0,FW_NORMAL,FALSE,FALSE,FALSE,DEFAULT_CHARSET,
      OUT_DEFAULT_PRECIS,CLIP_DEFAULT_PRECIS,CLEARTYPE_QUALITY,DEFAULT_PITCH,L"Microsoft YaHei UI");
  for(const auto& field:fields){SendMessageW(field.window,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);
   MoveWindow(field.window,px(field.x),px(field.y),px(field.width),px(field.height),TRUE);}
  if(previous)DeleteObject(previous);
 }
 HWND control(const wchar_t* cls,const std::wstring& text,int id,int x,int y,int width,int height,DWORD style=0,DWORD extra=0){
  auto h=CreateWindowExW(extra,cls,text.c_str(),WS_CHILD|WS_VISIBLE|style,px(x),px(y),px(width),px(height),window,
      reinterpret_cast<HMENU>(INT_PTR(id)),GetModuleHandleW(nullptr),nullptr);
  SendMessageW(h,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);controls[id]=h;fields.push_back({h,x,y,width,height});return h;
 }
 void field(int id,const wchar_t* label,const std::string& content,int y){
  control(L"STATIC",label,id+200,18,y+4,96,22);
  auto h=control(L"EDIT",wide(content),id,116,y,426,26,WS_TABSTOP|ES_AUTOHSCROLL,WS_EX_CLIENTEDGE);
  SendMessageW(h,EM_SETLIMITTEXT,id==Name?256:4096,0);
 }
 void updateCA(){SetWindowTextW(controls[CAStatus],profile.value("CA","").empty()?L"使用系统信任":L"已加载自定义公共 CA");}
 void initialize(){
  dpi=GetDpiForWindow(window);fonts();
  control(L"STATIC",L"建议导入管理员提供的站点文件。以下信息请向管理员索取。",600,18,14,524,30);
  field(Name,L"显示名称：",profile.value("Name",""),54);
  field(SiteID,L"站点 ID：",profile.value("ID",""),88);
  field(Origin,L"站点网址：",profile.value("Origin",""),122);
  std::string direct,auxiliary;for(const auto& candidate:profile.value("Candidates",Json::array())){auto& text=candidate.value("Kind","")=="direct"?direct:auxiliary;if(!text.empty())text+=" ";text+=candidate.value("Address","");}
  field(Direct,L"直达入口：",direct,156);
  field(Auxiliary,L"VPS 入口：",auxiliary,190);
  field(Gateway,L"路由虚拟 IP：",profile.value("GatewayIP",""),224);
  control(L"STATIC",L"证书信任：",601,18,263,96,24);
  control(L"STATIC",L"",CAStatus,116,263,270,24);
  control(L"BUTTON",L"导入 CA…",ImportCA,382,259,84,26,WS_TABSTOP);
  control(L"BUTTON",L"清除",ClearCA,476,259,66,26,WS_TABSTOP);updateCA();
  field(Relay,L"本地中继：",profile.value("LocalRelay",""),298);
  field(AuxRelay,L"VPS 中继：",profile.value("AuxiliaryRelay",""),332);
  field(Revision,L"配置版本：",std::to_string(profile.value("ConfigRevision",uint64_t(0))),366);
  if(create)SendMessageW(controls[Revision],EM_SETREADONLY,TRUE,0);
  control(L"STATIC",L"入口填主机:端口，多个用空格分隔。内网 IP 仅在内网可达；\r\n外网需公网直达或已配置的 VPS。新建配置使用引导版本 0，连接\r\n后同步正式版本；编辑已有配置请填写管理员提供的新版本。",602,18,404,524,60);
  control(L"BUTTON",L"保存",IDOK,372,478,80,28,WS_TABSTOP|BS_DEFPUSHBUTTON);
  control(L"BUTTON",L"取消",IDCANCEL,462,478,80,28,WS_TABSTOP);
  if(!create){SendMessageW(controls[SiteID],EM_SETREADONLY,TRUE,0);SendMessageW(controls[Origin],EM_SETREADONLY,TRUE,0);
   EnableWindow(controls[ImportCA],FALSE);EnableWindow(controls[ClearCA],FALSE);}
 }
 void loadCA(){
  wchar_t path[32768]{};OPENFILENAMEW choice{};choice.lStructSize=sizeof(choice);choice.hwndOwner=window;
  choice.lpstrFile=path;choice.nMaxFile=32768;choice.lpstrFilter=L"公共证书 (*.pem;*.crt)\0*.pem;*.crt\0所有文件\0*.*\0";
  choice.lpstrTitle=L"导入管理员提供的公共 CA 证书";choice.Flags=OFN_FILEMUSTEXIST|OFN_PATHMUSTEXIST|OFN_NOCHANGEDIR;
  if(!GetOpenFileNameW(&choice))return;
  auto certificate=readFile(path,32768);
  if(certificate.find("PRIVATE KEY")!=std::string::npos)throw Error("请只导入公共证书，文件不能包含私钥。");
  Json check=profile;check["CA"]=certificate;validateProfile(check);profile["CA"]=certificate;updateCA();
 }
 void save(){
  Json next=profile;next["Name"]=value(Name);next["ID"]=value(SiteID);next["Origin"]=value(Origin);
  next["GatewayIP"]=value(Gateway);next["LocalRelay"]=value(Relay);next["AuxiliaryRelay"]=value(AuxRelay);
  auto revision=value(Revision);if(revision.empty()||revision.find_first_not_of("0123456789")!=std::string::npos)throw Error("invalid_profile_revision");next["ConfigRevision"]=create?0:std::stoull(revision);
  next=profileEndpoints(next,value(Direct),value(Auxiliary));
  validateProfile(next);if(!create)validateProfileUpdate(profile,next);
  profile=std::move(next);accepted=true;DestroyWindow(window);
 }
};
LRESULT CALLBACK procedure(HWND h,UINT message,WPARAM w,LPARAM l){
 auto d=reinterpret_cast<Dialog*>(GetWindowLongPtrW(h,GWLP_USERDATA));
 if(message==WM_NCCREATE){d=static_cast<Dialog*>(reinterpret_cast<CREATESTRUCTW*>(l)->lpCreateParams);d->window=h;SetWindowLongPtrW(h,GWLP_USERDATA,reinterpret_cast<LONG_PTR>(d));}
 if(!d)return DefWindowProcW(h,message,w,l);
 try{
  switch(message){
  case WM_CREATE:d->initialize();return 0;
  case DM_GETDEFID:return MAKELRESULT(IDOK,DC_HASDEFID);
  case WM_COMMAND:
   switch(LOWORD(w)){case IDOK:d->save();break;case IDCANCEL:DestroyWindow(h);break;case ImportCA:d->loadCA();break;case ClearCA:d->profile["CA"]="";d->updateCA();break;}return 0;
  case WM_DPICHANGED:{d->dpi=HIWORD(w);d->fonts();auto rect=reinterpret_cast<RECT*>(l);SetWindowPos(h,nullptr,rect->left,rect->top,rect->right-rect->left,rect->bottom-rect->top,SWP_NOZORDER|SWP_NOACTIVATE);return 0;}
  case WM_CLOSE:DestroyWindow(h);return 0;
  case WM_DESTROY:d->done=true;return 0;
  }
 }catch(const std::exception& error){
  std::wstring detail=wide(error.what());
  if(detail==L"profile_revision_conflict")detail=L"此站点使用配置版本保护。请向管理员获取更新的站点文件，或在“配置版本”字段填写管理员提供的新版本。";
  else if(detail.rfind(L"invalid_",0)==0||detail==L"unsupported_profile")detail=L"请核对名称、站点 ID、HTTPS 网址、主机:端口和路由节点 IPv4 地址。";
  MessageBoxW(h,detail.c_str(),L"检查服务器配置",MB_OK|MB_ICONWARNING);
 }
 return DefWindowProcW(h,message,w,l);
}
}
std::optional<Json> profileDialog(HWND owner,const Json& profile,bool create){
 Dialog d;d.profile=profile;d.owner=owner;d.create=create;d.dpi=GetDpiForWindow(owner);
 WNDCLASSW cls{};cls.lpfnWndProc=procedure;cls.hInstance=GetModuleHandleW(nullptr);cls.lpszClassName=L"NetGoServerForm";
 cls.hCursor=LoadCursorW(nullptr,IDC_ARROW);cls.hbrBackground=reinterpret_cast<HBRUSH>(COLOR_BTNFACE+1);RegisterClassW(&cls);
 constexpr DWORD style=WS_CAPTION|WS_SYSMENU;constexpr DWORD extra=WS_EX_DLGMODALFRAME|WS_EX_CONTROLPARENT;
 RECT rect{0,0,d.px(560),d.px(522)},parent{},work{};GetWindowRect(owner,&parent);SystemParametersInfoW(SPI_GETWORKAREA,0,&work,0);
 AdjustWindowRectExForDpi(&rect,style,FALSE,extra,d.dpi);int width=rect.right-rect.left,height=rect.bottom-rect.top;
 int x=std::clamp((parent.left+parent.right-width)/2,work.left,std::max(work.left,work.right-width));
 int y=std::clamp((parent.top+parent.bottom-height)/2,work.top,std::max(work.top,work.bottom-height));
 auto h=CreateWindowExW(extra,cls.lpszClassName,create?L"新建服务器":L"编辑服务器",style,x,y,width,height,owner,nullptr,cls.hInstance,&d);
 if(!h)throw Error("window_failed");
 EnableWindow(owner,FALSE);ShowWindow(h,SW_SHOW);SetFocus(d.controls[Name]);
 MSG msg{};while(!d.done){int result=GetMessageW(&msg,nullptr,0,0);if(result<=0){if(result==0)PostQuitMessage(int(msg.wParam));break;}
  if(!IsDialogMessageW(h,&msg)){TranslateMessage(&msg);DispatchMessageW(&msg);}}
 if(IsWindow(h))DestroyWindow(h);
 EnableWindow(owner,TRUE);SetForegroundWindow(owner);if(d.font)DeleteObject(d.font);
 if(d.accepted)return d.profile;
 return std::nullopt;
}
}
