#pragma once
#include <algorithm>
#include <cstdint>
#include <optional>

namespace netgo::desktop::layout {
struct Rect {int left{},top{},right{},bottom{};};
struct Point {std::uint16_t x{},y{};};
inline Rect fit(Rect area,std::uint32_t width,std::uint32_t height){
 auto w=std::max(0,area.right-area.left),h=std::max(0,area.bottom-area.top);
 if(!width||!height||!w||!h)return {area.left,area.top,area.left,area.top};
 if(std::uint64_t(w)*height>std::uint64_t(h)*width)w=int(std::uint64_t(h)*width/height);
 else h=int(std::uint64_t(w)*height/width);
 int x=area.left+(area.right-area.left-w)/2,y=area.top+(area.bottom-area.top-h)/2;
 return {x,y,x+w,y+h};
}
inline std::optional<Point> map(Rect image,int x,int y,bool dragging=false){
 auto w=image.right-image.left,h=image.bottom-image.top;if(w<2||h<2)return {};
 if(!dragging&&(x<image.left||x>=image.right||y<image.top||y>=image.bottom))return {};
 auto px=std::clamp(x-image.left,0,w-1),py=std::clamp(y-image.top,0,h-1);
 return Point{std::uint16_t(std::int64_t(px)*65535/(w-1)),std::uint16_t(std::int64_t(py)*65535/(h-1))};
}
}
