#include "audio.hpp"
#include <gst/app/gstappsrc.h>
#include <stdexcept>

namespace netgo::desktop_linux {
struct AudioPlayer::Impl {
    GstElement* pipeline = nullptr;
    GstAppSrc* source = nullptr;
    GstBus* bus = nullptr;
    ~Impl() {
        if (pipeline) gst_element_set_state(pipeline, GST_STATE_NULL);
        if (bus) gst_object_unref(bus);
        if (source) gst_object_unref(source);
        if (pipeline) gst_object_unref(pipeline);
    }
};
AudioPlayer::AudioPlayer() : impl(std::make_unique<Impl>()) {
    gst_init(nullptr, nullptr);
    GError* error = nullptr;
    impl->pipeline = gst_parse_launch(
        "appsrc name=audio is-live=true format=time do-timestamp=true block=false max-bytes=19200 "
        "caps=audio/x-raw,format=S16LE,layout=interleaved,rate=48000,channels=2 "
        "! audioconvert ! audioresample ! autoaudiosink sync=false", &error);
    if (error) { g_error_free(error); throw std::runtime_error("audio_unavailable"); }
    if (!impl->pipeline) throw std::runtime_error("audio_unavailable");
    impl->source = GST_APP_SRC(gst_bin_get_by_name(GST_BIN(impl->pipeline), "audio"));
    impl->bus = gst_element_get_bus(impl->pipeline);
    if (!impl->source || !impl->bus || gst_element_set_state(impl->pipeline, GST_STATE_PLAYING) == GST_STATE_CHANGE_FAILURE)
        throw std::runtime_error("audio_unavailable");
}
AudioPlayer::~AudioPlayer() = default;
bool AudioPlayer::push(const uint8_t* bytes, size_t size) {
    if (!bytes || size < 4 || size > 3840 || size % 4) return false;
    if (auto error = gst_bus_pop_filtered(impl->bus, GST_MESSAGE_ERROR)) { gst_message_unref(error); return false; }
    if (gst_app_src_get_current_level_bytes(impl->source) + size > 19200) return false;
    auto buffer = gst_buffer_new_allocate(nullptr, size, nullptr);
    if (!buffer) return false;
    gst_buffer_fill(buffer, 0, bytes, size);
    GST_BUFFER_DURATION(buffer) = gst_util_uint64_scale(size / 4, GST_SECOND, 48000);
    return gst_app_src_push_buffer(impl->source, buffer) == GST_FLOW_OK;
}
}
