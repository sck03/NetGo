#include "client.hpp"
#include <curl/curl.h>
#include <sys/stat.h>
#include <unistd.h>
#include <stdexcept>
namespace netgo::desktop_linux {
namespace {
struct Reply {std::string text;std::atomic_bool* active;bool oversized=false;};
size_t append(char* data,size_t size,size_t count,void* p){auto& r=*static_cast<Reply*>(p);constexpr size_t limit=4u<<20;if(size && count>limit/size){r.oversized=true;return 0;}auto bytes=size*count;if(bytes>limit-r.text.size()){r.oversized=true;return 0;}try{r.text.append(data,bytes);return bytes;}catch(...){return 0;}}
int progress(void* p,curl_off_t,curl_off_t,curl_off_t,curl_off_t){return static_cast<Reply*>(p)->active->load()?0:1;}
}
Json ipc(const std::string& socket,const std::string& method,const std::string& path,const Json& value,std::atomic_bool& active,long timeout){
	if(!active)throw std::runtime_error("control_cancelled");
 struct stat st{};if(lstat(socket.c_str(),&st)!=0||!S_ISSOCK(st.st_mode)||st.st_uid!=getuid()||(st.st_mode&0077))throw std::runtime_error("client_service_unavailable");
 auto curl=std::unique_ptr<CURL,decltype(&curl_easy_cleanup)>(curl_easy_init(),curl_easy_cleanup);if(!curl)throw std::runtime_error("ipc_failed");
 Reply reply{{},&active};auto body=value.is_null()?std::string():value.dump();auto url="http://localhost"+path;
 auto headers=std::unique_ptr<curl_slist,decltype(&curl_slist_free_all)>(curl_slist_append(nullptr,"Content-Type: application/json"),curl_slist_free_all);
 curl_easy_setopt(curl.get(),CURLOPT_UNIX_SOCKET_PATH,socket.c_str());curl_easy_setopt(curl.get(),CURLOPT_URL,url.c_str());curl_easy_setopt(curl.get(),CURLOPT_CUSTOMREQUEST,method.c_str());curl_easy_setopt(curl.get(),CURLOPT_HTTPHEADER,headers.get());
 curl_easy_setopt(curl.get(),CURLOPT_PROXY,"");curl_easy_setopt(curl.get(),CURLOPT_CONNECTTIMEOUT_MS,1000L);curl_easy_setopt(curl.get(),CURLOPT_TIMEOUT_MS,timeout);curl_easy_setopt(curl.get(),CURLOPT_NOSIGNAL,1L);
 curl_easy_setopt(curl.get(),CURLOPT_WRITEFUNCTION,append);curl_easy_setopt(curl.get(),CURLOPT_WRITEDATA,&reply);curl_easy_setopt(curl.get(),CURLOPT_NOPROGRESS,0L);curl_easy_setopt(curl.get(),CURLOPT_XFERINFOFUNCTION,progress);curl_easy_setopt(curl.get(),CURLOPT_XFERINFODATA,&reply);
 if(!body.empty()){curl_easy_setopt(curl.get(),CURLOPT_POSTFIELDS,body.c_str());curl_easy_setopt(curl.get(),CURLOPT_POSTFIELDSIZE,long(body.size()));}
 auto result=curl_easy_perform(curl.get());long status=0;curl_easy_getinfo(curl.get(),CURLINFO_RESPONSE_CODE,&status);
 if(result!=CURLE_OK)throw std::runtime_error(!active?"control_cancelled":reply.oversized?"control_response_too_large":result==CURLE_OPERATION_TIMEDOUT?"control_timeout":"ipc_failed");
 auto response=Json::parse(reply.text);
 if(status<200||status>=300)throw std::runtime_error(response.value("error","ipc_failed"));
 return response;
}
}
