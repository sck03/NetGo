#include "netgo_remote_scroll.hpp"
#include "host.hpp"
#include <gio/gio.h>
#include <gio/gunixfdlist.h>
#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/extensions/XTest.h>
#include <unistd.h>
#include <chrono>
#include <set>
#include <stdexcept>
#include <thread>
#include <cmath>
#include <fstream>
#include <cstring>
#include <sys/stat.h>
namespace netgo::desktop_linux {
namespace {
void require(bool v, const char *e) {
    if (!v)
        throw std::runtime_error(e);
}
struct Variant {
    GVariant *value = nullptr;
    ~Variant() {
        if (value)
            g_variant_unref(value);
    }
    Variant(const Variant &) = delete;
    Variant(GVariant *v) : value(v) {}
};
GVariant *options() {
    GVariantBuilder b;
    g_variant_builder_init(&b, G_VARIANT_TYPE_VARDICT);
    return g_variant_builder_end(&b);
}
KeySym key(unsigned code) {
    if (code >= 0x41 && code <= 0x5a)
        return XK_a + code - 0x41;
    if (code >= 0x30 && code <= 0x39)
        return XK_0 + code - 0x30;
    if (code >= 0x70 && code <= 0x87)
        return XK_F1 + code - 0x70;
    switch (code) {
    case 8:
        return XK_BackSpace;
    case 9:
        return XK_Tab;
    case 13:
        return XK_Return;
    case 16:
    case 160:
    case 161:
        return XK_Shift_L;
    case 17:
    case 162:
    case 163:
        return XK_Control_L;
    case 18:
    case 164:
    case 165:
        return XK_Alt_L;
    case 27:
        return XK_Escape;
    case 32:
        return XK_space;
    case 33:
        return XK_Page_Up;
    case 34:
        return XK_Page_Down;
    case 35:
        return XK_End;
    case 36:
        return XK_Home;
    case 37:
        return XK_Left;
    case 38:
        return XK_Up;
    case 39:
        return XK_Right;
    case 40:
        return XK_Down;
    case 45:
        return XK_Insert;
    case 46:
        return XK_Delete;
    case 91:
        return XK_Super_L;
    case 186:
        return XK_semicolon;
    case 187:
        return XK_equal;
    case 188:
        return XK_comma;
    case 189:
        return XK_minus;
    case 190:
        return XK_period;
    case 191:
        return XK_slash;
    case 192:
        return XK_grave;
    case 219:
        return XK_bracketleft;
    case 220:
        return XK_backslash;
    case 221:
        return XK_bracketright;
    case 222:
        return XK_apostrophe;
    default:
        return NoSymbol;
    }
}
struct PortalReply {
    bool done = false;
    uint32_t response = 2;
    GVariant *values = nullptr;
};
void response(GDBusConnection *, const gchar *, const gchar *, const gchar *, const gchar *, GVariant *args,
              gpointer user) {
    auto &r = *static_cast<PortalReply *>(user);
    g_variant_get(args, "(u@a{sv})", &r.response, &r.values);
    r.done = true;
}
} // namespace
struct HostPlatform::Impl {
    std::function<bool()> active;
    std::filesystem::path restorePath;
    bool persistent = false;
    bool wayland = false;
    Display *display = nullptr;
    int screen = 0;
    netgo::remote::WheelAccumulator wheel;
    uint32_t width = 0, height = 0, sourceWidth = 0, sourceHeight = 0, node = 0;
    GDBusConnection *bus = nullptr;
    std::string session;
    int fd = -1;
    GstElement *pipeline = nullptr, *sink = nullptr;
    std::set<KeySym> keys;
    std::set<unsigned> buttons;
    uint32_t caps = 0;
    Impl() {
        gst_init(nullptr, nullptr);
        wayland = g_getenv("WAYLAND_DISPLAY") != nullptr;
        if (wayland) {
            bus = g_bus_get_sync(G_BUS_TYPE_SESSION, nullptr, nullptr);
            require(bus, "remote_portal_unavailable");
        } else {
            display = XOpenDisplay(nullptr);
            require(display, "remote_x11_unavailable");
            screen = DefaultScreen(display);
            sourceWidth = uint32_t(DisplayWidth(display, screen));
            sourceHeight = uint32_t(DisplayHeight(display, screen));
        }
    }
    ~Impl() {
        close();
        closePortal();
        if (display)
            XCloseDisplay(display);
        if (bus)
            g_object_unref(bus);
    }
    GVariant *request(const char *method, GVariant *args, const std::string &token,
                      const char *interface = "org.freedesktop.portal.RemoteDesktop") {
        auto unique = std::string(g_dbus_connection_get_unique_name(bus));
        for (char &c : unique)
            if (c == '.')
                c = '_';
        if (!unique.empty() && unique.front() == ':')
            unique.erase(0, 1);
        auto path = "/org/freedesktop/portal/desktop/request/" + unique + "/" + token;
        PortalReply reply;
        auto context = g_main_context_new();
        g_main_context_push_thread_default(context);
        auto subscription = g_dbus_connection_signal_subscribe(
            bus, "org.freedesktop.portal.Desktop", "org.freedesktop.portal.Request", "Response", path.c_str(),
            nullptr, G_DBUS_SIGNAL_FLAGS_NONE, response, &reply, nullptr);
        GError *error = nullptr;
        auto result = g_dbus_connection_call_sync(bus, "org.freedesktop.portal.Desktop",
                                                  "/org/freedesktop/portal/desktop", interface, method, args,
                                                  nullptr, G_DBUS_CALL_FLAGS_NONE, 3000, nullptr, &error);
        if (result)
            g_variant_unref(result);
        auto end = std::chrono::steady_clock::now() + std::chrono::seconds(30);
        while (result && !reply.done && (!active || active()) && std::chrono::steady_clock::now() < end) {
            while (g_main_context_iteration(context, FALSE)) {
            }
            std::this_thread::sleep_for(std::chrono::milliseconds(10));
        }
        if (!reply.done)
            g_dbus_connection_call(bus, "org.freedesktop.portal.Desktop", path.c_str(),
                                   "org.freedesktop.portal.Request", "Close", nullptr, nullptr,
                                   G_DBUS_CALL_FLAGS_NONE, 1000, nullptr, nullptr, nullptr);
        g_dbus_connection_signal_unsubscribe(bus, subscription);
        g_main_context_pop_thread_default(context);
        g_main_context_unref(context);
        if (error)
            g_error_free(error);
        if (!reply.done || reply.response) {
            if (reply.values)
                g_variant_unref(reply.values);
            throw std::runtime_error("remote_portal_permission_denied");
        }
        return reply.values;
    }
    void portal() {
        auto token = std::string("netgo") + std::to_string(g_get_monotonic_time());
        GVariantBuilder b;
        g_variant_builder_init(&b, G_VARIANT_TYPE_VARDICT);
        g_variant_builder_add(&b, "{sv}", "handle_token", g_variant_new_string(token.c_str()));
        g_variant_builder_add(&b, "{sv}", "session_handle_token", g_variant_new_string(token.c_str()));
        Variant created(
            request("CreateSession", g_variant_new("(@a{sv})", g_variant_builder_end(&b)), token));
        const char *s = nullptr;
        require(g_variant_lookup(created.value, "session_handle", "&s", &s), "remote_portal_session_failed");
        session = s;
        token += 'd';
        g_variant_builder_init(&b, G_VARIANT_TYPE_VARDICT);
        g_variant_builder_add(&b, "{sv}", "handle_token", g_variant_new_string(token.c_str()));
        g_variant_builder_add(&b, "{sv}", "types", g_variant_new_uint32((caps & 2) ? 3 : 0));
        if (persistent) {
            g_variant_builder_add(&b, "{sv}", "persist_mode", g_variant_new_uint32(2));
            std::ifstream input(restorePath);
            std::string restore;
            std::getline(input, restore);
            if (!restore.empty() && restore.size() <= 4096)
                g_variant_builder_add(&b, "{sv}", "restore_token", g_variant_new_string(restore.c_str()));
        }
        Variant selected(request(
            "SelectDevices", g_variant_new("(o@a{sv})", session.c_str(), g_variant_builder_end(&b)), token));
        // ScreenCast selection shares the RemoteDesktop session and displays the
        // compositor's own screen/permission picker. It never bypasses that picker.
        token += 's';
        g_variant_builder_init(&b, G_VARIANT_TYPE_VARDICT);
        g_variant_builder_add(&b, "{sv}", "handle_token", g_variant_new_string(token.c_str()));
        g_variant_builder_add(&b, "{sv}", "types", g_variant_new_uint32(1));
        g_variant_builder_add(&b, "{sv}", "multiple", g_variant_new_boolean(FALSE));
        g_variant_builder_add(&b, "{sv}", "cursor_mode", g_variant_new_uint32(2));
        auto args = g_variant_new("(o@a{sv})", session.c_str(), g_variant_builder_end(&b));
        // SelectSources is on ScreenCast; Start is on RemoteDesktop.
        Variant sources(request("SelectSources", args, token, "org.freedesktop.portal.ScreenCast"));
        token += 'a';
        g_variant_builder_init(&b, G_VARIANT_TYPE_VARDICT);
        g_variant_builder_add(&b, "{sv}", "handle_token", g_variant_new_string(token.c_str()));
        Variant started(request(
            "Start", g_variant_new("(os@a{sv})", session.c_str(), "", g_variant_builder_end(&b)), token));
        uint32_t devices = 0;
        if (!g_variant_lookup(started.value, "devices", "u", &devices) || (devices & 3) != 3)
            caps &= ~2u;
        Variant streams(g_variant_lookup_value(started.value, "streams", G_VARIANT_TYPE("a(ua{sv})")));
        require(streams.value && g_variant_n_children(streams.value) > 0, "remote_portal_no_screen");
        Variant first(g_variant_get_child_value(streams.value, 0));
        GVariant *properties = nullptr;
        g_variant_get(first.value, "(u@a{sv})", &node, &properties);
        Variant props(properties);
        int w = 0, h = 0;
        require(g_variant_lookup(properties, "size", "(ii)", &w, &h) && w > 0 && h > 0,
                "remote_portal_screen_size");
        sourceWidth = uint32_t(w);
        sourceHeight = uint32_t(h);
        const char *restore = nullptr;
        if (persistent && g_variant_lookup(started.value, "restore_token", "&s", &restore) && restore &&
            std::strlen(restore) <= 4096) {
            GError *error = nullptr;
            require(g_file_set_contents_full(restorePath.c_str(), restore, -1, G_FILE_SET_CONTENTS_CONSISTENT,
                                             0600, &error),
                    "remote_portal_persistence_failed");
            if (error)
                g_error_free(error);
        }
        GUnixFDList *descriptors = nullptr;
        Variant remote(g_dbus_connection_call_with_unix_fd_list_sync(
            bus, "org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop",
            "org.freedesktop.portal.ScreenCast", "OpenPipeWireRemote",
            g_variant_new("(o@a{sv})", session.c_str(), options()), G_VARIANT_TYPE("(h)"),
            G_DBUS_CALL_FLAGS_NONE, 3000, nullptr, &descriptors, nullptr, nullptr));
        require(remote.value && descriptors, "remote_pipewire_unavailable");
        int handle = -1;
        g_variant_get(remote.value, "(h)", &handle);
        fd = g_unix_fd_list_get(descriptors, handle, nullptr);
        g_object_unref(descriptors);
        require(fd >= 0, "remote_pipewire_unavailable");
    }
    void notify(const char *method, GVariant *parameters) {
        auto result = g_dbus_connection_call_sync(bus, "org.freedesktop.portal.Desktop",
                                                  "/org/freedesktop/portal/desktop",
                                                  "org.freedesktop.portal.RemoteDesktop", method, parameters,
                                                  nullptr, G_DBUS_CALL_FLAGS_NONE, 100, nullptr, nullptr);
        require(result, "remote_input_unavailable");
        g_variant_unref(result);
    }
    void injectKey(KeySym k, bool down) {
        require(k != NoSymbol, "remote_key_unavailable");
        if (wayland)
            notify("NotifyKeyboardKeysym",
                   g_variant_new("(o@a{sv}iu)", session.c_str(), options(), int(k), down ? 1u : 0u));
        else {
            auto code = XKeysymToKeycode(display, k);
            require(code && XTestFakeKeyEvent(display, code, down, CurrentTime), "remote_key_unavailable");
            XFlush(display);
        }
        if (down)
            keys.insert(k);
        else
            keys.erase(k);
    }
    void button(unsigned b, bool down) {
        require(b >= 1 && b <= 3, "remote_invalid_button");
        auto linuxButton = b == 1 ? 272 : b == 2 ? 273 : 274;
        if (wayland)
            notify("NotifyPointerButton",
                   g_variant_new("(o@a{sv}iu)", session.c_str(), options(), linuxButton, down ? 1u : 0u));
        else {
            XTestFakeButtonEvent(display, b == 2 ? 3 : b == 3 ? 2 : 1, down, CurrentTime);
            XFlush(display);
        }
        if (down)
            buttons.insert(b);
        else
            buttons.erase(b);
    }
    void release() noexcept {
        wheel.reset();
        try {
            auto held = keys;
            for (auto k : held)
                injectKey(k, false);
            auto pressed = buttons;
            for (auto b : pressed)
                button(b, false);
        } catch (...) {
        }
        keys.clear();
        buttons.clear();
    }
    void close() noexcept {
        release();
        if (pipeline) {
            gst_element_set_state(pipeline, GST_STATE_NULL);
            gst_object_unref(pipeline);
            pipeline = nullptr;
        }
        if (sink) {
            gst_object_unref(sink);
            sink = nullptr;
        }
    }
    void closePortal() noexcept {
        if (fd >= 0) {
            ::close(fd);
            fd = -1;
        }
        if (!session.empty() && bus) {
            g_dbus_connection_call(bus, "org.freedesktop.portal.Desktop", session.c_str(),
                                   "org.freedesktop.portal.Session", "Close", nullptr, nullptr,
                                   G_DBUS_CALL_FLAGS_NONE, 1000, nullptr, nullptr, nullptr);
            session.clear();
        }
    }
    void open(uint32_t c) {
        close();
        caps = c;
        require(!wayland || !session.empty(), "remote_portal_unavailable");
        auto scale = std::min(1.0, std::min(1280.0 / sourceWidth, 720.0 / sourceHeight));
        width = uint32_t(sourceWidth * scale) & ~1u;
        height = uint32_t(sourceHeight * scale) & ~1u;
        require(width >= 16 && height >= 16, "remote_invalid_display");
        std::string source = wayland ? "pipewiresrc fd=" + std::to_string(fd) +
                                           " path=" + std::to_string(node) + " do-timestamp=true"
                                     : "ximagesrc use-damage=false show-pointer=true";
        std::vector<std::string> codecs = {
            "vapostproc ! video/x-raw(memory:VAMemory),format=NV12,width=" + std::to_string(width) +
                ",height=" + std::to_string(height) + " ! vah264enc bitrate=4000 key-int-max=20",
            "videoconvert ! videoscale ! video/x-raw,format=NV12,width=" + std::to_string(width) +
                ",height=" + std::to_string(height) +
                " ! nvh264enc bitrate=4000 gop-size=20 zerolatency=true",
            "videoconvert ! videoscale ! video/x-raw,format=I420,width=" + std::to_string(width) +
                ",height=" + std::to_string(height) +
                " ! x264enc tune=zerolatency speed-preset=ultrafast bitrate=4000 key-int-max=20"};
        for (const auto &codec : codecs) {
            GError *error = nullptr;
            auto text =
                source +
                " ! videorate ! video/x-raw,framerate=20/1 ! queue max-size-buffers=2 max-size-bytes=0 "
                "max-size-time=0 leaky=downstream ! " +
                codec +
                " ! h264parse config-interval=-1 ! video/x-h264,stream-format=byte-stream,alignment=au ! "
                "appsink name=encoded sync=false max-buffers=4 drop=false";
            pipeline = gst_parse_launch(text.c_str(), &error);
            if (error) {
                g_error_free(error);
                if (pipeline)
                    gst_object_unref(pipeline);
                pipeline = nullptr;
                continue;
            }
            sink = gst_bin_get_by_name(GST_BIN(pipeline), "encoded");
            if (gst_element_set_state(pipeline, GST_STATE_PLAYING) != GST_STATE_CHANGE_FAILURE &&
                gst_element_get_state(pipeline, nullptr, nullptr, 3 * GST_SECOND) != GST_STATE_CHANGE_FAILURE)
                return;
            gst_element_set_state(pipeline, GST_STATE_NULL);
            gst_object_unref(sink);
            sink = nullptr;
            gst_object_unref(pipeline);
            pipeline = nullptr;
        }
        throw std::runtime_error("remote_linux_encoder_unavailable");
    }
};
HostPlatform::HostPlatform() : impl_(std::make_unique<Impl>()) {}
HostPlatform::~HostPlatform() = default;
uint32_t HostPlatform::prepare(uint32_t caps, const std::filesystem::path &path, bool persistent,
                               std::function<bool()> active) {
    impl_->caps = caps;
    impl_->restorePath = path;
    impl_->persistent = persistent;
    impl_->active = std::move(active);
    if (!persistent) {
        std::error_code ignored;
        std::filesystem::remove(path, ignored);
    }
    if (impl_->wayland)
        impl_->portal();
    return impl_->caps;
}
void HostPlatform::open(uint32_t caps, uint32_t &w, uint32_t &h, uint32_t &fps) {
    impl_->open(caps);
    w = impl_->width;
    h = impl_->height;
    fps = 20;
}
bool HostPlatform::poll(uint8_t *out, size_t cap, size_t &length) {
    length = 0;
    auto &p = *impl_;
    auto bus = gst_element_get_bus(p.pipeline);
    auto error = gst_bus_pop_filtered(bus, GstMessageType(GST_MESSAGE_ERROR | GST_MESSAGE_EOS));
    gst_object_unref(bus);
    if (error) {
        gst_message_unref(error);
        return false;
    }
    auto sample = gst_app_sink_try_pull_sample(GST_APP_SINK(p.sink), 0);
    if (!sample)
        return true;
    auto buffer = gst_sample_get_buffer(sample);
    auto size = gst_buffer_get_size(buffer);
    if (size > cap) {
        gst_sample_unref(sample);
        return false;
    }
    length = gst_buffer_extract(buffer, 0, out, size);
    gst_sample_unref(sample);
    return length == size;
}
void HostPlatform::input(const Json &e) {
    auto &p = *impl_;
    auto type = e.at("type").get<std::string>();
    if (type == "release") {
        p.release();
        return;
    }
    if (type == "key") {
        p.injectKey(key(e.at("code")), e.at("down"));
        return;
    }
    if (type == "button") {
        p.button(e.at("button"), e.at("down"));
        return;
    }
    if (type == "move") {
        double x = e.at("x").get<unsigned>() * double(p.sourceWidth - 1) / 65535,
               y = e.at("y").get<unsigned>() * double(p.sourceHeight - 1) / 65535;
        if (p.wayland)
            p.notify("NotifyPointerMotionAbsolute",
                     g_variant_new("(o@a{sv}udd)", p.session.c_str(), options(), p.node, x, y));
        else {
            XTestFakeMotionEvent(p.display, p.screen, int(x), int(y), CurrentTime);
            XFlush(p.display);
        }
        return;
    }
    if (type == "wheel") {
        auto delta = e.at("delta").get<int>();
        bool horizontal = e.value("horizontal", false);
        if (p.wayland)
            p.notify("NotifyPointerAxis",
                     g_variant_new("(o@a{sv}dd)", p.session.c_str(), options(),
                                   horizontal ? -delta / 120.0 : 0.0, horizontal ? 0.0 : -delta / 120.0));
        else {
            const int steps = p.wheel.consume(delta, horizontal);
            unsigned button = horizontal ? (steps > 0 ? 6 : 7) : (steps > 0 ? 4 : 5);
            for (int i = 0; i < std::abs(steps); ++i) {
                XTestFakeButtonEvent(p.display, button, True, CurrentTime);
                XTestFakeButtonEvent(p.display, button, False, CurrentTime);
            }
            XFlush(p.display);
        }
        return;
    }
    if (type == "text") {
        auto text = e.at("text").get<std::string>();
        require(g_utf8_validate(text.data(), gssize(text.size()), nullptr), "remote_invalid_text");
        for (const char *v = text.c_str(); *v; v = g_utf8_next_char(v)) {
            auto u = g_utf8_get_char(v);
            KeySym k = u < 0x100 ? u : (0x01000000 | u);
            p.injectKey(k, true);
            p.injectKey(k, false);
        }
        return;
    }
    throw std::runtime_error("remote_invalid_input");
}
void HostPlatform::close() noexcept { impl_->close(); }
} // namespace netgo::desktop_linux
