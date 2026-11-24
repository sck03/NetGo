#include "video_codec.hpp"
#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mferror.h>
#include <mftransform.h>
#include <wrl/client.h>
#include <algorithm>
#include <cstring>
#include <cstdio>
#include <stdexcept>

using Microsoft::WRL::ComPtr;
namespace netgo::video {
namespace {
constexpr DWORD max_sample=16*1024*1024;
void check(HRESULT hr,const char* operation){if(FAILED(hr)){char b[160];std::snprintf(b,sizeof(b),"%s HRESULT=0x%08lx",operation,static_cast<unsigned long>(hr));throw std::runtime_error(b);}}
void dimensions(std::uint32_t w,std::uint32_t h){if(w<16||h<16||w>4096||h>2160||(w&1)||(h&1))throw std::invalid_argument("unsupported video dimensions");}
ComPtr<IMFMediaType> type(GUID format,std::uint32_t w,std::uint32_t h,std::uint32_t fps){ComPtr<IMFMediaType> t;check(MFCreateMediaType(t.GetAddressOf()),"media type");check(t->SetGUID(MF_MT_MAJOR_TYPE,MFMediaType_Video),"major type");check(t->SetGUID(MF_MT_SUBTYPE,format),"subtype");check(t->SetUINT64(MF_MT_FRAME_SIZE,(UINT64(w)<<32)|h),"frame size");check(t->SetUINT64(MF_MT_FRAME_RATE,(UINT64(fps)<<32)|1),"frame rate");check(t->SetUINT64(MF_MT_PIXEL_ASPECT_RATIO,(UINT64(1)<<32)|1),"pixel aspect");check(t->SetUINT32(MF_MT_INTERLACE_MODE,MFVideoInterlace_Progressive),"interlace");return t;}
ComPtr<IMFSample> sample(const std::uint8_t* data,DWORD size,LONGLONG timestamp,LONGLONG duration){
 if(!size||size>max_sample)throw std::invalid_argument("video sample exceeds limit");
 ComPtr<IMFSample>s;ComPtr<IMFMediaBuffer>b;check(MFCreateSample(s.GetAddressOf()),"sample");check(MFCreateMemoryBuffer(size,b.GetAddressOf()),"sample buffer");
 if(data){BYTE* dest=nullptr;check(b->Lock(&dest,nullptr,nullptr),"sample lock");std::memcpy(dest,data,size);b->Unlock();check(b->SetCurrentLength(size),"sample size");}
 check(s->AddBuffer(b.Get()),"sample attach");check(s->SetSampleTime(timestamp),"sample time");check(s->SetSampleDuration(duration),"sample duration");return s;
}
std::vector<std::uint8_t> bytes(IMFSample* s){ComPtr<IMFMediaBuffer>b;check(s->ConvertToContiguousBuffer(b.GetAddressOf()),"contiguous buffer");BYTE* p=nullptr;DWORD n=0;check(b->Lock(&p,nullptr,&n),"output lock");if(n>max_sample){b->Unlock();throw std::runtime_error("oversize codec output");}std::vector<std::uint8_t> out;try{out.assign(p,p+n);}catch(...){b->Unlock();throw;}b->Unlock();return out;}
}
struct Codec::Impl {
 Mode mode;std::uint32_t width,height,fps;std::uint32_t decoded_stride{},decoded_height{};ComPtr<IMFTransform> transform;bool com=false,mf=false;
 Impl(Mode m,std::uint32_t w,std::uint32_t h,std::uint32_t f,std::uint32_t bitrate):mode(m),width(w),height(h),fps(f){
  dimensions(w,h);decoded_stride=w;decoded_height=h;if(f<1||f>60||bitrate<100'000||bitrate>40'000'000)throw std::invalid_argument("invalid video limits");
  HRESULT co=CoInitializeEx(nullptr,COINIT_MULTITHREADED);if(FAILED(co))check(co,"codec worker COM");com=true;
  try {
   check(MFStartup(MF_VERSION,MFSTARTUP_FULL),"Media Foundation startup");mf=true;
   MFT_REGISTER_TYPE_INFO in{MFMediaType_Video,m==Mode::encode?MFVideoFormat_NV12:MFVideoFormat_H264};
   MFT_REGISTER_TYPE_INFO out{MFMediaType_Video,m==Mode::encode?MFVideoFormat_H264:MFVideoFormat_NV12};
   IMFActivate** list=nullptr;UINT32 count=0;check(MFTEnumEx(m==Mode::encode?MFT_CATEGORY_VIDEO_ENCODER:MFT_CATEGORY_VIDEO_DECODER,MFT_ENUM_FLAG_SYNCMFT|MFT_ENUM_FLAG_SORTANDFILTER,&in,&out,&list,&count),"codec discovery");
   HRESULT activated=E_FAIL;for(UINT32 i=0;i<count;i++){if(!transform)activated=list[i]->ActivateObject(IID_PPV_ARGS(transform.GetAddressOf()));list[i]->Release();}CoTaskMemFree(list);if(!transform)check(activated,"system H.264 codec unavailable");
   ComPtr<IMFAttributes> attrs;if(SUCCEEDED(transform->GetAttributes(attrs.GetAddressOf())))attrs->SetUINT32(MF_LOW_LATENCY,TRUE);
   auto compressed=type(MFVideoFormat_H264,w,h,f);check(compressed->SetUINT32(MF_MT_AVG_BITRATE,bitrate),"bitrate");compressed->SetUINT32(MF_MT_MPEG2_PROFILE,66);
   auto raw=type(MFVideoFormat_NV12,w,h,f);raw->SetUINT32(MF_MT_DEFAULT_STRIDE,w);raw->SetUINT32(MF_MT_SAMPLE_SIZE,w*h*3/2);raw->SetUINT32(MF_MT_FIXED_SIZE_SAMPLES,TRUE);
   if(m==Mode::encode){check(transform->SetOutputType(0,compressed.Get(),0),"encoder output");check(transform->SetInputType(0,raw.Get(),0),"encoder input");}
   else{check(transform->SetInputType(0,compressed.Get(),0),"decoder input");check(transform->SetOutputType(0,raw.Get(),0),"decoder output");}
   check(transform->ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING,0),"begin streaming");check(transform->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM,0),"start stream");
  }catch(...){transform.Reset();if(mf)MFShutdown();if(com)CoUninitialize();throw;}
 }
 ~Impl(){if(transform){transform->ProcessMessage(MFT_MESSAGE_COMMAND_FLUSH,0);transform->ProcessMessage(MFT_MESSAGE_NOTIFY_END_STREAMING,0);transform.Reset();}if(mf)MFShutdown();if(com)CoUninitialize();}
 std::vector<ComPtr<IMFSample>> pull(){
  std::vector<ComPtr<IMFSample>> out;
  for(int attempt=0;attempt<8;attempt++){
   MFT_OUTPUT_STREAM_INFO info{};check(transform->GetOutputStreamInfo(0,&info),"output info");MFT_OUTPUT_DATA_BUFFER data{};ComPtr<IMFSample> buffer;
   if(!(info.dwFlags&MFT_OUTPUT_STREAM_PROVIDES_SAMPLES)){DWORD size=std::max<DWORD>(info.cbSize,width*height*3/2);buffer=sample(nullptr,size,0,10'000'000/fps);data.pSample=buffer.Get();}
   DWORD status=0;HRESULT result=transform->ProcessOutput(0,1,&data,&status);if(data.pEvents)data.pEvents->Release();
   if(result==MF_E_TRANSFORM_NEED_MORE_INPUT)break;
   if(result==MF_E_TRANSFORM_STREAM_CHANGE){
    if(mode!=Mode::decode)check(result,"encoder format change");
    bool selected=false;for(DWORD i=0;i<64;i++){
     ComPtr<IMFMediaType> candidate;if(FAILED(transform->GetOutputAvailableType(0,i,candidate.GetAddressOf())))break;
     GUID subtype{};candidate->GetGUID(MF_MT_SUBTYPE,&subtype);if(subtype!=MFVideoFormat_NV12)continue;
     UINT64 size=0;check(candidate->GetUINT64(MF_MT_FRAME_SIZE,&size),"decoded size");auto cw=UINT32(size>>32),ch=UINT32(size);
     // H.264 pads the coded frame to macroblocks; crop the advertised visible
     // frame, never interpret padded rows as pixels from another frame.
     if(cw<width||ch<height||cw>width+15||ch>height+15)throw std::runtime_error("unexpected decoder dimensions");
     UINT32 stride=cw;candidate->GetUINT32(MF_MT_DEFAULT_STRIDE,&stride);if(stride<cw||stride>16384)throw std::runtime_error("unsupported decoder stride");
     decoded_stride=stride;decoded_height=ch;check(transform->SetOutputType(0,candidate.Get(),0),"decoder change");selected=true;break;
    }if(!selected)throw std::runtime_error("NV12 decoder output unavailable");continue;
   }
   check(result,"codec output");if(!data.pSample)throw std::runtime_error("empty codec sample");if(data.pSample!=buffer.Get())buffer.Attach(data.pSample);out.push_back(std::move(buffer));
  }return out;
 }
};
Codec::Codec(Mode m,std::uint32_t w,std::uint32_t h,std::uint32_t f,std::uint32_t b):impl_(std::make_unique<Impl>(m,w,h,f,b)){}
Codec::~Codec()=default;
std::vector<Packet> Codec::encode(const Frame& f,std::int64_t timestamp){auto& p=*impl_;if(p.mode!=Mode::encode||f.width!=p.width||f.height!=p.height||f.nv12.size()!=size_t(p.width)*p.height*3/2)throw std::invalid_argument("encoder frame mismatch");auto s=sample(f.nv12.data(),DWORD(f.nv12.size()),timestamp,10'000'000/p.fps);check(p.transform->ProcessInput(0,s.Get(),0),"encode frame");std::vector<Packet> out;for(auto& v:p.pull()){LONGLONG time=0;v->GetSampleTime(&time);UINT32 key=0;v->GetUINT32(MFSampleExtension_CleanPoint,&key);out.push_back({time,key!=0,bytes(v.Get())});}return out;}
std::vector<Frame> Codec::decode(const Packet& packet){
 auto& p=*impl_;if(p.mode!=Mode::decode)throw std::invalid_argument("decoder mode");
 auto s=sample(packet.h264.data(),DWORD(packet.h264.size()),packet.timestamp,10'000'000/p.fps);check(p.transform->ProcessInput(0,s.Get(),0),"decode packet");std::vector<Frame> out;
 for(auto& v:p.pull()){
  auto raw=bytes(v.Get());size_t plane=size_t(p.decoded_stride)*p.decoded_height;
  if(raw.size()<plane*3/2)throw std::runtime_error("short decoder output");
  Frame frame{p.width,p.height,std::vector<std::uint8_t>(size_t(p.width)*p.height*3/2)};
  for(UINT32 y=0;y<p.height;y++)std::memcpy(frame.nv12.data()+size_t(y)*p.width,raw.data()+size_t(y)*p.decoded_stride,p.width);
  for(UINT32 y=0;y<p.height/2;y++)std::memcpy(frame.nv12.data()+size_t(p.width)*p.height+size_t(y)*p.width,raw.data()+plane+size_t(y)*p.decoded_stride,p.width);
  out.push_back(std::move(frame));
 }return out;
}
void Codec::reset(){check(impl_->transform->ProcessMessage(MFT_MESSAGE_COMMAND_FLUSH,0),"flush");check(impl_->transform->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM,0),"restart stream");}

