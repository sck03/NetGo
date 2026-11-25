#include "remote_runtime.hpp"
#include "connection.hpp"
#include "remote_transport.hpp"
#include "remote_input.hpp"
#include "netgo_remote_ticket.h"
#include "netgo_remote_client.h"
#include "netgo_remote_clock.hpp"
#include "netgo_remote_watchdog.hpp"
#include "desktop_capture.hpp"
#include "video_codec.hpp"
#include "audio.hpp"
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <d3d11.h>
#include <wrl/client.h>
#include <set>
#include <mutex>
#include <algorithm>
#include <utility>

namespace netgo::desktop {
namespace {
using remote::Connection;using remote::Message;using remote::Kind;using remote::Key;
using Bytes=std::vector<unsigned char>;
constexpr unsigned fps=20;
constexpr char base[]="/netgo/v2/remote";
Json call(const std::string& site,const std::string& method,const std::string& path,const Json& body=nullptr,const std::string& idem={},const std::function<bool()>& active={}){
 return pipeCall({{"action","remote-call"},{"site",site},{"method",method},{"path",path},{"body",body},{"idempotency",idem}},active);
}
Json context(const std::string& site,const std::function<bool()>& active={}){return pipeCall({{"action","remote-context"},{"site",site}},active);}
Bytes decode(std::string text,std::size_t expected){
 if(text.size()>65536||text.find_first_not_of("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")!=std::string::npos)throw Error("remote_invalid_encoding");
 if(text.empty())throw Error("remote_invalid_encoding");
 auto original=text;for(auto& c:text){if(c=='-')c='+';else if(c=='_')c='/';}while(text.size()%4)text+='=';
 Bytes out(text.size()/4*3);auto n=EVP_DecodeBlock(out.data(),reinterpret_cast<const unsigned char*>(text.data()),int(text.size()));if(n<0)throw Error("remote_invalid_encoding");
 while(!text.empty()&&text.back()=='='){n--;text.pop_back();}if(n<0)throw Error("remote_invalid_encoding");out.resize(std::size_t(n));
 if(out.size()!=expected||base64url(out.data(),out.size())!=original)throw Error("remote_invalid_encoding");
 return out;
}
Key key(const Json& j){auto b=decode(j.get<std::string>(),32);Key k{};std::copy(b.begin(),b.end(),k.begin());return k;}
Message message(Kind kind,const Json& j){auto text=j.dump();return {kind,{text.begin(),text.end()}};}
Json json(const Message& m){if(m.body.size()>65536)throw Error("remote_invalid_message");return Json::parse(m.body);}
Message receive(Connection& c,Kind expected,const std::function<bool()>& allowed){
 auto end=Clock::now()+std::chrono::seconds(20);Message m;
 while(allowed()&&Clock::now()<end){if(c.receive(m,100)){if(m.kind!=expected)throw Error("remote_unexpected_message");return m;}}
 throw Error("remote_handshake_timeout");
}
struct Gate {
 remote::ServerClock clock;
 ng_remote_ticket_verifier* verifier=nullptr;ng_remote_gate* value=nullptr;
 Gate(const Json& a,const Json& authenticated,uint64_t requested):clock(a.value("server_unix_ms",uint64_t(0)),requested){
  auto site=key(a.at("site_key"));auto binding=decode(a.at("session").at("binding"),32);
  const auto& session=a.at("session");
  std::string siteID=authenticated.at("site_id"),userID=session.at("user_id"),controllerID=session.at("controller_id"),hostID=session.at("host_id");
  if(authenticated.at("device_id")!=hostID||authenticated.at("public_key")!=a.at("host_key"))throw Error("remote_authorization_mismatch");
  auto controller=key(a.at("controller_key")),host=key(a.at("host_key"));auto boot=decode(session.at("host_boot"),16);
  ng_remote_ticket_binding fields{};fields.struct_size=sizeof(fields);fields.abi_version=NG_REMOTE_ABI;
  auto id=[](const std::string& value){return ng_remote_ticket_id{reinterpret_cast<const uint8_t*>(value.data()),value.size()};};
  fields.site_id=id(siteID);fields.user_id=id(userID);fields.controller_id=id(controllerID);fields.host_id=id(hostID);
  std::copy(site.begin(),site.end(),fields.site_key);std::copy(controller.begin(),controller.end(),fields.controller_key);std::copy(host.begin(),host.end(),fields.host_key);std::copy(boot.begin(),boot.end(),fields.host_boot);
  Key digest{};if(ng_remote_ticket_binding_digest(&fields,digest.data())!=NG_REMOTE_OK||!std::equal(digest.begin(),digest.end(),binding.begin()))throw Error("remote_binding_mismatch");
  if(ng_remote_ticket_verifier_create(site.data(),site.size(),&verifier)!=NG_REMOTE_OK)throw Error("remote_invalid_site_key");
  ng_remote_options options{};options.struct_size=sizeof(options);options.abi_version=NG_REMOTE_ABI;std::copy(binding.begin(),binding.end(),options.binding_digest);options.allowed_capabilities=NG_REMOTE_VIEW|NG_REMOTE_INPUT|NG_REMOTE_AUDIO;options.consent_mode=NG_REMOTE_LOCAL_CONSENT;options.verify=ng_remote_ticket_verify;options.verify_context=verifier;
  clock.configure(options);
  if(ng_remote_gate_create(&options,&value)!=NG_REMOTE_OK){ng_remote_ticket_verifier_destroy(verifier);verifier=nullptr;throw Error("remote_gate_failed");}
 }
 ~Gate(){ng_remote_gate_destroy(value);ng_remote_ticket_verifier_destroy(verifier);}
 Gate(const Gate&)=delete;
 bool check(unsigned cap=NG_REMOTE_VIEW){return ng_remote_gate_check(value,cap)==NG_REMOTE_OK;}
 void begin(const Bytes& wire){auto r=ng_remote_gate_begin(value,wire.data(),wire.size());if(r!=NG_REMOTE_OK&&r!=NG_REMOTE_CONSENT_REQUIRED)throw Error("remote_ticket_invalid");}
};
// A slow/unreachable control plane cannot leave an unlimited live capture worker.
struct Monitor {
 std::atomic_bool stop=false;remote::Watchdog watchdog;std::mutex mutex;std::string session;std::string site,boot;Callbacks* callbacks;std::thread worker;
 Monitor(std::string s,std::string b,Callbacks& cb):site(std::move(s)),boot(std::move(b)),callbacks(&cb){
  worker=std::thread([this]{try{
   while(live()){
    const auto nextPoll=remote::monotonic_time_ms()+3000;
    call(site,"POST",std::string(base)+"/host/heartbeat",{{"boot",boot}},{},[this]{return live();});
    if(!live())break;
    std::string id;{std::lock_guard lock(mutex);id=session;}
    if(!id.empty()){
     auto a=call(site,"GET",std::string(base)+"/sessions/"+id,nullptr,{},[this]{return live();});
     if(a.at("session").value("state","")=="closed")break;
    }else context(site,[this]{return live();});
    if(!watchdog.refresh())break;
    while(live()&&remote::monotonic_time_ms()<nextPoll)Sleep(100);
   }
  }catch(...){ }watchdog.close();});
 }
 ~Monitor(){stop=true;watchdog.close();if(worker.joinable())worker.join();}
 bool live(){return !stop&&callbacks->running()&&watchdog.live();}
 void bind(std::string id){std::lock_guard lock(mutex);session=std::move(id);}
};
struct Input {
 std::set<unsigned> keys;std::set<unsigned> buttons;int left=0,top=0;unsigned screenWidth=0,screenHeight=0;
 ~Input(){release();}
 void release(){for(auto k:keys){INPUT in{};in.type=INPUT_KEYBOARD;in.ki.wVk=WORD(k&255);in.ki.dwFlags=KEYEVENTF_KEYUP|((k&256)?KEYEVENTF_EXTENDEDKEY:0);SendInput(1,&in,sizeof(in));}keys.clear();for(auto b:buttons){INPUT in{};in.type=INPUT_MOUSE;in.mi.dwFlags=b==1?MOUSEEVENTF_LEFTUP:b==2?MOUSEEVENTF_RIGHTUP:MOUSEEVENTF_MIDDLEUP;SendInput(1,&in,sizeof(in));}buttons.clear();}
 void apply(const Json& j){
  INPUT in{};auto type=j.value("type","");unsigned tracked=0;bool down=false;
  if(type=="key"){auto code=j.at("code").get<unsigned>();if(code==0||code>255)throw Error("remote_invalid_input");down=j.at("down").get<bool>();auto extended=j.value("extended",false);in.type=INPUT_KEYBOARD;in.ki.wVk=WORD(code);in.ki.dwFlags=(down?0:KEYEVENTF_KEYUP)|(extended?KEYEVENTF_EXTENDEDKEY:0);tracked=code|(extended?256:0);}
  else if(type=="move"){
   auto x=j.at("x").get<unsigned>(),y=j.at("y").get<unsigned>();if(x>65535||y>65535||!screenWidth||!screenHeight)throw Error("remote_invalid_input");
   auto vw=GetSystemMetrics(SM_CXVIRTUALSCREEN),vh=GetSystemMetrics(SM_CYVIRTUALSCREEN);if(vw<2||vh<2)throw Error("remote_invalid_display");
   auto px=left+int(std::uint64_t(x)*(screenWidth-1)/65535),py=top+int(std::uint64_t(y)*(screenHeight-1)/65535);
   in.type=INPUT_MOUSE;in.mi.dx=LONG(std::int64_t(px-GetSystemMetrics(SM_XVIRTUALSCREEN))*65535/(vw-1));in.mi.dy=LONG(std::int64_t(py-GetSystemMetrics(SM_YVIRTUALSCREEN))*65535/(vh-1));in.mi.dwFlags=MOUSEEVENTF_MOVE|MOUSEEVENTF_ABSOLUTE|MOUSEEVENTF_VIRTUALDESK;
  }else if(type=="button"){auto b=j.at("button").get<unsigned>();if(b<1||b>3)throw Error("remote_invalid_input");down=j.at("down").get<bool>();tracked=b;in.type=INPUT_MOUSE;in.mi.dwFlags=b==1?(down?MOUSEEVENTF_LEFTDOWN:MOUSEEVENTF_LEFTUP):b==2?(down?MOUSEEVENTF_RIGHTDOWN:MOUSEEVENTF_RIGHTUP):(down?MOUSEEVENTF_MIDDLEDOWN:MOUSEEVENTF_MIDDLEUP);}
  else if(type=="wheel"){auto delta=j.at("delta").get<int>();if(delta<-1200||delta>1200)throw Error("remote_invalid_input");in.type=INPUT_MOUSE;in.mi.dwFlags=j.value("horizontal",false)?MOUSEEVENTF_HWHEEL:MOUSEEVENTF_WHEEL;in.mi.mouseData=DWORD(delta);}
  else if(type=="text"){
   auto text=j.at("text").get<std::string>();if(text.empty()||text.size()>8192||text.find('\0')!=std::string::npos)throw Error("remote_invalid_input");auto unicode=wide(text);std::vector<INPUT> letters;letters.reserve(unicode.size()*2);
   for(auto code:unicode){INPUT letter{};letter.type=INPUT_KEYBOARD;letter.ki.wScan=WORD(code);letter.ki.dwFlags=KEYEVENTF_UNICODE;letters.push_back(letter);letter.ki.dwFlags|=KEYEVENTF_KEYUP;letters.push_back(letter);}
   if(SendInput(UINT(letters.size()),letters.data(),sizeof(INPUT))!=letters.size()){release();throw Error("remote_input_unavailable");}return;
  }else if(type=="release"){release();return;}else throw Error("remote_invalid_input");
  if(SendInput(1,&in,sizeof(in))!=1)throw Error("remote_input_unavailable");
  if(type=="key"){if(down)keys.insert(tracked);else keys.erase(tracked);}
  if(type=="button"){if(down)buttons.insert(tracked);else buttons.erase(tracked);}
 }
};
struct Capture {
 Microsoft::WRL::ComPtr<ID3D11Texture2D> texture;video::Frame frame;Gate* gate;Input* input;std::string error;std::function<bool()> allowed;
 static void read(void* ctx,const capture::Surface& s)noexcept{
  auto& c=*static_cast<Capture*>(ctx);try{
   if(!c.allowed()||!c.gate->check())throw Error("remote_closed");
   if(s.clockwise_rotation||s.protected_content_masked)throw Error("remote_display_unavailable");
   auto size=video::fit_size(s.width,s.height);
   if(c.frame.width&&(c.frame.width!=size.width||c.frame.height!=size.height))throw Error("remote_display_changed");
   D3D11_TEXTURE2D_DESC desc{};s.texture->GetDesc(&desc);
   if(c.texture){D3D11_TEXTURE2D_DESC prior{};c.texture->GetDesc(&prior);if(prior.Width!=desc.Width||prior.Height!=desc.Height)c.texture.Reset();}
   if(!c.texture){desc.BindFlags=0;desc.MiscFlags=0;desc.Usage=D3D11_USAGE_STAGING;desc.CPUAccessFlags=D3D11_CPU_ACCESS_READ;if(FAILED(s.device->CreateTexture2D(&desc,nullptr,c.texture.GetAddressOf())))throw Error("remote_capture_failed");}
   s.context->CopyResource(c.texture.Get(),s.texture);D3D11_MAPPED_SUBRESOURCE map{};if(FAILED(s.context->Map(c.texture.Get(),0,D3D11_MAP_READ,0,&map)))throw Error("remote_capture_failed");
   try{c.frame=video::bgra_to_nv12(static_cast<const unsigned char*>(map.pData),s.width,s.height,map.RowPitch,size.width,size.height);}catch(...){s.context->Unmap(c.texture.Get(),0);throw;}s.context->Unmap(c.texture.Get(),0);
   c.input->left=s.desktop_left;c.input->top=s.desktop_top;c.input->screenWidth=s.width;c.input->screenHeight=s.height;
  }catch(const std::exception& e){c.error=e.what();}
 }
};
void closeSession(const std::string& site,const std::string& id){auto deadline=Clock::now()+std::chrono::seconds(3);try{call(site,"DELETE",std::string(base)+"/sessions/"+id,nullptr,{},[&]{return Clock::now()<deadline;});}catch(...){}}
}

Json inventory(const std::string& site,const std::function<bool()>& active){return call(site,"GET",base,nullptr,{},active);}

void host(const std::string& site,Callbacks cb){
 auto ctx=context(site,cb.running);std::string raw=credentialRead("device/"+ctx.at("credential_scope").get<std::string>());if(raw.size()!=32||publicKey(raw)!=ctx.at("public_key").get<std::string>()){wipe(raw);throw Error("remote_device_key_missing");}Key seed{};std::copy(raw.begin(),raw.end(),seed.begin());wipe(raw);
 Key random{};if(RAND_bytes(random.data(),16)!=1)throw Error("remote_random_failed");auto boot=base64url(random.data(),16);
 std::string ip=ctx.at("lease").at("ip"),device=ctx.at("device_id");
 remote::Listener listener(ip,29990,seed,cb.running);SecureZeroMemory(seed.data(),seed.size());
 wchar_t computer[128]{};DWORD count=128;GetComputerNameW(computer,&count);
 uint32_t available=NG_REMOTE_VIEW|NG_REMOTE_INPUT;try{audio::Stream probe(true);available|=NG_REMOTE_AUDIO;}catch(...){}
 Json registration={{"id",device},{"name",utf8(computer)},{"platform","windows"},{"boot",boot},{"enabled",true},{"capabilities",available},{"require_consent",true},{"port",listener.port()}};
 call(site,"PUT",std::string(base)+"/host",registration,{},cb.running);
 try{
  Monitor monitor(site,boot,cb);cb.status("等待已授权的远程连接；每次连接需要本机确认");
  while(monitor.live()){
   std::unique_ptr<Connection> connection;
   try{connection=listener.accept(100);}catch(...){if(!monitor.live())break;Sleep(100);continue;}
   if(!connection)continue;
   std::string id;
   try{
    auto hello=json(receive(*connection,Kind::hello,[&]{return monitor.live();}));id=hello.at("id");
    auto requestedAt=remote::monotonic_time_ms();auto a=call(site,"GET",std::string(base)+"/sessions/"+id,nullptr,{},cb.running);auto& session=a.at("session");
    if(session.at("host_id")!=device||session.at("host_boot")!=boot||session.at("state")!="issued"||hello.at("ticket")!=a.at("ticket"))throw Error("remote_authorization_mismatch");
    auto wire=decode(a.at("ticket"),NG_REMOTE_TICKET_SIZE);Gate gate(a,ctx,requestedAt);gate.begin(wire);
    Key challenge{};if(RAND_bytes(challenge.data(),challenge.size())!=1)throw Error("remote_random_failed");auto channel=connection->exporter();
    connection->send({Kind::challenge,{challenge.begin(),challenge.end()}});auto proof=receive(*connection,Kind::proof,[&]{return monitor.live();});auto controller=key(a.at("controller_key"));
    if(proof.body.size()!=64||ng_remote_possession_verify(controller.data(),wire.data(),wire.size(),challenge.data(),channel.data(),proof.body.data())!=NG_REMOTE_OK)throw Error("remote_proof_invalid");
    if(!cb.consent(session.at("user_id"),session.at("capabilities").get<uint32_t>()))throw Error("remote_consent_denied");
    if(ng_remote_gate_consent(gate.value,1)!=NG_REMOTE_OK||!gate.check())throw Error("remote_ticket_expired");
    call(site,"POST",std::string(base)+"/sessions/"+id+"/consume",{{"sequence",session.at("sequence")},{"boot",boot},{"proof",{{"challenge",base64url(challenge.data(),challenge.size())},{"channel",base64url(channel.data(),channel.size())},{"signature",base64url(proof.body.data(),proof.body.size())}}}});
    connection->authorize([&]{return monitor.live()&&gate.check();});
    monitor.bind(id);
    capture::DesktopCapture capture;if(capture.open()!=capture::Result::frame)throw Error("remote_capture_unavailable");Input input;
    Capture pixels{{},{},&gate,&input,{},[&]{return monitor.live();}};auto started=Clock::now(),next=started;
    auto captureDeadline=Clock::now()+std::chrono::seconds(3);
    while(pixels.frame.nv12.empty()&&Clock::now()<captureDeadline&&monitor.live()&&gate.check()){
     auto result=capture.next(Capture::read,&pixels,16);
     if(!pixels.error.empty())throw Error(pixels.error);
     if(result!=capture::Result::frame&&result!=capture::Result::timeout)throw Error("remote_capture_lost");
    }
    if(pixels.frame.nv12.empty())throw Error("remote_capture_unavailable");
    video::Codec encoder(video::Codec::Mode::encode,pixels.frame.width,pixels.frame.height,fps,3'000'000);
    std::unique_ptr<audio::Stream> sound;if(gate.check(NG_REMOTE_AUDIO))sound=std::make_unique<audio::Stream>(true);
    connection->send(message(Kind::accepted,{{"width",pixels.frame.width},{"height",pixels.frame.height},{"fps",fps},{"capabilities",session.at("capabilities")}}));cb.status("正在共享桌面；点击“停止”结束共享");
    while(monitor.live()&&gate.check()){
     Message m;if(connection->receive(m,0)){
      if(m.kind==Kind::close)break;
      if(m.kind==Kind::renew){
       auto update=json(m);auto bytes=decode(update.at("ticket"),NG_REMOTE_TICKET_SIZE);ng_remote_claims claims{};claims.struct_size=sizeof(claims);claims.abi_version=NG_REMOTE_ABI;
       if(ng_remote_ticket_verify(gate.verifier,bytes.data(),bytes.size(),&claims)!=NG_REMOTE_OK)throw Error("remote_renewal_invalid");
       auto requested=remote::monotonic_time_ms();auto fresh=call(site,"GET",std::string(base)+"/sessions/"+id,nullptr,{},cb.running);
       gate.clock.observe(fresh.value("server_unix_ms",uint64_t(0)),requested);
       if(claims.sequence>fresh.at("session").at("sequence").get<uint64_t>()||ng_remote_gate_renew(gate.value,bytes.data(),bytes.size())!=NG_REMOTE_OK)throw Error("remote_renewal_invalid");
      }
      else if(m.kind==Kind::input){auto events=json(m);if(!remote::valid_input_batch(events))throw Error("remote_invalid_input");for(const auto& event:events){if(!monitor.live()||!gate.check(NG_REMOTE_INPUT))throw Error("remote_input_forbidden");input.apply(event);}}
      else throw Error("remote_unexpected_message");
     }
     if(sound&&gate.check(NG_REMOTE_AUDIO)){for(auto& packet:sound->capture()){if(!monitor.live()||!gate.check(NG_REMOTE_AUDIO))break;connection->send({Kind::audio,std::move(packet)});}}
     if(Clock::now()<next){Sleep(2);continue;}next=Clock::now()+std::chrono::milliseconds(1000/fps);
     if(!gate.check())break;
     auto captured=capture.next(Capture::read,&pixels,16);if(!pixels.error.empty())throw Error(pixels.error);if(captured!=capture::Result::frame&&captured!=capture::Result::timeout)throw Error("remote_capture_lost");if(pixels.frame.nv12.empty())continue;
     if(!monitor.live()||!gate.check())break;
     auto stamp=std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now()-started).count()/100;
     for(auto& packet:encoder.encode(pixels.frame,stamp)){if(!monitor.live()||!gate.check())break;connection->send({Kind::video,std::move(packet.h264)});}
    }
    input.release();ng_remote_gate_revoke(gate.value);
   }catch(const std::exception& e){cb.status(std::string("远控已停止：")+e.what());}
   connection.reset();
   if(!id.empty())closeSession(site,id);
   monitor.bind({});
  }
 }catch(...){registration["enabled"]=false;try{call(site,"PUT",std::string(base)+"/host",registration);}catch(...){}throw;}
 registration["enabled"]=false;try{call(site,"PUT",std::string(base)+"/host",registration);}catch(...){}
}

