#include "NetGoApple.h"
#include <algorithm>
#include <cstring>
#include <string>
#include <sys/socket.h>
#include <unistd.h>
#include <sys/stat.h>
#include <TargetConditionals.h>
#if TARGET_OS_OSX
#include <curl/curl.h>
#endif

extern "C" int32_t ng_apple_tunnel_descriptor(char* name,size_t capacity){
 if(!name||capacity<32)return -1;
 // UTUN_OPT_IFNAME is a public control-socket option; no private packetFlow KVC.
 for(int fd=0;fd<1024;++fd){char current[32]{};socklen_t size=sizeof(current);
  if(getsockopt(fd,2,2,current,&size)==0&&std::strncmp(current,"utun",4)==0){
   int copy=dup(fd);if(copy>=0){std::strncpy(name,current,capacity-1);name[capacity-1]=0;return copy;}
  }
 }
 return -1;
}
#if TARGET_OS_OSX
namespace {struct Reply{char* out;size_t cap,size=0;};
size_t append(char* data,size_t unit,size_t count,void* context){auto& r=*static_cast<Reply*>(context);size_t size=unit*count;if(size>r.cap-r.size)return 0;std::memcpy(r.out+r.size,data,size);r.size+=size;return size;}}
extern "C" int32_t ng_apple_ipc(const char* socket,const char* method,const char* path,const char* body,char* output,size_t capacity,size_t* length){
 if(!socket||!method||!path||!output||!length||path[0]!='/'||capacity>1048576)return -1;
 struct stat st{};if(lstat(socket,&st)||!S_ISSOCK(st.st_mode)||st.st_uid!=getuid()||(st.st_mode&0077))return -2;
 CURL* handle=curl_easy_init();if(!handle)return -3;Reply reply{output,capacity};
 std::string url=std::string("http://localhost")+path;
 curl_slist* headers=curl_slist_append(nullptr,"Content-Type: application/json");
 curl_easy_setopt(handle,CURLOPT_UNIX_SOCKET_PATH,socket);curl_easy_setopt(handle,CURLOPT_URL,url.c_str());
 curl_easy_setopt(handle,CURLOPT_PROXY,"");curl_easy_setopt(handle,CURLOPT_CUSTOMREQUEST,method);
 curl_easy_setopt(handle,CURLOPT_HTTPHEADER,headers);curl_easy_setopt(handle,CURLOPT_CONNECTTIMEOUT_MS,1000L);
 curl_easy_setopt(handle,CURLOPT_TIMEOUT_MS,22000L);curl_easy_setopt(handle,CURLOPT_NOSIGNAL,1L);
 curl_easy_setopt(handle,CURLOPT_WRITEFUNCTION,append);curl_easy_setopt(handle,CURLOPT_WRITEDATA,&reply);
 if(body&&*body){curl_easy_setopt(handle,CURLOPT_POSTFIELDS,body);curl_easy_setopt(handle,CURLOPT_POSTFIELDSIZE,long(std::strlen(body)));}
 auto result=curl_easy_perform(handle);long status=0;curl_easy_getinfo(handle,CURLINFO_RESPONSE_CODE,&status);
 curl_slist_free_all(headers);curl_easy_cleanup(handle);*length=reply.size;
 return result==CURLE_OK&&status>=200&&status<300?0:-4;
}
#else
extern "C" int32_t ng_apple_ipc(const char*,const char*,const char*,const char*,char*,size_t,size_t*){return -1;}
#endif
