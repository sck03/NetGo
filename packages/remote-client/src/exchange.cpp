#include "remote_exchange.hpp"
#include "netgo_remote.h"
#include <stdexcept>
#include <utility>
namespace netgo::remote {
namespace {
void require(bool v, const char *error) {
    if (!v)
        throw std::runtime_error(error);
}
} // namespace
bool Exchange::push(const Json &c) {
    try {
        if (!c.is_object() || c.dump().size() > 65536)
            return false;
        auto action = c.at("action").get<std::string>();
        if (action != "clipboard.get" && action != "clipboard.set" && action != "file.list" &&
            action != "upload" && action != "download" && action != "cancel")
            return false;
        if (action == "clipboard.set" && (c.at("text").get<std::string>().size() > 8192 ||
                                          c.at("text").get<std::string>().find('\0') != std::string::npos))
            return false;
        if (action == "upload" || action == "download") {
            auto p = utf8_path(c.at("path").get<std::string>());
            if (!p.is_absolute() || !valid_file_name(c.at("name")))
                return false;
        }
        std::lock_guard lock(mutex_);
        if (action == "cancel") {
            cancel_ = true;
            while (!queue_.empty()) {
                discarded_.push_back(std::move(queue_.front()));
                queue_.pop_front();
            }
            return true;
        }
        if (queue_.size() + discarded_.size() >= 4)
            return false;
        queue_.push_back(c);
        return true;
    } catch (...) {
        return false;
    }
}
void Exchange::finish(const std::string &error) {
    // Completion allows UI adapters to delete staging files/release scoped URLs.
    // Close native handles before delivering that event.
    local_.reset();
    if (!active_.is_null()) {
        Json e = {
            {"action", active_.value("action", "")}, {"done", true}, {"bytes", offset_}, {"size", size_}};
        if (!error.empty())
            e["error"] = error;
        if (active_.contains("path"))
            e["path"] = active_.at("path");
        event_(e);
    }
    active_ = ready_ = Json();
    pending_.clear();
    localToken_.clear();
    remoteToken_.clear();
    sha_.clear();
    failure_.clear();
    size_ = offset_ = sent_ = 0;
    dataDone_ = false;
}
void Exchange::discard(std::deque<Json> commands, const std::string &error) {
    for (auto &c : commands) {
        Json e = {{"action", c.value("action", "")}, {"done", true}, {"error", error}};
        if (c.contains("path"))
            e["path"] = c.at("path");
        event_(e);
    }
}
void Exchange::reset() noexcept {
    try {
        std::deque<Json> queued, discarded;
        {
            std::lock_guard lock(mutex_);
            queued.swap(queue_);
            discarded.swap(discarded_);
            cancel_ = false;
        }
        discard(std::move(queued), "remote_disconnected");
        discard(std::move(discarded), "remote_transfer_cancelled");
        cancelling_ = false;
        blockedCancel_ = 0;
        finish("remote_disconnected");
    } catch (...) {
        local_.reset();
    }
}
Exchange::Json Exchange::send(Json command) {
    command["id"] = ++id_;
    auto tracked = command;
    if (tracked.contains("data")) {
        tracked["bytes"] = decode64(tracked.at("data"), file_chunk).size();
        tracked.erase("data");
    }
    pending_.push_back({std::move(tracked), std::chrono::steady_clock::now() + std::chrono::seconds(10)});
    return command;
}
Exchange::Json Exchange::next(uint32_t caps, std::chrono::steady_clock::time_point now) {
    bool cancel = false;
    std::deque<Json> discarded;
    {
        std::lock_guard lock(mutex_);
        cancel = std::exchange(cancel_, false);
        discarded.swap(discarded_);
    }
    discard(std::move(discarded), "remote_transfer_cancelled");
    if (blockedCancel_) {
        std::deque<Json> queued;
        { std::lock_guard lock(mutex_); queued.swap(queue_); }
        discard(std::move(queued), "remote_command_timeout");
        return {};
    }
    if (cancel)
        cancelling_ = true;
    if (!active_.is_null()) {
        auto action = active_.value("action", "");
        uint32_t required = action == "upload"                              ? NG_REMOTE_UPLOAD
                            : action == "download" || action == "file.list" ? NG_REMOTE_DOWNLOAD
                            : action == "cancel"                            ? NG_REMOTE_VIEW
                                                                            : NG_REMOTE_CLIPBOARD;
        if ((caps & required) != required)
            cancelling_ = true;
    }
    if (!pending_.empty()) {
        if (now >= pending_.front().deadline) {
            // Responses remain ordered, but the peer may finish an expired request later.
            // Retire its IDs without letting a file timeout tear down media or authorization.
            const bool wasCancel = active_.value("action", "") == "cancel";
            retired_ = id_;
            finish("remote_command_timeout");
            cancelling_ = false;
            if (wasCancel) { blockedCancel_ = retired_; return {}; }
            active_ = {{"action", "cancel"}};
            return send({{"action", "file.cancel"}});
        }
        if (cancelling_ || pending_.size() >= 8) return {};
    }
    if (cancelling_) {
        cancelling_ = false;
        finish(failure_.empty() ? "remote_transfer_cancelled" : failure_);
        active_ = {{"action", "cancel"}};
        return send({{"action", "file.cancel"}});
    }
    if (ready_.is_null() && !active_.is_null() && !remoteToken_.empty() && !dataDone_) {
        if (active_.at("action") == "upload") {
            try { advanceUpload(); }
            catch (const std::exception& error) {
                // Drain outstanding acknowledgements, then cancel only the file.
                // The viewer, input and authorization loop remain alive.
                failure_ = error.what();
                cancelling_ = true;
                return {};
            }
        }
        else if (active_.at("action") == "download") {
            ready_ = {{"action", "file.read"}, {"token", remoteToken_}, {"offset", sent_}};
            sent_ += std::min<std::uint64_t>(file_chunk, size_ - sent_);
            dataDone_ = sent_ == size_;
        }
    }
    if (!ready_.is_null()) {
        auto step = ready_.at("action");
        if (!pending_.empty() && step != "file.read" && step != "file.write") return {};
        auto out = std::move(ready_);
        ready_ = Json();
        return send(std::move(out));
    }
    if (!active_.is_null())
        return {};
    {
        std::lock_guard lock(mutex_);
        if (queue_.empty())
            return {};
        active_ = std::move(queue_.front());
        queue_.pop_front();
    }
    try {
        auto action = active_.at("action").get<std::string>();
        uint32_t required = action == "upload"                              ? NG_REMOTE_UPLOAD
                            : action == "download" || action == "file.list" ? NG_REMOTE_DOWNLOAD
                                                                            : NG_REMOTE_CLIPBOARD;
        require((caps & required) == required, "remote_command_forbidden");
        if (action == "upload") {
            auto p = utf8_path(active_.at("path").get<std::string>());
            local_ = std::make_unique<Files>(p.parent_path(), false);
            auto u = p.filename().u8string();
            auto info = local_->open(std::string(reinterpret_cast<const char *>(u.data()), u.size()));
            size_ = info.at("size");
            localToken_ = info.at("token");
            return send({{"action", "file.begin"}, {"name", active_.at("name")}, {"size", size_}});
        }
        if (action == "download")
            return send({{"action", "file.open"}, {"name", active_.at("name")}});
        return send(active_);
    } catch (const std::exception &e) {
        finish(e.what());
        return {};
    }
}
std::chrono::steady_clock::time_point Exchange::wakeAt() {
    std::lock_guard lock(mutex_);
    if (cancel_ || !discarded_.empty() ||
        (pending_.empty() && ((!queue_.empty() && (active_.is_null() || blockedCancel_)) || cancelling_ || !ready_.is_null())))
        return std::chrono::steady_clock::now();
    return pending_.empty() ? std::chrono::steady_clock::time_point::max() : pending_.front().deadline;
}
void Exchange::advanceUpload() {
    auto chunk = local_->read(localToken_, sent_);
    dataDone_ = chunk.at("done").get<bool>();
    if (dataDone_) sha_ = chunk.at("sha256");
    auto data = chunk.at("data").get<std::string>();
    if (data.empty()) {
        ready_ = {{"action", "file.commit"}, {"token", remoteToken_}, {"sha256", sha_}};
        return;
    }
    ready_ = {{"action", "file.write"}, {"token", remoteToken_}, {"offset", sent_}, {"data", data}};
    sent_ += decode64(data, file_chunk).size();
}
void Exchange::receive(const Json &reply) {
    require(reply.is_object() && reply.contains("id") && reply.at("id").is_number_unsigned(), "remote_command_mismatch");
    if (blockedCancel_ && reply.at("id") == blockedCancel_) {
        if (!reply.contains("error") && reply.contains("result") && reply.at("result").value("done", false)) blockedCancel_ = 0;
        return;
    }
    if (reply.at("id").get<std::uint64_t>() <= retired_) return;
    require(!pending_.empty() && reply.at("id") == pending_.front().request.at("id"), "remote_command_mismatch");
    auto request = std::move(pending_.front().request);
    pending_.pop_front();
    {
        std::lock_guard lock(mutex_);
        if (cancel_) {
            cancelling_ = true;
            cancel_ = false;
        }
    }
    // A response already in flight must not publish a download after cancellation.
    if (cancelling_)
        return;
    if (reply.contains("error")) {
        failure_ = reply.at("error").get<std::string>();
        cancelling_ = true;
        return;
    }
    try {
        const auto &result = reply.at("result");
        auto action = active_.at("action").get<std::string>(), step = request.at("action").get<std::string>();
        if (action == "upload") {
            if (step == "file.begin") {
                remoteToken_ = result.at("token");
            } else if (step == "file.write") {
                require(request.at("offset") == offset_, "remote_file_offset_mismatch");
                offset_ += request.at("bytes").get<std::uint64_t>();
                require(result.at("offset") == offset_, "remote_file_offset_mismatch");
                if (offset_ == size_)
                    ready_ = {{"action", "file.commit"}, {"token", remoteToken_}, {"sha256", sha_}};
            } else {
                require(step == "file.commit" && result.at("done").get<bool>(), "remote_file_incomplete");
                finish();
                return;
            }
        } else if (action == "download") {
            if (step == "file.open") {
                size_ = result.at("size");
                require(size_ <= max_file_bytes, "remote_file_too_large");
                remoteToken_ = result.at("token");
                auto p = utf8_path(active_.at("path").get<std::string>());
                local_ = std::make_unique<Files>(p.parent_path(), false);
                auto u = p.filename().u8string();
                localToken_ =
                    local_->begin(std::string(reinterpret_cast<const char *>(u.data()), u.size()), size_);
            } else {
                require(step == "file.read" && result.at("offset") == offset_, "remote_file_offset_mismatch");
                auto bytes = decode64(result.at("data"), file_chunk);
                require(bytes.size() <= size_ - offset_, "remote_file_too_large");
                if (!bytes.empty())
                    offset_ = local_->write(localToken_, offset_, bytes);
                if (result.at("done").get<bool>()) {
                    require(offset_ == size_, "remote_file_incomplete");
                    local_->commit(localToken_, result.at("sha256"), [this] {
                        std::lock_guard lock(mutex_);
                        return !cancel_ && (!allowed_ || allowed_());
                    });
                    finish();
                    return;
                }
                require(!bytes.empty() && offset_ < size_, "remote_invalid_file_chunk");
            }
        } else {
            auto event = result;
            event["action"] = action;
            event["done"] = true;
            event_(event);
            active_ = Json();
            finish();
            return;
        }
        auto now = std::chrono::steady_clock::now();
        if (now - progress_ >= std::chrono::milliseconds(150)) {
            event_({{"action", action}, {"bytes", offset_}, {"size", size_}, {"done", false}});
            progress_ = now;
        }
    } catch (const std::exception &e) {
        failure_ = e.what();
        cancelling_ = true;
    }
}
} // namespace netgo::remote
