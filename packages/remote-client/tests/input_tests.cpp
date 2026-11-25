#include "remote_input.hpp"
#include "netgo_remote_client.h"
#include <iostream>
#include <stdexcept>

using namespace netgo::remote;
void check(bool value,const char* message){if(!value)throw std::runtime_error(message);}
int main(){try{
 InputQueue q;
 for(unsigned x=0;x<10000;++x)check(q.push({{"type","move"},{"x",x},{"y",0}},100),"move");
 auto events=q.take(101);check(events.size()==1&&events[0]["x"]==9999,"move coalescing");
 q.push({{"type","key"},{"code",17},{"down",true}},100);
 q.push({{"type","button"},{"button",1},{"down",true}},101);
 q.push({{"type","release"}},102);events=q.take(103);
 check(events.size()==1&&events[0]["type"]=="release","focus release replayed queued input");
 for(unsigned i=0;i<129;++i)q.push({{"type","key"},{"code",65},{"down",true}},100);
 events=q.take(101);check(events.size()==2&&events[0]["type"]=="release","event overflow failed open");
 for(unsigned i=0;i<10;++i)q.push({{"type","text"},{"text",std::string(8192,'\n')}},100);
 events=q.take(101);check(valid_input_batch(events)&&events.dump().size()<=input_batch_limit&&events[0]["type"]=="release","escaped text byte budget");
 q.push({{"type","key"},{"code",65},{"down",true}},100);events=q.take(1100);
 check(events.size()==1&&events[0]["type"]=="release","stale input replayed");
 q.push({{"type","key"},{"code",65},{"down",true}},2000);events=q.take(1999);
 check(events.size()==1&&events[0]["type"]=="release","clock rollback replayed input");
 for(const auto& event:InputEvent::array({{{"type","move"},{"x",-1},{"y",0}},{{"type","key"},{"code",256},{"down",true}},{{"type","text"},{"text",std::string(8193,'a')}},{{"type","release"},{"extra",true}},{{"type","button"},{"button",1},{"down",1}}}))check(!q.push(event),"invalid input accepted");
 check(!valid_input_batch(InputEvent::array({{{"type","release"}},{{"type","key"},{"code",0},{"down",true}}})),"mixed invalid batch accepted");
 auto input=ng_remote_input_create();check(input!=nullptr,"queue allocation");
 const std::string key=R"({"type":"key","code":65,"down":true})";
 check(ng_remote_input_push(input,key.data(),key.size())==0,"C push");
 char output[65536];size_t size=99;check(ng_remote_input_read(input,output,1,&size)==NG_REMOTE_CLIENT_INVALID&&size==0,"small output");
 check(ng_remote_input_read(input,output,sizeof(output),&size)==0&&InputEvent::parse(output,output+size).size()==1,"small read consumed input");
 ng_remote_input_destroy(input);
 std::cout<<"PASS bounded input, text, focus, expiry, validation and C ABI\n";return 0;
 }catch(const std::exception& e){std::cerr<<e.what()<<'\n';return 1;}}
