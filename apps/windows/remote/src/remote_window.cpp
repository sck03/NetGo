#include "remote_runtime.hpp"
#include "display_layout.hpp"
#include <shellapi.h>
#include <commctrl.h>
#include <windowsx.h>
#include "remote_input.hpp"
#include <mutex>
#include <future>
#include "netgo_remote.h"
#include <imm.h>
#include <uxtheme.h>

using namespace netgo;
namespace {
constexpr UINT update=WM_APP+51,ask=WM_APP+52,done=WM_APP+53;
HWND window=nullptr,list=nullptr,connectButton=nullptr,hostButton=nullptr,stopButton=nullptr,notice=nullptr;
HWND refreshButton=nullptr,viewOnly=nullptr,audioOption=nullptr,fullButton=nullptr;
bool fullscreen=false;WINDOWPLACEMENT placement{};LONG_PTR windowStyle=0;
std::atomic_bool running=true,busy=false,closing=false,receiving=false;std::thread worker;
std::mutex mutex;desktop::Frame frame;std::string status="正在加载远程主机…",site;Json hosts=Json::array();netgo::remote::InputQueue inputs;
bool viewing=false,listing=false,composing=false,releasingCapture=false;std::atomic_uint32_t activeCaps=0;
struct Consent {std::string user;uint32_t caps=0;std::promise<bool> result;std::atomic_bool expired=false;};
HRESULT CALLBACK consentTimer(HWND dialog,UINT event,WPARAM elapsed,LPARAM,LONG_PTR data){
 auto* request=reinterpret_cast<Consent*>(data);
 if(event==TDN_TIMER&&(!running||closing||request->expired||elapsed>=15000))SendMessageW(dialog,TDM_CLICK_BUTTON,IDNO,0);
 return S_OK;
}
void postStatus(std::string value){if(viewing&&(activeCaps&NG_REMOTE_VIEW)&&!(activeCaps&NG_REMOTE_INPUT))value+="（仅观看）";{std::lock_guard lock(mutex);status=std::move(value);}PostMessageW(window,update,0,0);}
RECT displayRect(){RECT r{};GetClientRect(window,&r);r.top=116;r.left=12;r.right-=12;r.bottom-=12;return r;}
desktop::layout::Rect imageRectLocked(){auto r=displayRect();return desktop::layout::fit({r.left,r.top,r.right,r.bottom},frame.width,frame.height);}
desktop::layout::Rect imageRect(){std::lock_guard lock(mutex);return imageRectLocked();}
bool inputEnabled(){return viewing&&receiving&&running&&(activeCaps&NG_REMOTE_INPUT);}
std::string hostLabel(const Json& host){
 auto value=host.value("status","");
 if(value=="ready")return "可连接";
 if(value=="busy")return "正在使用";
 if(value=="offline")return "主机失联";
 if(value=="vpn_offline")return "VPN 未连接";
 if(value=="disabled")return "共享已关闭";
 return "状态未知";
}
Json selectedHost(){
 auto index=SendMessageW(list,CB_GETCURSEL,0,0);std::lock_guard lock(mutex);
 return index>=0&&std::size_t(index)<hosts.size()?hosts[std::size_t(index)]:Json();
}
void selectionChanged(bool explain){
 auto host=selectedHost();auto caps=host.is_null()?0:host.value("available_capabilities",0u);
 EnableWindow(connectButton,!busy&&!host.is_null()&&host.value("status","")=="ready"&&(caps&NG_REMOTE_VIEW));
 EnableWindow(viewOnly,!busy&&(caps&NG_REMOTE_INPUT));EnableWindow(audioOption,!busy&&(caps&NG_REMOTE_AUDIO));
 if(explain&&!host.is_null())postStatus(host.value("name","")+" · "+hostLabel(host)+(host.value("status","")=="ready"?"；可选择仅观看或收听声音后连接。":"；请在对方恢复共享后刷新。"));
}
void toggleFullscreen(){
 if(!fullscreen&&(!viewing||!receiving))return;
 if(inputEnabled())inputs.push({{"type","release"}});
 if(GetCapture()==window)ReleaseCapture();
 if(!fullscreen){
  placement.length=sizeof(placement);if(!GetWindowPlacement(window,&placement))return;windowStyle=GetWindowLongPtrW(window,GWL_STYLE);
  MONITORINFO monitor{};monitor.cbSize=sizeof(monitor);if(!GetMonitorInfoW(MonitorFromWindow(window,MONITOR_DEFAULTTONEAREST),&monitor))return;
  SetWindowLongPtrW(window,GWL_STYLE,windowStyle&~WS_OVERLAPPEDWINDOW);fullscreen=true;
  SetWindowPos(window,HWND_TOP,monitor.rcMonitor.left,monitor.rcMonitor.top,monitor.rcMonitor.right-monitor.rcMonitor.left,monitor.rcMonitor.bottom-monitor.rcMonitor.top,SWP_FRAMECHANGED);
 }else{
  fullscreen=false;SetWindowLongPtrW(window,GWL_STYLE,windowStyle);SetWindowPlacement(window,&placement);
  SetWindowPos(window,nullptr,0,0,0,0,SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_FRAMECHANGED);
 }
 SetWindowTextW(fullButton,fullscreen?L"退出全屏 (F11)":L"全屏 (F11)");
}
void stop(){receiving=false;running=false;activeCaps=0;{std::lock_guard lock(mutex);frame={};inputs.clear();}EnableWindow(stopButton,FALSE);postStatus("正在停止远程桌面并清理会话…");}
void queue(Json value){
 if(!inputEnabled())return;
 inputs.push(std::move(value));
}
desktop::Callbacks callbacks(){
 return {[]{return running.load();},postStatus,
 [](desktop::Frame value){if(!running||closing)return;{std::lock_guard lock(mutex);if(!running||closing)return;frame=std::move(value);}receiving=true;PostMessageW(window,update,0,0);},
 [](const std::string& user,uint32_t caps){auto request=std::make_shared<Consent>();request->user=user;request->caps=caps;auto future=request->result.get_future();auto p=new std::shared_ptr<Consent>(request);if(!PostMessageW(window,ask,0,reinterpret_cast<LPARAM>(p))){delete p;return false;}
  auto until=Clock::now()+std::chrono::seconds(15);while(running&&Clock::now()<until){if(future.wait_for(std::chrono::milliseconds(100))==std::future_status::ready)return future.get();}request->expired=true;return false;},
 []{return inputs.take().get<std::vector<Json>>();},
 [](uint32_t caps){activeCaps=caps;}};
}
void start(bool host,const std::string& id={}){
 if(busy.exchange(true))return;
 if(worker.joinable())worker.join();
 auto caps=uint32_t(NG_REMOTE_VIEW);
 if(SendMessageW(viewOnly,BM_GETCHECK,0,0)!=BST_CHECKED)caps|=NG_REMOTE_INPUT;
 if(SendMessageW(audioOption,BM_GETCHECK,0,0)==BST_CHECKED)caps|=NG_REMOTE_AUDIO;
 running=true;receiving=false;activeCaps=0;composing=false;viewing=!host;
 for(auto control:{connectButton,hostButton,list,refreshButton,viewOnly,audioOption})EnableWindow(control,FALSE);
 EnableWindow(stopButton,TRUE);postStatus(host?"正在开启本机共享…":"正在连接远程主机…");
 worker=std::thread([host,id,caps]{try{if(host)desktop::host(site,callbacks());else desktop::view(site,id,caps,callbacks());}catch(const std::exception& e){postStatus(std::string("远程桌面已停止：")+e.what());}PostMessageW(window,done,0,0);});
}
void refresh(){
 if(busy.exchange(true))return;
 if(worker.joinable())worker.join();
 listing=true;
 running=true;
 for(auto control:{connectButton,hostButton,list,refreshButton,viewOnly,audioOption})EnableWindow(control,FALSE);
 postStatus("正在刷新主机状态…");
 worker=std::thread([]{try{auto inventory=desktop::inventory(site,[]{return running.load();});{std::lock_guard lock(mutex);hosts=Json::array();for(const auto& host:inventory.at("hosts"))if(host.value("available_capabilities",0u)&NG_REMOTE_VIEW)hosts.push_back(host);std::stable_sort(hosts.begin(),hosts.end(),[](const auto& a,const auto& b){return (a.value("status","")=="ready")>(b.value("status","")=="ready");});}postStatus(hosts.empty()?"暂无已授权主机；请让对方开启共享，并由管理员授予桌面权限。":"选择主机开始远程协助；灰色连接按钮表示该主机暂不可连接。");}catch(const std::exception& e){postStatus(e.what());}PostMessageW(window,done,0,0);});
}
LRESULT CALLBACK proc(HWND h,UINT m,WPARAM w,LPARAM l){
 switch(m){
 case WM_CREATE:{
  window=h;list=CreateWindowW(WC_COMBOBOXW,L"",WS_CHILD|WS_VISIBLE|WS_TABSTOP|CBS_DROPDOWNLIST|WS_VSCROLL,12,12,280,200,h,reinterpret_cast<HMENU>(1),nullptr,nullptr);
  connectButton=CreateWindowW(L"BUTTON",L"连接主机",WS_CHILD|WS_VISIBLE|WS_TABSTOP,300,8,100,28,h,reinterpret_cast<HMENU>(2),nullptr,nullptr);
  hostButton=CreateWindowW(L"BUTTON",L"允许本机远控…",WS_CHILD|WS_VISIBLE|WS_TABSTOP,412,8,130,28,h,reinterpret_cast<HMENU>(3),nullptr,nullptr);
  refreshButton=CreateWindowW(L"BUTTON",L"刷新",WS_CHILD|WS_VISIBLE|WS_TABSTOP,554,8,70,28,h,reinterpret_cast<HMENU>(4),nullptr,nullptr);
  stopButton=CreateWindowW(L"BUTTON",L"停止",WS_CHILD|WS_VISIBLE|WS_TABSTOP,636,8,80,28,h,reinterpret_cast<HMENU>(5),nullptr,nullptr);EnableWindow(stopButton,FALSE);
  viewOnly=CreateWindowW(L"BUTTON",L"仅观看",WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_AUTOCHECKBOX,12,48,110,28,h,reinterpret_cast<HMENU>(6),nullptr,nullptr);
  audioOption=CreateWindowW(L"BUTTON",L"收听电脑声音",WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_AUTOCHECKBOX,134,48,150,28,h,reinterpret_cast<HMENU>(7),nullptr,nullptr);
  fullButton=CreateWindowW(L"BUTTON",L"全屏 (F11)",WS_CHILD|WS_VISIBLE|WS_TABSTOP,554,48,150,28,h,reinterpret_cast<HMENU>(8),nullptr,nullptr);
  SendMessageW(audioOption,BM_SETCHECK,BST_CHECKED,0);EnableWindow(fullButton,FALSE);
  notice=CreateWindowW(L"STATIC",L"",WS_CHILD|WS_VISIBLE|SS_ENDELLIPSIS,12,82,790,24,h,nullptr,nullptr,nullptr);
  auto font=GetStockObject(DEFAULT_GUI_FONT);for(auto child:{list,connectButton,hostButton,refreshButton,stopButton,viewOnly,audioOption,fullButton,notice}){SendMessageW(child,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);SetWindowTheme(child,L"Explorer",nullptr);}return 0;
 }
 case WM_COMMAND:
  if(LOWORD(w)==1&&HIWORD(w)==CBN_SELCHANGE&&!busy)selectionChanged(true);
  else if(LOWORD(w)==2&&!busy){auto host=selectedHost();if(!host.is_null()&&host.value("status","")=="ready"&&(host.value("available_capabilities",0u)&NG_REMOTE_VIEW))start(false,host.at("id"));}
  else if(LOWORD(w)==3&&!busy){if(MessageBoxW(h,L"允许站点管理员授权的账号申请查看和控制当前桌面？\n每次连接仍需本机确认。点击“停止”或关闭窗口立即停止。",L"启用远程桌面",MB_YESNO|MB_ICONQUESTION)==IDYES)start(true);}
  else if(LOWORD(w)==4&&!busy)refresh();else if(LOWORD(w)==5&&busy)stop();else if(LOWORD(w)==8)toggleFullscreen();
  return 0;
 case update:{std::lock_guard lock(mutex);SetWindowTextW(notice,wide(status).c_str());EnableWindow(fullButton,fullscreen||(viewing&&receiving));auto rect=displayRect();InvalidateRect(h,&rect,FALSE);return 0;}
 case ask:{
  std::unique_ptr<std::shared_ptr<Consent>> p(reinterpret_cast<std::shared_ptr<Consent>*>(l));auto& request=**p;int button=IDNO;
  if(running&&!closing&&!request.expired){
   auto content=L"站点账号 "+wide(request.user)+L" 请求查看屏幕。";if(request.caps&NG_REMOTE_INPUT)content+=L"\n允许操作键盘鼠标。";if(request.caps&NG_REMOTE_AUDIO)content+=L"\n允许收听此电脑播放的声音。";
   TASKDIALOGCONFIG dialog{};dialog.cbSize=sizeof(dialog);dialog.hwndParent=h;dialog.dwFlags=TDF_CALLBACK_TIMER|TDF_ALLOW_DIALOG_CANCELLATION;
   dialog.pszWindowTitle=L"NetGo 远程协助";dialog.pszMainInstruction=L"是否允许本次连接？";dialog.pszContent=content.c_str();dialog.pszFooter=L"15 秒内未确认将自动拒绝。";
   dialog.dwCommonButtons=TDCBF_YES_BUTTON|TDCBF_NO_BUTTON;dialog.nDefaultButton=IDNO;dialog.pfCallback=consentTimer;dialog.lpCallbackData=reinterpret_cast<LONG_PTR>(&request);
   if(FAILED(TaskDialogIndirect(&dialog,&button,nullptr,nullptr)))button=IDNO;
  }
  request.result.set_value(button==IDYES&&running&&!closing&&!request.expired);return 0;
 }
 case done:{
  if(worker.joinable())worker.join();
  busy=false;running=false;receiving=false;activeCaps=0;EnableWindow(stopButton,FALSE);
  if(closing){DestroyWindow(h);return 0;}if(fullscreen)toggleFullscreen();
  if(listing){listing=false;SendMessageW(list,CB_RESETCONTENT,0,0);std::lock_guard lock(mutex);for(const auto& item:hosts){auto name=wide(item.value("name","")+" · "+hostLabel(item));SendMessageW(list,CB_ADDSTRING,0,reinterpret_cast<LPARAM>(name.c_str()));}if(!hosts.empty())SendMessageW(list,CB_SETCURSEL,0,0);}
  else{std::lock_guard lock(mutex);frame={};inputs.clear();}
  viewing=false;EnableWindow(fullButton,FALSE);for(auto control:{hostButton,list,refreshButton})EnableWindow(control,TRUE);selectionChanged(false);InvalidateRect(h,nullptr,TRUE);return 0;
 }
 case WM_GETMINMAXINFO:{auto limits=reinterpret_cast<MINMAXINFO*>(l);limits->ptMinTrackSize={800,360};return 0;}
 case WM_SIZE:{
  auto width=int(LOWORD(l)),listWidth=std::max(180,width-472);MoveWindow(list,12,12,listWidth,200,TRUE);int x=24+listWidth;
  for(auto [control,size]:{std::pair{connectButton,100},std::pair{hostButton,130},std::pair{refreshButton,70},std::pair{stopButton,80}}){MoveWindow(control,x,12,size,28,TRUE);x+=size+10;}
  MoveWindow(fullButton,std::max(300,width-162),48,150,28,TRUE);MoveWindow(notice,12,82,std::max(10,width-24),24,TRUE);InvalidateRect(h,nullptr,TRUE);return 0;
 }
 case WM_PAINT:{PAINTSTRUCT paint{};auto dc=BeginPaint(h,&paint);auto area=displayRect();auto brush=CreateSolidBrush(RGB(20,32,44));FillRect(dc,&area,brush);DeleteObject(brush);std::lock_guard lock(mutex);auto r=imageRectLocked();if(!frame.bgra.empty()){BITMAPINFO info{};info.bmiHeader.biSize=sizeof(BITMAPINFOHEADER);info.bmiHeader.biWidth=LONG(frame.width);info.bmiHeader.biHeight=-LONG(frame.height);info.bmiHeader.biPlanes=1;info.bmiHeader.biBitCount=32;info.bmiHeader.biCompression=BI_RGB;SetStretchBltMode(dc,COLORONCOLOR);StretchDIBits(dc,r.left,r.top,r.right-r.left,r.bottom-r.top,0,0,int(frame.width),int(frame.height),frame.bgra.data(),&info,DIB_RGB_COLORS,SRCCOPY);}else{SetBkMode(dc,TRANSPARENT);SetTextColor(dc,RGB(180,202,220));auto font=SelectObject(dc,GetStockObject(DEFAULT_GUI_FONT));DrawTextW(dc,listing?L"正在读取主机状态…":busy?(viewing?L"连接后，远程画面将在这里显示":L"共享状态见上方提示 · 来访连接需要你的同意"):L"选择主机开始远程协助 · 画面自动适应窗口",-1,&area,DT_CENTER|DT_VCENTER|DT_SINGLELINE);SelectObject(dc,font);}EndPaint(h,&paint);return 0;}
 case WM_MOUSEMOVE:if(inputEnabled()){auto p=desktop::layout::map(imageRect(),GET_X_LPARAM(l),GET_Y_LPARAM(l),GetCapture()==h);if(p)queue({{"type","move"},{"x",p->x},{"y",p->y}});}return 0;
 case WM_LBUTTONDOWN:case WM_LBUTTONUP:case WM_RBUTTONDOWN:case WM_RBUTTONUP:case WM_MBUTTONDOWN:case WM_MBUTTONUP:if(inputEnabled()){auto p=desktop::layout::map(imageRect(),GET_X_LPARAM(l),GET_Y_LPARAM(l));bool down=m==WM_LBUTTONDOWN||m==WM_RBUTTONDOWN||m==WM_MBUTTONDOWN;if(!down||p){if(down){SetFocus(h);SetCapture(h);queue({{"type","move"},{"x",p->x},{"y",p->y}});}queue({{"type","button"},{"button",(m==WM_LBUTTONDOWN||m==WM_LBUTTONUP)?1:(m==WM_RBUTTONDOWN||m==WM_RBUTTONUP)?2:3},{"down",down}});if(!down&&GetCapture()==h&&!(w&(MK_LBUTTON|MK_RBUTTON|MK_MBUTTON))){releasingCapture=true;ReleaseCapture();releasingCapture=false;}}}return 0;
 case WM_MOUSEWHEEL:case WM_MOUSEHWHEEL:if(inputEnabled()){POINT p{GET_X_LPARAM(l),GET_Y_LPARAM(l)};ScreenToClient(h,&p);if(desktop::layout::map(imageRect(),p.x,p.y))queue({{"type","wheel"},{"delta",GET_WHEEL_DELTA_WPARAM(w)},{"horizontal",m==WM_MOUSEHWHEEL}});}return 0;
 case WM_IME_STARTCOMPOSITION:composing=true;if(viewing&&receiving)queue({{"type","release"}});break;
 case WM_IME_ENDCOMPOSITION:composing=false;break;
 case WM_IME_COMPOSITION:if(viewing&&receiving&&(l&GCS_RESULTSTR)){auto ime=ImmGetContext(h);if(ime){auto bytes=ImmGetCompositionStringW(ime,GCS_RESULTSTR,nullptr,0);if(bytes>0&&bytes<=16384&&bytes%2==0){std::wstring text(size_t(bytes)/2,L'\0');if(ImmGetCompositionStringW(ime,GCS_RESULTSTR,text.data(),DWORD(bytes))==bytes)queue({{"type","text"},{"text",utf8(text)}});}ImmReleaseContext(h,ime);}}break;
 case WM_KEYDOWN:case WM_KEYUP:case WM_SYSKEYDOWN:case WM_SYSKEYUP:if(inputEnabled()&&w<=255){bool imeOpen=false;auto ime=ImmGetContext(h);if(ime){imeOpen=ImmGetOpenStatus(ime)!=FALSE;ImmReleaseContext(h,ime);}auto modifiers=(GetKeyState(VK_CONTROL)|GetKeyState(VK_MENU))&0x8000;if(!composing&&!(imeOpen&&!modifiers&&w>=VK_SPACE&&w<=0x5a))queue({{"type","key"},{"code",unsigned(w)},{"extended",bool(l&(1L<<24))},{"down",m==WM_KEYDOWN||m==WM_SYSKEYDOWN}});if(!imeOpen)return 0;}break;
 case WM_CAPTURECHANGED:if(!releasingCapture&&viewing&&receiving)queue({{"type","release"}});break;
 case WM_KILLFOCUS:if(viewing&&busy)queue({{"type","release"}});break;
 case WM_CLOSE:closing=true;if(busy){stop();EnableWindow(connectButton,FALSE);EnableWindow(hostButton,FALSE);}else DestroyWindow(h);return 0;
 case WM_DESTROY:PostQuitMessage(0);return 0;
 }
 return DefWindowProcW(h,m,w,l);
}
}
int remoteMain(HINSTANCE instance,int show,const std::wstring& siteValue){
 try{
  site=utf8(siteValue);if(site.empty()||site.size()>63||site.find_first_not_of("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-")!=std::string::npos)return 2;
  Handle single(CreateMutexW(nullptr,TRUE,wide("Local\\NetGoRemote-"+sid()).c_str()));if(GetLastError()==ERROR_ALREADY_EXISTS){MessageBoxW(nullptr,L"已有远程桌面窗口正在运行。",L"NetGo",MB_OK);return 0;}
  WSADATA ws{};if(WSAStartup(MAKEWORD(2,2),&ws))return 1;INITCOMMONCONTROLSEX common{sizeof(common),ICC_STANDARD_CLASSES};InitCommonControlsEx(&common);
  WNDCLASSW cls{};cls.hInstance=instance;cls.lpfnWndProc=proc;cls.lpszClassName=L"NetGoRemoteWindow";cls.hCursor=LoadCursorW(nullptr,IDC_ARROW);cls.hIcon=LoadIconW(instance,MAKEINTRESOURCEW(101));cls.hbrBackground=reinterpret_cast<HBRUSH>(COLOR_BTNFACE+1);RegisterClassW(&cls);
  window=CreateWindowExW(0,cls.lpszClassName,L"NetGo · 远程桌面",WS_OVERLAPPEDWINDOW,CW_USEDEFAULT,CW_USEDEFAULT,980,680,nullptr,nullptr,instance,nullptr);if(!window)throw Error("remote_window_failed");ShowWindow(window,show);refresh();MSG msg{};while(GetMessageW(&msg,nullptr,0,0)>0){if((msg.message==WM_KEYDOWN||msg.message==WM_KEYUP)&&msg.wParam==VK_F11){if(msg.message==WM_KEYDOWN&&!(msg.lParam&(1L<<30)))toggleFullscreen();continue;}if(GetFocus()!=window&&IsDialogMessageW(window,&msg))continue;TranslateMessage(&msg);DispatchMessageW(&msg);}running=false;if(worker.joinable())worker.join();WSACleanup();return 0;
 }catch(const std::exception& e){MessageBoxW(nullptr,wide(e.what()).c_str(),L"NetGo 远程桌面",MB_ICONERROR);return 1;}
}
