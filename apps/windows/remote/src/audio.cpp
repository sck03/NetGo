#include "audio.hpp"
#include <windows.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <wrl/client.h>
#include <algorithm>
#include <cstring>
#include <stdexcept>
namespace netgo::audio {
namespace {void check(HRESULT hr){if(FAILED(hr))throw std::runtime_error("remote_audio_unavailable");}}
struct Stream::Impl {
 bool recording=false,started=false,com=false;UINT32 capacity=0;
 Microsoft::WRL::ComPtr<IAudioClient> client;
 Microsoft::WRL::ComPtr<IAudioCaptureClient> capture;
 Microsoft::WRL::ComPtr<IAudioRenderClient> render;
 ~Impl(){if(client&&started)client->Stop();render.Reset();capture.Reset();client.Reset();if(com)CoUninitialize();}
 void start(){if(!started){check(client->Start());started=true;}}
};
Stream::Stream(bool capture):impl(std::make_unique<Impl>()){
 auto hr=CoInitializeEx(nullptr,COINIT_MULTITHREADED);if(FAILED(hr)&&hr!=RPC_E_CHANGED_MODE)check(hr);impl->com=SUCCEEDED(hr);impl->recording=capture;
 Microsoft::WRL::ComPtr<IMMDeviceEnumerator> enumerator;check(CoCreateInstance(__uuidof(MMDeviceEnumerator),nullptr,CLSCTX_INPROC_SERVER,__uuidof(IMMDeviceEnumerator),reinterpret_cast<void**>(enumerator.GetAddressOf())));
 Microsoft::WRL::ComPtr<IMMDevice> device;check(enumerator->GetDefaultAudioEndpoint(eRender,eConsole,device.GetAddressOf()));check(device->Activate(__uuidof(IAudioClient),CLSCTX_INPROC_SERVER,nullptr,reinterpret_cast<void**>(impl->client.GetAddressOf())));
 WAVEFORMATEX format{};format.wFormatTag=WAVE_FORMAT_PCM;format.nChannels=2;format.nSamplesPerSec=48000;format.wBitsPerSample=16;format.nBlockAlign=4;format.nAvgBytesPerSec=192000;
 DWORD flags=AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM|AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY|(capture?AUDCLNT_STREAMFLAGS_LOOPBACK:0);
 check(impl->client->Initialize(AUDCLNT_SHAREMODE_SHARED,flags,1000000,0,&format,nullptr));check(impl->client->GetBufferSize(&impl->capacity));
 if(capture)check(impl->client->GetService(__uuidof(IAudioCaptureClient),reinterpret_cast<void**>(impl->capture.GetAddressOf())));
 else check(impl->client->GetService(__uuidof(IAudioRenderClient),reinterpret_cast<void**>(impl->render.GetAddressOf())));
}
Stream::~Stream()=default;
std::vector<std::vector<uint8_t>> Stream::capture(){
 if(!impl->recording)throw std::runtime_error("remote_audio_unavailable");impl->start();std::vector<std::vector<uint8_t>> packets;UINT32 available=0;check(impl->capture->GetNextPacketSize(&available));
 while(available){BYTE* bytes=nullptr;UINT32 frames=0;DWORD flags=0;check(impl->capture->GetBuffer(&bytes,&frames,&flags,nullptr,nullptr));
  try{for(UINT32 offset=0;offset<frames;offset+=960){auto count=std::min<UINT32>(960,frames-offset);std::vector<uint8_t> packet(count*4);if(!(flags&AUDCLNT_BUFFERFLAGS_SILENT))std::memcpy(packet.data(),bytes+offset*4,packet.size());if(packets.size()>=6)packets.erase(packets.begin());packets.push_back(std::move(packet));}}
  catch(...){impl->capture->ReleaseBuffer(frames);throw;}check(impl->capture->ReleaseBuffer(frames));check(impl->capture->GetNextPacketSize(&available));
 }
 return packets;
}
void Stream::play(const uint8_t* bytes,size_t size){
 if(impl->recording||!bytes||size==0||size>3840||size%4)throw std::runtime_error("remote_invalid_audio");UINT32 padding=0;check(impl->client->GetCurrentPadding(&padding));if(padding>impl->capacity)throw std::runtime_error("remote_audio_unavailable");auto frames=UINT32(size/4);if(frames>impl->capacity-padding)return;
 BYTE* buffer=nullptr;check(impl->render->GetBuffer(frames,&buffer));std::memcpy(buffer,bytes,size);check(impl->render->ReleaseBuffer(frames,0));impl->start();
}
}
