#include "client.hpp"
extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/imgutils.h>
#include <libswscale/swscale.h>
}
#include <cstring>
#include <stdexcept>
namespace netgo::desktop_linux {
struct Decoder::Impl {
 AVCodecContext* codec=nullptr;AVFrame* frame=nullptr;AVPacket* packet=nullptr;SwsContext* scale=nullptr;
 ~Impl(){sws_freeContext(scale);av_packet_free(&packet);av_frame_free(&frame);avcodec_free_context(&codec);}
};
Decoder::Decoder():impl(std::make_unique<Impl>()){
 auto decoder=avcodec_find_decoder(AV_CODEC_ID_H264);if(!decoder)throw std::runtime_error("h264_unavailable");
 impl->codec=avcodec_alloc_context3(decoder);impl->frame=av_frame_alloc();impl->packet=av_packet_alloc();if(!impl->codec||!impl->frame||!impl->packet)throw std::runtime_error("decoder_failed");
 impl->codec->thread_count=2;impl->codec->max_pixels=4096*2160;impl->codec->flags|=AV_CODEC_FLAG_LOW_DELAY;if(avcodec_open2(impl->codec,decoder,nullptr)<0)throw std::runtime_error("decoder_failed");
}
Decoder::~Decoder()=default;
void Decoder::decode(const uint8_t* bytes,size_t size,const std::function<void(Frame)>& present){
 if(!size||size>4194304)throw std::runtime_error("invalid_video_packet");
 av_packet_unref(impl->packet);if(av_new_packet(impl->packet,int(size))<0)throw std::runtime_error("decoder_failed");std::memcpy(impl->packet->data,bytes,size);
 if(avcodec_send_packet(impl->codec,impl->packet)<0)throw std::runtime_error("decoder_failed");
 int result;
 while((result=avcodec_receive_frame(impl->codec,impl->frame))>=0){auto& f=*impl->frame;if(f.width<2||f.width>4096||f.height<2||f.height>2160)throw std::runtime_error("unsupported_video_size");
  Frame output{uint32_t(f.width),uint32_t(f.height),std::vector<uint8_t>(size_t(f.width)*size_t(f.height)*4)};
  impl->scale=sws_getCachedContext(impl->scale,f.width,f.height,AVPixelFormat(f.format),f.width,f.height,AV_PIX_FMT_BGRA,SWS_FAST_BILINEAR,nullptr,nullptr,nullptr);if(!impl->scale)throw std::runtime_error("decoder_failed");
  uint8_t* destination[]={output.bgra.data(),nullptr,nullptr,nullptr};int strides[]={f.width*4,0,0,0};if(sws_scale(impl->scale,f.data,f.linesize,0,f.height,destination,strides)!=f.height)throw std::runtime_error("decoder_failed");present(std::move(output));av_frame_unref(impl->frame);
 }
 if(result!=AVERROR(EAGAIN)&&result!=AVERROR_EOF)throw std::runtime_error("decoder_failed");
}
}
