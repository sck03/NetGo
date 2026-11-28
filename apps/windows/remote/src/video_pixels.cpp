#include "video_codec.hpp"
#include <algorithm>
#include <stdexcept>

namespace netgo::video {
namespace {
void dimensions(std::uint32_t w, std::uint32_t h) {
 if (w < 16 || h < 16 || w > 4096 || h > 2160 || (w & 1) || (h & 1))
  throw std::invalid_argument("unsupported video dimensions");
}
}
Size fit_size(std::uint32_t w,std::uint32_t h,std::uint32_t maxw,std::uint32_t maxh){
 dimensions(maxw,maxh);if(w<16||h<16||w>16384||h>16384)throw std::invalid_argument("invalid display dimensions");
 auto ow=std::min(w,maxw),oh=std::min(h,maxh);
 if(std::uint64_t(ow)*h>std::uint64_t(oh)*w)ow=std::uint32_t(std::uint64_t(oh)*w/h);
 else oh=std::uint32_t(std::uint64_t(ow)*h/w);
 ow&=~1u;oh&=~1u;dimensions(ow,oh);return {ow,oh};
}

void bgra_to_nv12(const std::uint8_t* data,std::uint32_t w,std::uint32_t h,std::uint32_t stride,std::uint32_t ow,std::uint32_t oh,Frame& out){
 // The negotiated canvas is fixed for the session. Match the GPU stretch and
 // normalized input mapping when a source mode becomes smaller or changes aspect.
 dimensions(ow,oh);if(!data||w<16||h<16||w>16384||h>16384||stride<w*4)throw std::invalid_argument("BGRA input bounds");out.width=ow;out.height=oh;out.nv12.resize(size_t(ow)*oh*3/2);auto clip=[](int v){return std::uint8_t(std::clamp(v,0,255));};
 for(std::uint32_t y=0;y<oh;y++)for(std::uint32_t x=0;x<ow;x++){const auto* p=data+size_t(y*h/oh)*stride+size_t(x*w/ow)*4;int b=p[0],g=p[1],r=p[2];out.nv12[size_t(y)*ow+x]=clip(((66*r+129*g+25*b+128)>>8)+16);if(!(y&1)&&!(x&1)){size_t uv=size_t(ow)*oh+size_t(y/2)*ow+x;out.nv12[uv]=clip(((-38*r-74*g+112*b+128)>>8)+128);out.nv12[uv+1]=clip(((112*r-94*g-18*b+128)>>8)+128);}}
}
Frame bgra_to_nv12(const std::uint8_t* data,std::uint32_t w,std::uint32_t h,std::uint32_t stride,std::uint32_t ow,std::uint32_t oh){Frame out;bgra_to_nv12(data,w,h,stride,ow,oh,out);return out;}

void nv12_to_bgra(const FrameView& f, std::vector<std::uint8_t>& out) {
 dimensions(f.width, f.height);
 if (f.stride < f.width || f.stride > 16384 || f.y.size() < size_t(f.stride) * f.height ||
     f.uv.size() < size_t(f.stride) * (f.height / 2)) throw std::invalid_argument("NV12 input bounds");
 out.resize(size_t(f.width) * f.height * 4);
 auto clip = [](int v) { return std::uint8_t(std::clamp(v >> 8, 0, 255)); };
 // NV12 shares one chroma sample across four pixels. Compute its contribution
 // once per block, retaining the exact integer BT.601 rounding of the encoder.
 for (std::uint32_t y = 0; y < f.height; y += 2) {
  const auto* uv = f.uv.data() + size_t(y / 2) * f.stride;
  for (std::uint32_t x = 0; x < f.width; x += 2) {
   const int d = uv[x] - 128, e = uv[x + 1] - 128;
   const int blue = 516 * d + 128, green = -100 * d - 208 * e + 128, red = 409 * e + 128;
   for (unsigned row = 0; row < 2; ++row) {
    const auto* luma = f.y.data() + size_t(y + row) * f.stride + x;
    auto* pixel = out.data() + (size_t(y + row) * f.width + x) * 4;
    for (unsigned col = 0; col < 2; ++col, pixel += 4) {
     const int c = 298 * (luma[col] - 16);
     pixel[0] = clip(c + blue); pixel[1] = clip(c + green); pixel[2] = clip(c + red); pixel[3] = 255;
    }
   }
  }
 }
}
void nv12_to_bgra(const Frame& f, std::vector<std::uint8_t>& out) {
 dimensions(f.width, f.height);
 const size_t plane = size_t(f.width) * f.height;
 if (f.nv12.size() != plane * 3 / 2) throw std::invalid_argument("NV12 input bounds");
 nv12_to_bgra(FrameView{f.width, f.height, f.width, {f.nv12.data(), plane}, {f.nv12.data() + plane, plane / 2}}, out);
}
}
