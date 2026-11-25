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
    active_ = pending_ = ready_ = chunk_ = Json();
    localToken_.clear();
    remoteToken_.clear();
    sha_.clear();
    size_ = offset_ = 0;
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
        finish("remote_disconnected");
    } catch (...) {
        local_.reset();
    }
}
Exchange::Json Exchange::send(Json command) {
    command["id"] = ++id_;
    pending_ = command;
    deadline_ = std::chrono::steady_clock::now() + std::chrono::seconds(10);
    return command;
}
Exchange::Json Exchange::next(uint32_t caps) {
    bool cancel = false;
    std::deque<Json> discarded;
    {
        std::lock_guard lock(mutex_);
        cancel = std::exchange(cancel_, false);
        discarded.swap(discarded_);
    }
    discard(std::move(discarded), "remote_transfer_cancelled");
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
    if (!pending_.is_null()) {
        require(std::chrono::steady_clock::now() < deadline_, "remote_command_timeout");
        return {};
    }
    if (cancelling_) {
        cancelling_ = false;
        finish("remote_transfer_cancelled");
        active_ = {{"action", "cancel"}};
        return send({{"action", "file.cancel"}});
    }
    if (!ready_.is_null()) {
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
void Exchange::advanceUpload() {
    chunk_ = local_->read(localToken_, offset_);
    if (chunk_.at("done").get<bool>())
        sha_ = chunk_.at("sha256");
    auto data = chunk_.at("data").get<std::string>();
    if (data.empty()) {
        ready_ = {{"action", "file.commit"}, {"token", remoteToken_}, {"sha256", sha_}};
        return;
    }
    ready_ = {{"action", "file.write"}, {"token", remoteToken_}, {"offset", offset_}, {"data", data}};
}
void Exchange::receive(const Json &reply) {
    require(!pending_.is_null() && reply.at("id") == pending_.at("id"), "remote_command_mismatch");
    auto request = std::move(pending_);
    pending_ = Json();
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
        finish(reply.at("error").get<std::string>());
        return;
    }
    try {
        const auto &result = reply.at("result");
        auto action = active_.at("action").get<std::string>(), step = request.at("action").get<std::string>();
        if (action == "upload") {
            if (step == "file.begin") {
                remoteToken_ = result.at("token");
                advanceUpload();
            } else if (step == "file.write") {
                auto data = decode64(chunk_.at("data"), file_chunk);
                offset_ += data.size();
                require(result.at("offset") == offset_, "remote_file_offset_mismatch");
                if (offset_ == size_)
                    ready_ = {{"action", "file.commit"}, {"token", remoteToken_}, {"sha256", sha_}};
                else
                    advanceUpload();
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
                    local_->commit(localToken_, result.at("sha256"));
                    finish();
                    return;
                }
                require(!bytes.empty() && offset_ < size_, "remote_invalid_file_chunk");
            }
            ready_ = {{"action", "file.read"}, {"token", remoteToken_}, {"offset", offset_}};
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
        finish(e.what());
        active_ = {{"action", "cancel"}};
        ready_ = {{"action", "file.cancel"}};
    }
}
} // namespace netgo::remote
