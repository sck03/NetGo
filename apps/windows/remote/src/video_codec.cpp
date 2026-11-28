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
void Codec::decode(const std::uint8_t* data,std::size_t length,std::int64_t stamp,const std::function<void(const FrameView&)>& receive){
 auto& p=*impl_;if(p.mode!=Mode::decode)throw std::invalid_argument("decoder mode");
 if(!data||!length||length>max_sample)throw std::invalid_argument("video sample exceeds limit");
 auto s=sample(data,DWORD(length),stamp,10'000'000/p.fps);check(p.transform->ProcessInput(0,s.Get(),0),"decode packet");
 for(auto& v:p.pull()){
  ComPtr<IMFMediaBuffer> buffer;check(v->ConvertToContiguousBuffer(buffer.GetAddressOf()),"contiguous decoder buffer");
  BYTE* raw=nullptr;DWORD size=0;check(buffer->Lock(&raw,nullptr,&size),"decoder buffer lock");
  try {
   size_t plane=size_t(p.decoded_stride)*p.decoded_height;
   if(size<plane*3/2||size>max_sample)throw std::runtime_error("invalid decoder output");
   receive(FrameView{p.width, p.height, p.decoded_stride, {raw, plane}, {raw + plane, plane/2}});
  }catch(...){buffer->Unlock();throw;}
  buffer->Unlock();
 }
}
void Codec::reset(){check(impl_->transform->ProcessMessage(MFT_MESSAGE_COMMAND_FLUSH,0),"flush");check(impl_->transform->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM,0),"restart stream");}

}