Size fit_size(std::uint32_t w,std::uint32_t h,std::uint32_t maxw,std::uint32_t maxh){
 dimensions(maxw,maxh);if(w<16||h<16||w>16384||h>16384)throw std::invalid_argument("invalid display dimensions");
 auto ow=std::min(w,maxw),oh=std::min(h,maxh);
 if(std::uint64_t(ow)*h>std::uint64_t(oh)*w)ow=std::uint32_t(std::uint64_t(oh)*w/h);
 else oh=std::uint32_t(std::uint64_t(ow)*h/w);
 ow&=~1u;oh&=~1u;dimensions(ow,oh);return {ow,oh};
}

Frame bgra_to_nv12(const std::uint8_t* data,std::uint32_t w,std::uint32_t h,std::uint32_t stride,std::uint32_t ow,std::uint32_t oh){
 dimensions(ow,oh);if(!data||w<ow||h<oh||w>16384||h>16384||stride<w*4)throw std::invalid_argument("BGRA input bounds");Frame out{ow,oh,std::vector<std::uint8_t>(size_t(ow)*oh*3/2)};auto clip=[](int v){return std::uint8_t(std::clamp(v,0,255));};
 for(std::uint32_t y=0;y<oh;y++)for(std::uint32_t x=0;x<ow;x++){const auto* p=data+size_t(y*h/oh)*stride+size_t(x*w/ow)*4;int b=p[0],g=p[1],r=p[2];out.nv12[size_t(y)*ow+x]=clip(((66*r+129*g+25*b+128)>>8)+16);if(!(y&1)&&!(x&1)){size_t uv=size_t(ow)*oh+size_t(y/2)*ow+x;out.nv12[uv]=clip(((-38*r-74*g+112*b+128)>>8)+128);out.nv12[uv+1]=clip(((112*r-94*g-18*b+128)>>8)+128);}}
 return out;
}
std::vector<std::uint8_t> nv12_to_bgra(const Frame& f){dimensions(f.width,f.height);if(f.nv12.size()!=size_t(f.width)*f.height*3/2)throw std::invalid_argument("NV12 input bounds");std::vector<std::uint8_t> out(size_t(f.width)*f.height*4);auto clip=[](int v){return std::uint8_t(std::clamp(v,0,255));};for(std::uint32_t y=0;y<f.height;y++)for(std::uint32_t x=0;x<f.width;x++){int c=f.nv12[size_t(y)*f.width+x]-16;size_t uv=size_t(f.width)*f.height+size_t(y/2)*f.width+(x&~1u);int d=f.nv12[uv]-128,e=f.nv12[uv+1]-128;auto* p=out.data()+(size_t(y)*f.width+x)*4;p[0]=clip((298*c+516*d+128)>>8);p[1]=clip((298*c-100*d-208*e+128)>>8);p[2]=clip((298*c+409*e+128)>>8);p[3]=255;}return out;}
}
