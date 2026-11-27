#include "connection.hpp"
#include <algorithm>
#include <charconv>
#include <cctype>
#include <sstream>
#include <string_view>

namespace netgo {
namespace {
std::string lower(std::string s) {
    for (char& c : s) c = char(std::tolower(static_cast<unsigned char>(c)));
    return s;
}
size_t number(std::string_view text, int base, const char* error) {
    size_t value = 0;
    auto result = std::from_chars(text.data(), text.data() + text.size(), value, base);
    if (text.empty() || result.ec != std::errc() || result.ptr != text.data() + text.size())
        throw Error(error);
    return value;
}
}
void validateHealth(const Json& h,const std::string& siteID) {
 if(h.value("site_id","")!=siteID)throw Error("site_identity_mismatch");
 if(h.value("protocol",0)!=2||!h.contains("capabilities")||!h["capabilities"].is_array())throw Error("protocol_incompatible");
 for(const auto& c:{"native-auth-v1","device-signature-v1","leases-v1","profile-revision-v1"}){
  if(std::find(h["capabilities"].begin(),h["capabilities"].end(),Json(c))==h["capabilities"].end())throw Error("protocol_incompatible");
 }
 if(!h.contains("server_unix")||!h["server_unix"].is_number_integer())throw Error("invalid_control_health");
 auto remote=h["server_unix"].get<int64_t>();
 if(remote<946684800||remote>4102444800)throw Error("invalid_control_health");
}
void HttpResponseReader::append(const char* bytes, size_t length) {
    if (length > maxResponseJson + 32768 - raw_.size()) throw Error("http_response_too_large");
    raw_.append(bytes, length);
    advance();
}
void HttpResponseReader::advance() {
    for (;;) {
        switch (stage_) {
        case Stage::header: {
            auto end = raw_.find("\r\n\r\n", cursor_);
            if (end == std::string::npos) {
                if (raw_.size() > 32768 + 3) throw Error("invalid_http_response");
                cursor_ = raw_.size() > 3 ? raw_.size() - 3 : 0;
                return;
            }
            if (end > 32768) throw Error("invalid_http_response");
            std::istringstream lines(raw_.substr(0, end));
            std::string version, line;
            lines >> version >> status_;
            std::getline(lines, line);
            if ((version != "HTTP/1.1" && version != "HTTP/1.0") || status_ < 100 || status_ > 599)
                throw Error("invalid_http_response");
            reusable_ = version == "HTTP/1.1";
            size_t length = SIZE_MAX;
            while (std::getline(lines, line)) {
                auto colon = line.find(':');
                if (colon == std::string::npos || !colon) throw Error("invalid_http_header");
                auto key = lower(line.substr(0, colon)), value = lower(line.substr(colon + 1));
                auto first = value.find_first_not_of(" \t\r"), last = value.find_last_not_of(" \t\r");
                value = first == std::string::npos ? "" : value.substr(first, last - first + 1);
                if (key == "connection" && value.find("close") != std::string::npos) reusable_ = false;
                if (key == "transfer-encoding") {
                    if (chunked_ || value != "chunked") throw Error("unsupported_http_encoding");
                    chunked_ = true;
                }
                if (key == "content-encoding" && value != "identity") throw Error("unsupported_http_encoding");
                if (key == "content-length") {
                    if (length != SIZE_MAX) throw Error("invalid_http_length");
                    length = number(value, 10, "invalid_http_length");
                    if (length > maxResponseJson) throw Error("http_response_too_large");
                }
            }
            if (chunked_ && length != SIZE_MAX) throw Error("invalid_http_framing");
            if (status_ == 204 || status_ == 304) {
                if (chunked_ || (status_ == 204 && length != SIZE_MAX && length != 0))
                    throw Error("invalid_http_framing");
                length = 0;
            }
            cursor_ = bodyStart_ = end + 4;
            if (chunked_) stage_ = Stage::chunkSize;
            else if (length != SIZE_MAX) { remaining_ = length; stage_ = Stage::fixed; }
            else { stage_ = Stage::eof; reusable_ = false; }
            break;
        }
        case Stage::fixed:
            if (raw_.size() - bodyStart_ > remaining_) throw Error("invalid_http_length");
            if (raw_.size() - bodyStart_ < remaining_) return;
            cursor_ = raw_.size();
            stage_ = Stage::complete;
            break;
        case Stage::eof:
            if (raw_.size() - bodyStart_ > maxResponseJson) throw Error("http_response_too_large");
            return;
        case Stage::chunkSize: {
            const auto end = raw_.find("\r\n", cursor_);
            if (end == std::string::npos) {
                if (raw_.size() - cursor_ > 129) throw Error("invalid_http_chunk");
                return;
            }
            if (end - cursor_ > 128) throw Error("invalid_http_chunk");
            auto text = std::string_view(raw_).substr(cursor_, end - cursor_);
            text = text.substr(0, text.find(';'));
            remaining_ = number(text, 16, "invalid_http_chunk");
            if (remaining_ > maxResponseJson - decoded_.size()) throw Error("http_response_too_large");
            cursor_ = end + 2;
            stage_ = remaining_ ? Stage::chunkBody : Stage::trailers;
            trailerStart_ = cursor_;
            break;
        }
        case Stage::chunkBody:
            if (raw_.size() - cursor_ < remaining_ + 2) return;
            if (raw_.compare(cursor_ + remaining_, 2, "\r\n") != 0) throw Error("invalid_http_chunk");
            decoded_.append(raw_, cursor_, remaining_);
            cursor_ += remaining_ + 2;
            stage_ = Stage::chunkSize;
            break;
        case Stage::trailers: {
            const auto end = raw_.find("\r\n", cursor_);
            if (end == std::string::npos) {
                if (raw_.size() - trailerStart_ > 32768) throw Error("invalid_http_chunk");
                return;
            }
            if (end - trailerStart_ > 32768) throw Error("invalid_http_chunk");
            if (end == cursor_) { cursor_ += 2; stage_ = Stage::complete; break; }
            auto colon = raw_.find(':', cursor_);
            if (colon == std::string::npos || colon == cursor_ || colon >= end) throw Error("invalid_http_chunk");
            auto name = lower(raw_.substr(cursor_, colon - cursor_));
            if (name == "content-length" || name == "transfer-encoding") throw Error("invalid_http_framing");
            cursor_ = end + 2;
            break;
        }
        case Stage::complete:
            if (cursor_ != raw_.size()) throw Error("invalid_http_framing");
            return;
        }
    }
}
HttpResponse HttpResponseReader::finish(bool closed) {
    if (!complete() && !(closed && stage_ == Stage::eof)) throw Error("invalid_http_response");
    if (chunked_) return {status_, std::move(decoded_)};
    raw_.erase(0, bodyStart_);
    return {status_, std::move(raw_)};
}
HttpResponse decodeHttp(const std::string& raw) {
    HttpResponseReader reader;
    reader.append(raw.data(), raw.size());
    return reader.finish(true);
}
}
