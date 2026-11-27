#include "frame_pool.hpp"
#include <cstdlib>

int main() {
    netgo::desktop_linux::FramePool pool;
    auto first=pool.acquire(64), second=pool.acquire(64), third=pool.acquire(64);
    if(!first||!second||!third||pool.acquire(64))std::abort();
    (*first)[0]=42;
    auto displayed=first;
    auto* allocation=first->data();
    first.reset();
    if(pool.acquire(64)||(*displayed)[0]!=42)std::abort();
    displayed.reset();
    auto reused=pool.acquire(64);
    if(!reused||reused->data()!=allocation)std::abort();
    // A texture outlives the decoder/pool that produced it.
    std::shared_ptr<const std::vector<uint8_t>> retained;
    {netgo::desktop_linux::FramePool other;auto frame=other.acquire(128);(*frame)[0]=7;retained=frame;}
    if(retained->at(0)!=7)std::abort();
}
