#include <jni.h>
#include "netgo_remote_client.h"
#include "netgo_remote.h"
#include <nlohmann/json.hpp>
#include <atomic>
#include <memory>
#include <mutex>
#include <thread>
#include <unordered_map>
#include <deque>
#include <cstring>

namespace {
using Json=nlohmann::json;
struct Env {
 JavaVM* vm;JNIEnv* value=nullptr;bool detach=false;
 explicit Env(JavaVM* v):vm(v){if(vm->GetEnv(reinterpret_cast<void**>(&value),JNI_VERSION_1_6)!=JNI_OK){if(vm->AttachCurrentThread(&value,nullptr)==JNI_OK)detach=true;else value=nullptr;}}
 ~Env(){if(detach)vm->DetachCurrentThread();}
};
struct Session {JavaVM* vm=nullptr;jobject object=nullptr;std::string site,host;std::atomic_bool active=true;std::mutex mutex;ng_remote_client* client=nullptr;std::deque<Json> inputs;};
std::mutex registryMutex;std::unordered_map<jlong,std::shared_ptr<Session>> sessions;std::atomic<jlong> next{1};
std::shared_ptr<Session> get(jlong handle){std::lock_guard lock(registryMutex);auto found=sessions.find(handle);return found==sessions.end()?nullptr:found->second;}
bool healthy(JNIEnv* env){if(env->ExceptionCheck()){env->ExceptionClear();return false;}return true;}
std::string text(JNIEnv* env,jstring value){if(!value)return {};auto bytes=env->GetStringUTFChars(value,nullptr);if(!bytes)return {};std::string result(bytes);env->ReleaseStringUTFChars(value,bytes);return result;}
jmethodID method(JNIEnv* env,Session& s,const char* name,const char* signature){auto type=env->GetObjectClass(s.object);if(!type)return nullptr;auto result=env->GetMethodID(type,name,signature);env->DeleteLocalRef(type);return result;}
void status(Session& s,const char* code){Env env(s.vm);if(!env.value||!s.active)return;auto id=method(env.value,s,"onStatus","(Ljava/lang/String;)V");auto value=env.value->NewStringUTF(code);if(id&&value)env.value->CallVoidMethod(s.object,id,value);if(value)env.value->DeleteLocalRef(value);healthy(env.value);}
int32_t control(void* context,const char* action,const char* path,const char* body,const char* key,char* output,size_t capacity,size_t* length){
 auto& s=*static_cast<Session*>(context);Env env(s.vm);if(!env.value)return -1;auto id=method(env.value,s,"control","(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)[B");if(!id||!healthy(env.value))return -1;
 auto a=env.value->NewStringUTF(action),p=env.value->NewStringUTF(path),b=env.value->NewStringUTF(body),k=env.value->NewStringUTF(key);
 auto result=static_cast<jbyteArray>(env.value->CallObjectMethod(s.object,id,a,p,b,k));for(auto value:{a,p,b,k})if(value)env.value->DeleteLocalRef(value);
 if(!healthy(env.value)||!result)return -1;auto size=env.value->GetArrayLength(result);int32_t ok=-1;if(size>0&&size_t(size)<=capacity){env.value->GetByteArrayRegion(result,0,size,reinterpret_cast<jbyte*>(output));*length=size_t(size);ok=healthy(env.value)?0:-1;}env.value->DeleteLocalRef(result);return ok;
}
int32_t sign(void* context,const uint8_t* bytes,size_t size,uint8_t output[64]){
 auto& s=*static_cast<Session*>(context);Env env(s.vm);if(!env.value)return -1;auto id=method(env.value,s,"sign","([B)[B");if(!id||!healthy(env.value))return -1;auto input=env.value->NewByteArray(jsize(size));if(!input)return -1;env.value->SetByteArrayRegion(input,0,jsize(size),reinterpret_cast<const jbyte*>(bytes));auto result=static_cast<jbyteArray>(env.value->CallObjectMethod(s.object,id,input));env.value->DeleteLocalRef(input);if(!healthy(env.value)||!result)return -1;bool ok=env.value->GetArrayLength(result)==64;if(ok)env.value->GetByteArrayRegion(result,0,64,reinterpret_cast<jbyte*>(output));env.value->DeleteLocalRef(result);return ok&&healthy(env.value)?0:-1;
}
int32_t packet(Session& s,const char* name,const uint8_t* bytes,size_t size,int64_t stamp){
 Env env(s.vm);if(!env.value||!s.active)return -1;bool video=std::strcmp(name,"onVideo")==0;auto id=method(env.value,s,name,video?"([BJ)Z":"([B)Z");if(!id||!healthy(env.value))return -1;auto data=env.value->NewByteArray(jsize(size));if(!data)return -1;env.value->SetByteArrayRegion(data,0,jsize(size),reinterpret_cast<const jbyte*>(bytes));auto ok=video?env.value->CallBooleanMethod(s.object,id,data,jlong(stamp)):env.value->CallBooleanMethod(s.object,id,data);env.value->DeleteLocalRef(data);return healthy(env.value)&&ok?0:-1;
}
void run(jlong handle,std::shared_ptr<Session> s){
 ng_remote_client_options o{};o.struct_size=sizeof(o);o.abi_version=NG_REMOTE_CLIENT_ABI;o.site_id=s->site.c_str();o.host_id=s->host.c_str();o.capabilities=NG_REMOTE_VIEW|NG_REMOTE_INPUT|NG_REMOTE_AUDIO;o.context=s.get();
 o.active=[](void* p)->int32_t{return static_cast<Session*>(p)->active?1:0;};o.control=control;o.sign=sign;
 o.input=[](void* p,char* output,size_t capacity,size_t* length)->int32_t{try{auto& s=*static_cast<Session*>(p);std::lock_guard lock(s.mutex);auto value=Json(s.inputs).dump(-1,' ',true);s.inputs.clear();if(value.size()>capacity)return -1;std::memcpy(output,value.data(),value.size());*length=value.size();return 0;}catch(...){return -1;}};
 o.format=[](void* p,uint32_t w,uint32_t h,uint32_t rate,uint32_t caps)->int32_t{auto& s=*static_cast<Session*>(p);Env env(s.vm);if(!env.value||!s.active)return -1;auto id=method(env.value,s,"onFormat","(IIII)Z");if(!id||!healthy(env.value))return -1;auto ok=env.value->CallBooleanMethod(s.object,id,jint(w),jint(h),jint(rate),jint(caps));return healthy(env.value)&&ok?0:-1;};
 o.video=[](void* p,const uint8_t* bytes,size_t size,int64_t stamp)->int32_t{return packet(*static_cast<Session*>(p),"onVideo",bytes,size,stamp);};o.audio=[](void* p,const uint8_t* bytes,size_t size)->int32_t{return packet(*static_cast<Session*>(p),"onAudio",bytes,size,0);};o.status=[](void* p,const char* code){status(*static_cast<Session*>(p),code);};
 ng_remote_client* client=nullptr;if(s->active&&ng_remote_client_create(&o,&client)==0){{std::lock_guard lock(s->mutex);s->client=client;}ng_remote_client_run(client);{std::lock_guard lock(s->mutex);s->client=nullptr;}ng_remote_client_destroy(client);}
 s->active=false;Env env(s->vm);if(env.value){auto id=method(env.value,*s,"onClosed","()V");if(id)env.value->CallVoidMethod(s->object,id);healthy(env.value);env.value->DeleteGlobalRef(s->object);s->object=nullptr;}
 std::lock_guard lock(registryMutex);sessions.erase(handle);
}
}
extern "C" JNIEXPORT jlong JNICALL Java_org_netgo_client_NativeRemote_startNative(JNIEnv* env,jobject object,jstring site,jstring host){try{
 auto s=std::make_shared<Session>();env->GetJavaVM(&s->vm);s->object=env->NewGlobalRef(object);s->site=text(env,site);s->host=text(env,host);if(!s->object||s->site.empty()||s->site.size()>63||s->host.empty()||s->host.size()>128){if(s->object)env->DeleteGlobalRef(s->object);return 0;}auto handle=next.fetch_add(1);{std::lock_guard lock(registryMutex);sessions.emplace(handle,s);}try{std::thread(run,handle,s).detach();}catch(...){std::lock_guard lock(registryMutex);sessions.erase(handle);env->DeleteGlobalRef(s->object);return 0;}return handle;
 }catch(...){return 0;}}
extern "C" JNIEXPORT void JNICALL Java_org_netgo_client_NativeRemote_cancelNative(JNIEnv*,jobject,jlong handle){auto s=get(handle);if(!s)return;s->active=false;std::lock_guard lock(s->mutex);if(s->client)ng_remote_client_cancel(s->client);s->inputs.clear();}
extern "C" JNIEXPORT void JNICALL Java_org_netgo_client_NativeRemote_inputNative(JNIEnv* env,jobject,jlong handle,jbyteArray value){try{auto s=get(handle);if(!s||!s->active||!value)return;auto size=env->GetArrayLength(value);if(size<=0||size>16384)return;std::string input(size_t(size),'\0');env->GetByteArrayRegion(value,0,size,reinterpret_cast<jbyte*>(input.data()));if(!healthy(env))return;auto event=Json::parse(input);std::lock_guard lock(s->mutex);if(event.value("type","")=="move"&&!s->inputs.empty()&&s->inputs.back().value("type","")=="move")s->inputs.back()=std::move(event);else{if(s->inputs.size()>=128){s->inputs.clear();s->inputs.push_back({{"type","release"}});}s->inputs.push_back(std::move(event));}}catch(...){}}