void view(const std::string& site,const std::string& hostID,uint32_t capabilities,Callbacks cb){
 struct Bridge {std::string site;Callbacks cb;std::unique_ptr<video::Codec> decoder;std::unique_ptr<audio::Stream> sound;};Bridge bridge{site,std::move(cb),{}, {}};
 ng_remote_client_options options{};options.struct_size=sizeof(options);options.abi_version=NG_REMOTE_CLIENT_ABI;options.site_id=site.c_str();options.host_id=hostID.c_str();options.capabilities=capabilities;options.context=&bridge;
 options.active=[](void* p)->int32_t{return static_cast<Bridge*>(p)->cb.running()?1:0;};
 options.control=[](void* p,const char* method,const char* path,const char* body,const char* idem,char* output,size_t capacity,size_t* length)->int32_t{try{
  auto& b=*static_cast<Bridge*>(p);auto active=b.cb.running;
  // Cancelling the viewer must still close its issued/active server session.
  // This borrows the existing VPN identity with a separate cleanup deadline.
  if(std::string(method)=="DELETE"){auto end=Clock::now()+std::chrono::seconds(3);active=[end]{return Clock::now()<end;};}
  auto value=std::string(method)=="CONTEXT"?context(b.site,active):call(b.site,method,path,*body?Json::parse(body):Json(),idem,active);auto text=value.dump();if(text.size()>capacity)return -1;std::copy(text.begin(),text.end(),output);*length=text.size();return 0;
 }catch(...){return -1;}};
 options.sign=[](void* p,const uint8_t* bytes,size_t size,uint8_t output[64])->int32_t{try{
  auto& b=*static_cast<Bridge*>(p);auto ctx=context(b.site,b.cb.running);auto seed=credentialRead("device/"+ctx.at("credential_scope").get<std::string>());if(seed.size()!=32||publicKey(seed)!=ctx.at("public_key").get<std::string>()){wipe(seed);return -1;}
  auto privateKey=std::unique_ptr<EVP_PKEY,decltype(&EVP_PKEY_free)>(EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519,nullptr,reinterpret_cast<const uint8_t*>(seed.data()),seed.size()),EVP_PKEY_free);wipe(seed);
  auto signer=std::unique_ptr<EVP_MD_CTX,decltype(&EVP_MD_CTX_free)>(EVP_MD_CTX_new(),EVP_MD_CTX_free);size_t n=64;return privateKey&&signer&&EVP_DigestSignInit(signer.get(),nullptr,nullptr,nullptr,privateKey.get())==1&&EVP_DigestSign(signer.get(),output,&n,bytes,size)==1&&n==64?0:-1;
 }catch(...){return -1;}};
 options.input=[](void* p,char* output,size_t capacity,size_t* length)->int32_t{try{auto text=Json(static_cast<Bridge*>(p)->cb.inputs()).dump();if(text.size()>capacity)return -1;std::copy(text.begin(),text.end(),output);*length=text.size();return 0;}catch(...){return -1;}};
 options.format=[](void* p,uint32_t w,uint32_t h,uint32_t rate,uint32_t caps)->int32_t{try{auto& b=*static_cast<Bridge*>(p);b.decoder=std::make_unique<video::Codec>(video::Codec::Mode::decode,w,h,rate);if(caps&NG_REMOTE_AUDIO)b.sound=std::make_unique<audio::Stream>(false);if(b.cb.permissions)b.cb.permissions(caps);return 0;}catch(...){return -1;}};
 options.audio=[](void* p,const uint8_t* bytes,size_t size)->int32_t{try{static_cast<Bridge*>(p)->sound->play(bytes,size);return 0;}catch(...){return -1;}};
 options.video=[](void* p,const uint8_t* bytes,size_t size,int64_t stamp)->int32_t{try{auto& b=*static_cast<Bridge*>(p);for(auto& frame:b.decoder->decode({stamp,false,Bytes(bytes,bytes+size)}))b.cb.present({frame.width,frame.height,video::nv12_to_bgra(frame)});return 0;}catch(...){return -1;}};
 options.status=[](void* p,const char* code){auto& b=*static_cast<Bridge*>(p);try{
  std::string text=code;
  if(text=="remote_connected")text="已连接；点击“停止”结束远程桌面";
  else if(text=="remote_waiting_consent")text="等待对方确认…";
  else if(text=="remote_clock_invalid"||text=="remote_generation_invalid")text="服务端远控信息不完整，请同步更新局域网服务端与客户端";
  else if(text=="remote_host_busy")text="对方正在接受其他远程连接，请稍后刷新主机列表";
  else if(text=="remote_host_unavailable")text="对方暂不可连接，请确认其 VPN 已连接并开启桌面共享";
  else if(text=="remote_view_forbidden")text="当前账号未获该主机的观看授权";
  else if(text=="remote_disconnected")text="远程桌面已结束";
  else if(text=="remote_decoder_unavailable")text="当前系统无法解码此远程画面";
  b.cb.status(std::move(text));
 }catch(...){}};
 ng_remote_client* raw=nullptr;if(ng_remote_client_create(&options,&raw)!=NG_REMOTE_CLIENT_OK)throw Error("remote_client_failed");std::unique_ptr<ng_remote_client,decltype(&ng_remote_client_destroy)> client(raw,ng_remote_client_destroy);ng_remote_client_run(client.get());
}
}
