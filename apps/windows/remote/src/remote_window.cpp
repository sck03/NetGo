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

using namespace netgo;
namespace {
constexpr UINT update=WM_APP+51,ask=WM_APP+52,done=WM_APP+53;
HWND window=nullptr,list=nullptr,connectButton=nullptr,hostButton=nullptr,stopButton=nullptr,notice=nullptr;
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
RECT displayRect(){RECT r{};GetClientRect(window,&r);r.top=82;r.left=8;r.right-=8;r.bottom-=8;return r;}
desktop::layout::Rect imageRectLocked(){auto r=displayRect();return desktop::layout::fit({r.left,r.top,r.right,r.bottom},frame.width,frame.height);}
desktop::layout::Rect imageRect(){std::lock_guard lock(mutex);return imageRectLocked();}
bool inputEnabled(){return viewing&&receiving&&running&&(activeCaps&NG_REMOTE_INPUT);}
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
 running=true;receiving=false;activeCaps=0;composing=false;viewing=!host;EnableWindow(connectButton,FALSE);EnableWindow(hostButton,FALSE);EnableWindow(list,FALSE);EnableWindow(stopButton,TRUE);
 worker=std::thread([host,id]{try{if(host)desktop::host(site,callbacks());else desktop::view(site,id,callbacks());}catch(const std::exception& e){postStatus(std::string("远程桌面已停止：")+e.what());}PostMessageW(window,done,0,0);});
}
void refresh(){
 if(busy.exchange(true))return;
 if(worker.joinable())worker.join();
 listing=true;
 running=true;
 worker=std::thread([]{try{auto inventory=desktop::inventory(site,[]{return running.load();});{std::lock_guard lock(mutex);hosts=Json::array();for(const auto& host:inventory.at("hosts"))if(host.value("status","")=="ready"&&(host.value("available_capabilities",0u)&NG_REMOTE_VIEW))hosts.push_back(host);}postStatus(hosts.empty()?"暂无可连接的已授权主机；请确认对方已连接 VPN、开启共享且没有其他会话。":"选择已授权主机，或允许此电脑接收远程连接。");}catch(const std::exception& e){postStatus(e.what());}PostMessageW(window,done,0,0);});
}
LRESULT CALLBACK proc(HWND h,UINT m,WPARAM w,LPARAM l){
 switch(m){
 case WM_CREATE:{
  window=h;list=CreateWindowW(WC_COMBOBOXW,L"",WS_CHILD|WS_VISIBLE|CBS_DROPDOWNLIST|WS_VSCROLL,8,8,280,200,h,reinterpret_cast<HMENU>(1),nullptr,nullptr);
  connectButton=CreateWindowW(L"BUTTON",L"连接主机",WS_CHILD|WS_VISIBLE|WS_TABSTOP,300,8,100,28,h,reinterpret_cast<HMENU>(2),nullptr,nullptr);
  hostButton=CreateWindowW(L"BUTTON",L"允许本机远控…",WS_CHILD|WS_VISIBLE|WS_TABSTOP,412,8,130,28,h,reinterpret_cast<HMENU>(3),nullptr,nullptr);
  CreateWindowW(L"BUTTON",L"刷新",WS_CHILD|WS_VISIBLE|WS_TABSTOP,554,8,70,28,h,reinterpret_cast<HMENU>(4),nullptr,nullptr);
  stopButton=CreateWindowW(L"BUTTON",L"停止",WS_CHILD|WS_VISIBLE|WS_TABSTOP,636,8,80,28,h,reinterpret_cast<HMENU>(5),nullptr,nullptr);EnableWindow(stopButton,FALSE);
  notice=CreateWindowW(L"STATIC",L"",WS_CHILD|WS_VISIBLE,8,44,790,30,h,nullptr,nullptr,nullptr);
  auto font=GetStockObject(DEFAULT_GUI_FONT);for(auto child:{list,connectButton,hostButton,stopButton,notice})SendMessageW(child,WM_SETFONT,reinterpret_cast<WPARAM>(font),TRUE);return 0;
 }
 case WM_COMMAND:if(LOWORD(w)==2&&!busy){auto i=SendMessageW(list,CB_GETCURSEL,0,0);std::string id;{std::lock_guard lock(mutex);if(i>=0&&std::size_t(i)<hosts.size())id=hosts[std::size_t(i)].at("id");}if(!id.empty())start(false,id);}else if(LOWORD(w)==3&&!busy){if(MessageBoxW(h,L"允许站点管理员授权的账号申请查看和控制当前桌面？\n每次连接仍需本机确认。点击“停止”或关闭窗口立即停止。",L"启用远程桌面",MB_YESNO|MB_ICONQUESTION)==IDYES)start(true);}else if(LOWORD(w)==4&&!busy)refresh();else if(LOWORD(w)==5&&busy)stop();return 0;
 case update:{std::lock_guard lock(mutex);SetWindowTextW(notice,wide(status).c_str());auto rect=displayRect();InvalidateRect(h,&rect,FALSE);return 0;}
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
 case done:{if(worker.joinable())worker.join();busy=false;running=false;receiving=false;activeCaps=0;EnableWindow(stopButton,FALSE);if(closing){DestroyWindow(h);return 0;}if(listing){listing=false;SendMessageW(list,CB_RESETCONTENT,0,0);std::lock_guard lock(mutex);for(const auto& item:hosts){auto name=wide(item.value("name",""));SendMessageW(list,CB_ADDSTRING,0,reinterpret_cast<LPARAM>(name.c_str()));}if(!hosts.empty())SendMessageW(list,CB_SETCURSEL,0,0);}else{std::lock_guard lock(mutex);frame={};inputs.clear();}viewing=false;EnableWindow(connectButton,TRUE);EnableWindow(hostButton,TRUE);EnableWindow(list,TRUE);InvalidateRect(h,nullptr,TRUE);return 0;}
 case WM_GETMINMAXINFO:{auto limits=reinterpret_cast<MINMAXINFO*>(l);limits->ptMinTrackSize={760,260};return 0;}
 case WM_SIZE:MoveWindow(notice,8,44,std::max(10,int(LOWORD(l))-16),30,TRUE);InvalidateRect(h,nullptr,TRUE);return 0;
 case WM_PAINT:{PAINTSTRUCT paint{};auto dc=BeginPaint(h,&paint);auto area=displayRect();FillRect(dc,&area,reinterpret_cast<HBRUSH>(GetStockObject(BLACK_BRUSH)));std::lock_guard lock(mutex);auto r=imageRectLocked();if(!frame.bgra.empty()){BITMAPINFO info{};info.bmiHeader.biSize=sizeof(BITMAPINFOHEADER);info.bmiHeader.biWidth=LONG(frame.width);info.bmiHeader.biHeight=-LONG(frame.height);info.bmiHeader.biPlanes=1;info.bmiHeader.biBitCount=32;info.bmiHeader.biCompression=BI_RGB;SetStretchBltMode(dc,COLORONCOLOR);StretchDIBits(dc,r.left,r.top,r.right-r.left,r.bottom-r.top,0,0,int(frame.width),int(frame.height),frame.bgra.data(),&info,DIB_RGB_COLORS,SRCCOPY);}EndPaint(h,&paint);return 0;}
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
  window=CreateWindowExW(0,cls.lpszClassName,L"NetGo · 远程桌面",WS_OVERLAPPEDWINDOW,CW_USEDEFAULT,CW_USEDEFAULT,840,560,nullptr,nullptr,instance,nullptr);if(!window)throw Error("remote_window_failed");ShowWindow(window,show);refresh();MSG msg{};while(GetMessageW(&msg,nullptr,0,0)>0){TranslateMessage(&msg);DispatchMessageW(&msg);}running=false;if(worker.joinable())worker.join();WSACleanup();return 0;
 }catch(const std::exception& e){MessageBoxW(nullptr,wide(e.what()).c_str(),L"NetGo 远程桌面",MB_ICONERROR);return 1;}
}
