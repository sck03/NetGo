#pragma once
#include <cstdint>
#include <mutex>
#include <string>
#include <utility>

namespace netgo::remote {
// Host registration and individual sessions have independent lifetimes.
class SessionFence {
 mutable std::mutex mutex_;
 std::string id_;
 uint64_t epoch_=0;
 bool authorized_=false;
public:
 using Snapshot=std::pair<uint64_t,std::string>;
 void bind(std::string id){std::lock_guard lock(mutex_);++epoch_;id_=std::move(id);authorized_=!id_.empty();}
 Snapshot snapshot()const{std::lock_guard lock(mutex_);return {epoch_,id_};}
 void reject(const Snapshot& value){std::lock_guard lock(mutex_);if(value.first==epoch_&&value.second==id_)authorized_=false;}
 bool live()const{std::lock_guard lock(mutex_);return authorized_;}
};
}
