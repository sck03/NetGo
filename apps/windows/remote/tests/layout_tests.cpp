#include "display_layout.hpp"
#include "video_codec.hpp"
#include <stdexcept>
#include <iostream>
#include <cmath>
#include <algorithm>
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
 // Compare padded, borrowed planes against the scalar color definition.
 std::vector<std::uint8_t> yplane(32*18), uvplane(32*9);
 for(size_t i=0;i<yplane.size();++i)yplane[i]=std::uint8_t(i*71+13);
 for(size_t i=0;i<uvplane.size();++i)uvplane[i]=std::uint8_t(i*37+29);
 netgo::video::FrameView view{16,18,32,yplane,uvplane};
 netgo::video::nv12_to_bgra(view,bgra);
 auto clip=[](int v){return std::uint8_t(std::clamp(v,0,255));};
 for(unsigned y=0;y<18;++y)for(unsigned x=0;x<16;++x){
  int c=yplane[y*32+x]-16,d=uvplane[(y/2)*32+(x&~1u)]-128,e=uvplane[(y/2)*32+(x&~1u)+1]-128;
  auto p=(y*16+x)*4;check(bgra[p]==clip((298*c+516*d+128)>>8));check(bgra[p+1]==clip((298*c-100*d-208*e+128)>>8));check(bgra[p+2]==clip((298*c+409*e+128)>>8));check(bgra[p+3]==255);
 }
 view.uv=view.uv.first(10);invalid=false;try{netgo::video::nv12_to_bgra(view,bgra);}catch(const std::invalid_argument&){invalid=true;}check(invalid);
 std::cout<<"PASS aspect-preserving video size, letterbox input exclusion, drag clamp and minimized window\n";return 0;
 }catch(const std::exception& e){std::cerr<<e.what()<<'\n';return 1;}}
