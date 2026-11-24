#include "desktop_capture.hpp"
#include "video_codec.hpp"
#include <windows.h>
#include <d3d11.h>
#include <wrl/client.h>
#include <chrono>
#include <iostream>
#include <cstring>
#include <string>

using Microsoft::WRL::ComPtr;
namespace {
struct Capture { netgo::video::Frame frame;std::string error; };
void read(void* ctx,const netgo::capture::Surface& s) noexcept {
 auto& out=*static_cast<Capture*>(ctx);try{
  if(s.clockwise_rotation||s.protected_content_masked)throw std::runtime_error("rotated/protected display needs dedicated adaptation");
  D3D11_TEXTURE2D_DESC d{};s.texture->GetDesc(&d);d.BindFlags=0;d.MiscFlags=0;d.Usage=D3D11_USAGE_STAGING;d.CPUAccessFlags=D3D11_CPU_ACCESS_READ;
  ComPtr<ID3D11Texture2D> cpu;if(FAILED(s.device->CreateTexture2D(&d,nullptr,cpu.GetAddressOf())))throw std::runtime_error("capture staging failed");s.context->CopyResource(cpu.Get(),s.texture);
  D3D11_MAPPED_SUBRESOURCE map{};if(FAILED(s.context->Map(cpu.Get(),0,D3D11_MAP_READ,0,&map)))throw std::runtime_error("capture map failed");
  try{auto size=netgo::video::fit_size(s.width,s.height);out.frame=netgo::video::bgra_to_nv12(static_cast<const std::uint8_t*>(map.pData),s.width,s.height,map.RowPitch,size.width,size.height);}catch(...){s.context->Unmap(cpu.Get(),0);throw;}s.context->Unmap(cpu.Get(),0);
 }catch(const std::exception& e){out.error=e.what();}
}
}
int main(int argc,char** argv){
 bool live=argc==2&&std::strcmp(argv[1],"--run")==0;if(!live&&!(argc==2&&std::strcmp(argv[1],"--check")==0)){std::cerr<<"--check synthetic codec | --run real desktop capture, no images saved\n";return 2;}
 try{
  using netgo::video::Codec;netgo::capture::DesktopCapture capture;Capture source;
  if(live){if(capture.open()!=netgo::capture::Result::frame)throw std::runtime_error("interactive desktop unavailable");auto until=std::chrono::steady_clock::now()+std::chrono::seconds(5);while(source.frame.nv12.empty()&&std::chrono::steady_clock::now()<until){capture.next(read,&source,16);if(!source.error.empty())throw std::runtime_error(source.error);}if(source.frame.nv12.empty())throw std::runtime_error("no desktop frame");}
  else{source.frame={1280,720,std::vector<std::uint8_t>(1280*720*3/2,128)};for(size_t i=0;i<1280*720;i++)source.frame.nv12[i]=std::uint8_t(16+(i%220));}
  auto width=source.frame.width,height=source.frame.height;Codec encoder(Codec::Mode::encode,width,height,20,3'000'000),decoder(Codec::Mode::decode,width,height,20);size_t packets=0,frames=0,bytes=0;auto start=std::chrono::steady_clock::now();
  for(int i=0;i<30;i++){for(auto& packet:encoder.encode(source.frame,i*500000ll)){packets++;bytes+=packet.h264.size();for(auto& frame:decoder.decode(packet)){auto bgra=netgo::video::nv12_to_bgra(frame);if(bgra.size()!=size_t(width)*height*4)throw std::runtime_error("invalid decoded frame");frames++;}}}
  if(packets<20||frames<20)throw std::runtime_error("codec produced too few frames");
  std::cout<<"PASS "<<(live?"real DXGI desktop":"synthetic input")<<" -> NV12 -> system H264 encoder -> decoder -> BGRA; width="<<width<<" height="<<height<<" packets="<<packets<<" frames="<<frames<<" encoded_bytes="<<bytes<<" elapsed_ms="<<std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now()-start).count()<<"; software fallback; no transport, audio, input or hardware-performance acceptance\n";
  return 0;
 }catch(const std::exception& e){std::cerr<<"FAIL "<<e.what()<<"\n";return 1;}
}
