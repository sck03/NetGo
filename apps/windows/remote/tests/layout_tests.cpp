#include "display_layout.hpp"
#include "video_codec.hpp"
#include <stdexcept>
#include <iostream>
#include <cmath>
void check(bool ok){if(!ok)throw std::runtime_error("display layout mismatch");}
int main(){try{
 using namespace netgo::desktop::layout;
 auto box=fit({8,82,808,532},1920,1080);check(box.left==8&&box.top==82&&box.right==808&&box.bottom==532);
 box=fit({0,0,800,600},1920,1080);check(box.left==0&&box.top==75&&box.right==800&&box.bottom==525);
 check(!map(box,200,74));auto p=map(box,799,524);check(p&&p->x==65535&&p->y==65535);
 p=map(box,-100,1000,true);check(p&&p->x==0&&p->y==65535);
 box=fit({0,0,800,600},1024,768);check(box.right==800&&box.bottom==600);
 box=fit({0,0,800,600},1080,1920);check(box.left==231&&box.right==568&&box.top==0&&box.bottom==600);
 check(!map(fit({0,0,0,0},1280,720),0,0));check(!map(fit({0,0,800,600},0,0),1,1));
 for(auto input: {netgo::video::Size{1920,1080},{3840,2160},{1600,1200},{1024,768},{640,480},{1080,1920}}){
  auto size=netgo::video::fit_size(input.width,input.height);
  check(size.width<=1280&&size.height<=720&&size.width<=input.width&&size.height<=input.height&&!(size.width&1)&&!(size.height&1));
  check(std::abs(double(size.width)/size.height-double(input.width)/input.height)<0.01);
 }
 auto small=netgo::video::fit_size(640,480);check(small.width==640&&small.height==480);
 // Fixed session canvas must survive a smaller source and an aspect ratio change.
 netgo::video::Frame converted;
 std::vector<std::uint8_t> pixels(640*480*4,255), bgra;
 netgo::video::bgra_to_nv12(pixels.data(),640,480,640*4,1280,720,converted);
 check(converted.width==1280&&converted.height==720&&converted.nv12.front()==235&&converted.nv12[1280*720-1]==235);
 netgo::video::nv12_to_bgra(converted,bgra);check(bgra.size()==1280*720*4&&bgra.front()>250);
 auto storage=bgra.data();netgo::video::nv12_to_bgra(converted,bgra);check(storage==bgra.data());
 bool invalid=false;try{netgo::video::bgra_to_nv12(pixels.data(),0,480,640*4,1280,720,converted);}catch(const std::invalid_argument&){invalid=true;}check(invalid);
 std::cout<<"PASS aspect-preserving video size, letterbox input exclusion, drag clamp and minimized window\n";return 0;
 }catch(const std::exception& e){std::cerr<<e.what()<<'\n';return 1;}}
