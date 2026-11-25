#include "client.hpp"
#include "netgo_remote_client.h"
#include "netgo_remote.h"
#include <gtk/gtk.h>
#include <curl/curl.h>
#include <openssl/evp.h>
#include <filesystem>
#include <fstream>
#include <thread>
#include <mutex>
#include "remote_input.hpp"
#include <algorithm>
#include <csignal>
#include <cstring>
#include <sys/stat.h>
#include <unistd.h>

using netgo::desktop_linux::Json;using netgo::desktop_linux::Frame;
namespace {
struct State {
 GtkApplication* app=nullptr;GtkWidget *window=nullptr,*profiles=nullptr,*alias=nullptr,*password=nullptr,*notice=nullptr,*hosts=nullptr,*picture=nullptr,*connect=nullptr,*remoteButton=nullptr;
 GtkIMContext* ime=nullptr;GSubprocess* service=nullptr;GtkWidget* internet=nullptr;
 std::string root,socket,site;Json profileList=Json::array(),hostList=Json::array();
 std::atomic_bool alive=true,remoteActive=false,jobBusy=false,renderPending=false;std::thread job,remote;
 std::mutex mutex;Frame frame;netgo::remote::InputQueue inputs;uint32_t caps=0;std::unique_ptr<netgo::desktop_linux::Decoder> decoder;
 ng_remote_client* client=nullptr;bool smoke=false;unsigned timer=0;
 ~State(){alive=false;remoteActive=false;if(job.joinable())job.join();if(remote.joinable())remote.join();if(service){g_subprocess_send_signal(service,SIGTERM);g_object_unref(service);}if(ime)g_object_unref(ime);}
};
State* state=nullptr;
template<class F> GCallback callback(F function){return reinterpret_cast<GCallback>(function);}
void names(GtkWidget* dropdown,const Json& list,const char* field){auto strings=gtk_string_list_new(nullptr);for(const auto& item:list)gtk_string_list_append(strings,item.at(field).get<std::string>().c_str());gtk_drop_down_set_model(GTK_DROP_DOWN(dropdown),G_LIST_MODEL(strings));g_object_unref(strings);if(!list.empty())gtk_drop_down_set_selected(GTK_DROP_DOWN(dropdown),0);}
std::string selected(GtkWidget* dropdown,const Json& list,const char* field){auto i=gtk_drop_down_get_selected(GTK_DROP_DOWN(dropdown));return i<list.size()?list[i].at(field).get<std::string>():std::string();}
struct MainAction {std::function<void()> action;};
void mainThread(std::function<void()> action){g_main_context_invoke(nullptr,[](void* p)->gboolean{std::unique_ptr<MainAction> a(static_cast<MainAction*>(p));a->action();return G_SOURCE_REMOVE;},new MainAction{std::move(action)});}
void status(std::string text){mainThread([text=std::move(text)]{if(state->alive)gtk_label_set_text(GTK_LABEL(state->notice),text.c_str());});}
Json call(const std::string& method,const std::string& path,const Json& body=nullptr){return netgo::desktop_linux::ipc(state->socket,method,path,body,state->alive);}
void startService(){
 auto folder=std::filesystem::canonical("/proc/self/exe").parent_path();auto binary=folder/"netgo-client-service";
 if(std::filesystem::exists("/opt/netgo-client/bin/netgo-client-service"))binary="/opt/netgo-client/bin/netgo-client-service";
 GError* error=nullptr;state->service=g_subprocess_new(GSubprocessFlags(G_SUBPROCESS_FLAGS_STDOUT_SILENCE|G_SUBPROCESS_FLAGS_STDERR_SILENCE),&error,binary.c_str(),"--root",state->root.c_str(),"--socket",state->socket.c_str(),"--daemon","/run/netgo-client/netbird.sock",nullptr);
 if(error){std::string message=error->message;g_error_free(error);throw std::runtime_error(message);}
}
void prepareNetwork(){
 struct stat info{};
 if(stat("/run/netgo-client/netbird.sock",&info)==0&&S_ISSOCK(info.st_mode)&&info.st_uid==getuid()&&std::filesystem::exists("/opt/netgo-client/bin/netgo-client-service"))return;
 auto folder=std::filesystem::canonical("/proc/self/exe").parent_path();auto binary=folder/"netgo-client-service";auto owner=std::to_string(getuid());GError* error=nullptr;
 auto setup=g_subprocess_new(GSubprocessFlags(G_SUBPROCESS_FLAGS_STDOUT_SILENCE|G_SUBPROCESS_FLAGS_STDERR_SILENCE),&error,"pkexec",binary.c_str(),"--network","install","--source",folder.c_str(),"--owner",owner.c_str(),nullptr);
 if(!setup){if(error)g_error_free(error);throw std::runtime_error("无法打开系统管理员授权，请检查桌面系统的授权组件");}
 auto ok=g_subprocess_wait_check(setup,nullptr,&error);g_object_unref(setup);if(!ok){if(error)g_error_free(error);throw std::runtime_error("网络组件安装未完成，请确认管理员授权后重试");}
 if(state->service){g_subprocess_send_signal(state->service,SIGTERM);g_subprocess_wait(state->service,nullptr,nullptr);g_object_unref(state->service);state->service=nullptr;}
 startService();
 for(int i=0;i<100&&state->alive;++i){try{call("GET","/desktop/status");return;}catch(...){g_usleep(100000);}}
 throw std::runtime_error("连接服务未就绪，请重新启动 NetGo");
}
std::string base64(const uint8_t* bytes,size_t size){std::string out(4*((size+2)/3),'\0');auto n=EVP_EncodeBlock(reinterpret_cast<uint8_t*>(out.data()),bytes,int(size));out.resize(size_t(n));for(auto& c:out){if(c=='+')c='-';else if(c=='/')c='_';}while(!out.empty()&&out.back()=='=')out.pop_back();return out;}
std::vector<uint8_t> decode(std::string text,size_t expected){for(auto& c:text){if(c=='-')c='+';else if(c=='_')c='/';}while(text.size()%4)text+='=';std::vector<uint8_t> out(text.size()/4*3);auto n=EVP_DecodeBlock(out.data(),reinterpret_cast<const uint8_t*>(text.data()),int(text.size()));while(!text.empty()&&text.back()=='='){--n;text.pop_back();}if(n<0||size_t(n)!=expected)throw std::runtime_error("invalid_signature");out.resize(expected);return out;}
void task(std::function<void()> action){
 if(state->jobBusy.exchange(true))return;
 if(state->job.joinable())state->job.join();
 state->job=std::thread([action=std::move(action)]{try{action();}catch(const std::exception& e){status(e.what());}state->jobBusy=false;});
}
void queue(Json input){if(!state->remoteActive)return;std::lock_guard lock(state->mutex);if(!(state->caps&NG_REMOTE_INPUT))return;
 state->inputs.push(std::move(input));
}
void present(Frame frame){
 {std::lock_guard lock(state->mutex);state->frame=std::move(frame);}
 if(state->renderPending.exchange(true))return;
 mainThread([]{
  state->renderPending=false;if(!state->alive)return;Frame frame;{std::lock_guard lock(state->mutex);frame=state->frame;}
  if(frame.bgra.empty())return;
  auto bytes=g_bytes_new(frame.bgra.data(),frame.bgra.size());auto texture=gdk_memory_texture_new(int(frame.width),int(frame.height),GDK_MEMORY_B8G8R8A8,bytes,frame.width*4);g_bytes_unref(bytes);gtk_picture_set_paintable(GTK_PICTURE(state->picture),GDK_PAINTABLE(texture));g_object_unref(texture);
 });
}
bool point(double x,double y,bool clamp){
 uint32_t w,h;{std::lock_guard lock(state->mutex);w=state->frame.width;h=state->frame.height;}if(!w||!h)return false;
 double vw=gtk_widget_get_width(state->picture),vh=gtk_widget_get_height(state->picture),scale=std::min(vw/w,vh/h);if(scale<=0)return false;auto left=(vw-w*scale)/2,top=(vh-h*scale)/2;
 if(!clamp&&(x<left||y<top||x>=left+w*scale||y>=top+h*scale))return false;
 queue({{"type","move"},{"x",uint32_t(std::clamp((x-left)/(w*scale),0.0,1.0)*65535)},{"y",uint32_t(std::clamp((y-top)/(h*scale),0.0,1.0)*65535)}});return true;
}
void stopRemote(){state->remoteActive=false;std::lock_guard lock(state->mutex);if(state->client)ng_remote_client_cancel(state->client);state->inputs.clear();}
void view(std::string host){
 if(state->remoteActive.exchange(true))return;
 if(state->remote.joinable())state->remote.join();
 gtk_widget_set_sensitive(state->remoteButton,FALSE);
 state->remote=std::thread([host=std::move(host)]{
  ng_remote_client_options o{};o.struct_size=sizeof(o);o.abi_version=NG_REMOTE_CLIENT_ABI;o.site_id=state->site.c_str();o.host_id=host.c_str();o.capabilities=NG_REMOTE_VIEW|NG_REMOTE_INPUT;
  o.active=[](void*)->int32_t{return state->alive&&state->remoteActive?1:0;};
  o.control=[](void*,const char* method,const char* path,const char* body,const char* idem,char* output,size_t capacity,size_t* length)->int32_t{try{
   auto value=std::string(method)=="CONTEXT"?call("GET","/desktop/remote/context?site="+state->site):call("POST","/desktop/remote/call",{{"site",state->site},{"method",method},{"path",path},{"body",*body?Json::parse(body):Json()},{"idempotency",idem}});auto text=value.dump();if(text.size()>capacity)return -1;std::copy(text.begin(),text.end(),output);*length=text.size();return 0;
  }catch(...){return -1;}};
  o.sign=[](void*,const uint8_t* message,size_t size,uint8_t output[64])->int32_t{try{auto result=call("POST","/desktop/remote/sign",{{"Site",state->site},{"Message",base64(message,size)}});auto bytes=decode(result.at("signature"),64);std::copy(bytes.begin(),bytes.end(),output);return 0;}catch(...){return -1;}};
  o.input=[](void*,char* output,size_t capacity,size_t* length)->int32_t{try{std::lock_guard lock(state->mutex);auto text=state->inputs.take().dump();if(text.size()>capacity)return -1;std::copy(text.begin(),text.end(),output);*length=text.size();return 0;}catch(...){return -1;}};
  o.format=[](void*,uint32_t w,uint32_t h,uint32_t fps,uint32_t caps)->int32_t{try{if(w>4096||h>2160||fps>60)return -1;state->decoder=std::make_unique<netgo::desktop_linux::Decoder>();std::lock_guard lock(state->mutex);state->caps=caps;return 0;}catch(...){return -1;}};
  o.video=[](void*,const uint8_t* bytes,size_t size,int64_t)->int32_t{try{state->decoder->decode(bytes,size,present);return 0;}catch(...){return -1;}};
  o.status=[](void*,const char* code){status(std::string(code)=="remote_connected"?"远程桌面已连接":std::string(code)=="remote_waiting_consent"?"等待主机确认…":std::string(code)=="remote_host_busy"?"对方正在使用远程桌面，请稍后刷新":std::string(code)=="remote_host_unavailable"?"对方暂不可连接，请检查 VPN 和桌面共享":std::string(code)=="remote_view_forbidden"?"当前账号没有观看权限":code);};
  ng_remote_client* client=nullptr;
  if(ng_remote_client_create(&o,&client)==0){{std::lock_guard lock(state->mutex);state->client=client;}ng_remote_client_run(client);{std::lock_guard lock(state->mutex);state->client=nullptr;}ng_remote_client_destroy(client);}else status("remote_client_failed");
  state->decoder.reset();state->remoteActive=false;{std::lock_guard lock(state->mutex);state->caps=0;state->inputs.clear();state->frame={};}
  mainThread([]{if(state->alive){gtk_picture_set_paintable(GTK_PICTURE(state->picture),nullptr);gtk_widget_set_sensitive(state->remoteButton,TRUE);}});
 });
}
void refresh(){task([]{
 auto profiles=call("GET","/desktop/profiles");mainThread([profiles=std::move(profiles)]{if(!state->alive)return;state->profileList=profiles;names(state->profiles,profiles,"Name");});
 });}
void chooseProfile(GObject* dialog,GAsyncResult* result,void*){
 GError* error=nullptr;auto file=gtk_file_dialog_open_finish(GTK_FILE_DIALOG(dialog),result,&error);if(!file){if(error)g_error_free(error);return;}auto path=g_file_get_path(file);std::string name=path?path:"";g_free(path);g_object_unref(file);
 task([name]{
  std::ifstream input(name,std::ios::binary);if(!input||std::filesystem::file_size(name)>1048576)throw std::runtime_error("invalid_site_profile");Json profile;input>>profile;
  bool changed=false;for(const auto& old:call("GET","/desktop/profiles"))if(old.at("ID")==profile.at("ID")&&(old.value("Origin","")!=profile.value("Origin","")||old.value("CA","")!=profile.value("CA","")))changed=true;
  if(!changed){call("POST","/desktop/import",profile);status("站点已导入");return;}
  mainThread([profile=std::move(profile)]{
   if(!state->alive)return;
   auto dialog=gtk_alert_dialog_new("文件更改了站点地址或证书。请确认它来自你的服务器管理员后继续。");const char* buttons[]={"取消","确认导入",nullptr};gtk_alert_dialog_set_buttons(dialog,buttons);gtk_alert_dialog_set_cancel_button(dialog,0);
   gtk_alert_dialog_choose(dialog,GTK_WINDOW(state->window),nullptr,[](GObject* object,GAsyncResult* result,void* context){std::unique_ptr<Json> value(static_cast<Json*>(context));GError* error=nullptr;auto selected=gtk_alert_dialog_choose_finish(GTK_ALERT_DIALOG(object),result,&error);if(error){g_error_free(error);return;}if(selected==1)task([profile=std::move(*value)]{call("POST","/desktop/import-trusted",profile);status("站点已导入");});},new Json(std::move(profile)));g_object_unref(dialog);
  });
 });
}
uint32_t virtualKey(guint key){
 if(key>=GDK_KEY_a&&key<=GDK_KEY_z)return key-GDK_KEY_a+'A';
 if(key>=GDK_KEY_A&&key<=GDK_KEY_Z)return key;
 if(key>=GDK_KEY_0&&key<=GDK_KEY_9)return key;
 if(key>=GDK_KEY_F1&&key<=GDK_KEY_F24)return 112+key-GDK_KEY_F1;
 switch(key){case GDK_KEY_Return:return 13;case GDK_KEY_BackSpace:return 8;case GDK_KEY_Tab:return 9;case GDK_KEY_Escape:return 27;case GDK_KEY_space:return 32;case GDK_KEY_Left:return 37;case GDK_KEY_Up:return 38;case GDK_KEY_Right:return 39;case GDK_KEY_Down:return 40;case GDK_KEY_Delete:return 46;case GDK_KEY_Home:return 36;case GDK_KEY_End:return 35;case GDK_KEY_Page_Up:return 33;case GDK_KEY_Page_Down:return 34;case GDK_KEY_Shift_L:case GDK_KEY_Shift_R:return 16;case GDK_KEY_Control_L:case GDK_KEY_Control_R:return 17;case GDK_KEY_Alt_L:case GDK_KEY_Alt_R:return 18;default:return 0;}
}
void activate(GtkApplication* app,void*){
 state->app=app;state->window=gtk_application_window_new(app);gtk_window_set_title(GTK_WINDOW(state->window),"NetGo");gtk_window_set_default_size(GTK_WINDOW(state->window),960,700);
 auto box=gtk_box_new(GTK_ORIENTATION_VERTICAL,10);gtk_widget_set_margin_start(box,16);gtk_widget_set_margin_end(box,16);gtk_widget_set_margin_top(box,16);gtk_widget_set_margin_bottom(box,16);gtk_window_set_child(GTK_WINDOW(state->window),box);
 auto row=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);gtk_box_append(GTK_BOX(box),row);state->profiles=gtk_drop_down_new(nullptr,nullptr);gtk_widget_set_hexpand(state->profiles,TRUE);gtk_box_append(GTK_BOX(row),state->profiles);
 auto import=gtk_button_new_with_label("导入站点…");gtk_box_append(GTK_BOX(row),import);g_signal_connect(import,"clicked",callback(+[](GtkButton*,void*){auto dialog=gtk_file_dialog_new();gtk_file_dialog_set_title(dialog,"导入 NetGo 站点");gtk_file_dialog_open(dialog,GTK_WINDOW(state->window),nullptr,chooseProfile,nullptr);g_object_unref(dialog);}),nullptr);
 auto refreshButton=gtk_button_new_with_label("刷新");gtk_box_append(GTK_BOX(row),refreshButton);g_signal_connect(refreshButton,"clicked",callback(+[](GtkButton*,void*){refresh();}),nullptr);
 row=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);gtk_box_append(GTK_BOX(box),row);state->alias=gtk_entry_new();gtk_entry_set_placeholder_text(GTK_ENTRY(state->alias),"账号");gtk_box_append(GTK_BOX(row),state->alias);state->password=gtk_password_entry_new();gtk_box_append(GTK_BOX(row),state->password);
 state->internet=gtk_check_button_new_with_label("通过旁路由访问互联网（需要账号授权）");gtk_box_append(GTK_BOX(box),state->internet);
 state->connect=gtk_button_new_with_label("连接网络");gtk_box_append(GTK_BOX(row),state->connect);g_signal_connect(state->connect,"clicked",callback(+[](GtkButton*,void*){
  auto id=selected(state->profiles,state->profileList,"ID");if(id.empty()){status("请先导入并选择站点");return;}if(state->remoteActive){status("请先结束远程桌面");return;}state->site=id;
  Json request={{"Site",state->site},{"Alias",gtk_editable_get_text(GTK_EDITABLE(state->alias))},{"Password",gtk_editable_get_text(GTK_EDITABLE(state->password))},{"Replace",false},{"Internet",bool(gtk_check_button_get_active(GTK_CHECK_BUTTON(state->internet)))}};gtk_editable_set_text(GTK_EDITABLE(state->password),"");task([request=std::move(request)]{prepareNetwork();call("POST","/desktop/connect",request);});
 }),nullptr);
 auto disconnect=gtk_button_new_with_label("断开网络");gtk_box_append(GTK_BOX(row),disconnect);g_signal_connect(disconnect,"clicked",callback(+[](GtkButton*,void*){stopRemote();task([]{call("POST","/desktop/disconnect");});}),nullptr);
 state->notice=gtk_label_new("导入站点后使用已有账号连接");gtk_label_set_xalign(GTK_LABEL(state->notice),0);gtk_box_append(GTK_BOX(box),state->notice);
 row=gtk_box_new(GTK_ORIENTATION_HORIZONTAL,8);gtk_box_append(GTK_BOX(box),row);state->hosts=gtk_drop_down_new(nullptr,nullptr);gtk_widget_set_hexpand(state->hosts,TRUE);gtk_box_append(GTK_BOX(row),state->hosts);
 auto hosts=gtk_button_new_with_label("更新远程主机");gtk_box_append(GTK_BOX(row),hosts);g_signal_connect(hosts,"clicked",callback(+[](GtkButton*,void*){if(state->site.empty()||state->remoteActive)return;task([]{auto result=call("POST","/desktop/remote/call",{{"site",state->site},{"method","GET"},{"path","/netgo/v2/remote"},{"body",nullptr},{"idempotency",""}});mainThread([result=std::move(result)]{if(!state->alive)return;state->hostList=Json::array();for(const auto& host:result.at("hosts"))if(host.value("status","")=="ready"&&(host.value("available_capabilities",0u)&NG_REMOTE_VIEW))state->hostList.push_back(host);names(state->hosts,state->hostList,"name");if(state->hostList.empty())status("暂无可连接的已授权主机；请确认对方已连接 VPN 并开启共享。");});});}),nullptr);
 state->remoteButton=gtk_button_new_with_label("打开远程桌面");gtk_box_append(GTK_BOX(row),state->remoteButton);g_signal_connect(state->remoteButton,"clicked",callback(+[](GtkButton*,void*){auto id=selected(state->hosts,state->hostList,"id");if(!id.empty())view(id);}),nullptr);
 auto stop=gtk_button_new_with_label("结束远程桌面");gtk_box_append(GTK_BOX(row),stop);g_signal_connect(stop,"clicked",callback(+[](GtkButton*,void*){stopRemote();}),nullptr);
 state->picture=gtk_picture_new();gtk_widget_set_vexpand(state->picture,TRUE);gtk_picture_set_content_fit(GTK_PICTURE(state->picture),GTK_CONTENT_FIT_CONTAIN);gtk_widget_set_focusable(state->picture,TRUE);gtk_box_append(GTK_BOX(box),state->picture);
 auto motion=gtk_event_controller_motion_new();g_signal_connect(motion,"motion",callback(+[](GtkEventControllerMotion*,double x,double y,void*){point(x,y,false);}),nullptr);gtk_widget_add_controller(state->picture,motion);
 auto click=gtk_gesture_click_new();gtk_gesture_single_set_button(GTK_GESTURE_SINGLE(click),0);g_signal_connect(click,"pressed",callback(+[](GtkGestureClick* g,int,double x,double y,void*){auto button=gtk_gesture_single_get_current_button(GTK_GESTURE_SINGLE(g));if(point(x,y,false)&&button<=3){gtk_widget_grab_focus(state->picture);queue({{"type","button"},{"button",button==2?3:button==3?2:1},{"down",true}});}}),nullptr);
 g_signal_connect(click,"released",callback(+[](GtkGestureClick* g,int,double x,double y,void*){point(x,y,true);auto button=gtk_gesture_single_get_current_button(GTK_GESTURE_SINGLE(g));if(button<=3)queue({{"type","button"},{"button",button==2?3:button==3?2:1},{"down",false}});}),nullptr);g_signal_connect(click,"cancel",callback(+[](GtkGesture*,GdkEventSequence*,void*){queue({{"type","release"}});}),nullptr);gtk_widget_add_controller(state->picture,GTK_EVENT_CONTROLLER(click));
 auto scroll=gtk_event_controller_scroll_new(GTK_EVENT_CONTROLLER_SCROLL_BOTH_AXES);g_signal_connect(scroll,"scroll",callback(+[](GtkEventControllerScroll*,double x,double y,void*)->gboolean{if(y)queue({{"type","wheel"},{"delta",int(std::clamp(-y*120,-1200.0,1200.0))},{"horizontal",false}});if(x)queue({{"type","wheel"},{"delta",int(std::clamp(x*120,-1200.0,1200.0))},{"horizontal",true}});return TRUE;}),nullptr);gtk_widget_add_controller(state->picture,scroll);
 state->ime=gtk_im_multicontext_new();gtk_im_context_set_client_widget(state->ime,state->picture);g_signal_connect(state->ime,"commit",callback(+[](GtkIMContext*,const char* text,void*){if(text&&std::strlen(text)<=8192)queue({{"type","text"},{"text",text}});}),nullptr);
 auto keys=gtk_event_controller_key_new();g_signal_connect(keys,"key-pressed",callback(+[](GtkEventControllerKey* c,guint key,guint,GdkModifierType mods,void*)->gboolean{if(!(mods&(GDK_CONTROL_MASK|GDK_ALT_MASK))&&gtk_im_context_filter_keypress(state->ime,gtk_event_controller_get_current_event(GTK_EVENT_CONTROLLER(c))))return TRUE;auto code=virtualKey(key);if(code)queue({{"type","key"},{"code",code},{"extended",code>=33&&code<=46},{"down",true}});return TRUE;}),nullptr);g_signal_connect(keys,"key-released",callback(+[](GtkEventControllerKey*,guint key,guint,GdkModifierType,void*){auto code=virtualKey(key);if(code)queue({{"type","key"},{"code",code},{"extended",code>=33&&code<=46},{"down",false}});}),nullptr);gtk_widget_add_controller(state->picture,keys);
 auto focus=gtk_event_controller_focus_new();g_signal_connect(focus,"enter",callback(+[](GtkEventControllerFocus*,void*){gtk_im_context_focus_in(state->ime);}),nullptr);g_signal_connect(focus,"leave",callback(+[](GtkEventControllerFocus*,void*){gtk_im_context_focus_out(state->ime);queue({{"type","release"}});}),nullptr);gtk_widget_add_controller(state->picture,focus);
 g_signal_connect(state->window,"close-request",callback(+[](GtkWindow*,void*)->gboolean{stopRemote();state->alive=false;return FALSE;}),nullptr);
 gtk_window_present(GTK_WINDOW(state->window));
 if(state->smoke){g_timeout_add(1000,[](void*)->gboolean{g_application_quit(G_APPLICATION(state->app));return G_SOURCE_REMOVE;},nullptr);return;}
 if(!std::filesystem::exists(state->socket)){try{startService();}catch(const std::exception& e){status(e.what());}}
 state->timer=g_timeout_add(1000,[](void*)->gboolean{if(!state->alive)return G_SOURCE_REMOVE;if(!state->jobBusy)task([]{auto value=call("GET","/desktop/status");status(value.value("state","")+"  "+value.value("error",""));});return G_SOURCE_CONTINUE;},nullptr);refresh();
}
}
int main(int argc,char** argv){
 if(argc==2&&std::string(argv[1])=="--version"){g_print("{\"version\":\"%s\",\"platform\":\"linux\",\"arch\":\"amd64\"}\n",NETGO_BUILD_VERSION);return 0;}
 State storage;state=&storage;auto config=g_get_user_config_dir();state->root=std::string(config)+"/NetGo";state->socket=state->root+"/client.sock";
 for(int i=1;i<argc;++i){if(std::string(argv[i])=="--smoke")state->smoke=true;else if(std::string(argv[i])=="--socket"&&i+1<argc)state->socket=argv[++i];else return 2;}
 curl_global_init(CURL_GLOBAL_DEFAULT);auto app=gtk_application_new("org.netgo.desktop",G_APPLICATION_DEFAULT_FLAGS);g_signal_connect(app,"activate",callback(activate),nullptr);int result=g_application_run(G_APPLICATION(app),1,argv);state->alive=false;stopRemote();if(state->timer)g_source_remove(state->timer);if(state->job.joinable())state->job.join();if(state->remote.joinable())state->remote.join();while(g_main_context_pending(nullptr))g_main_context_iteration(nullptr,FALSE);g_object_unref(app);curl_global_cleanup();return result;
}
