#include "netgo_remote_session_fence.hpp"
#include <stdexcept>
int main(){
 netgo::remote::SessionFence fence;
 fence.bind("old");auto old=fence.snapshot();fence.bind("new");fence.reject(old);
 if(!fence.live())throw std::runtime_error("stale session failure killed new sharing");
 fence.reject(fence.snapshot());if(fence.live())throw std::runtime_error("revoked session still live");
 fence.bind({});fence.bind("next");if(!fence.live())throw std::runtime_error("host cannot accept next session");
}
