#include "remote_bridge.hpp"
#include "audio.hpp"
#include "remote_inventory.hpp"
#include "client.hpp"
#include "frame_presenter.hpp"
#include "operation_epoch.hpp"
#include "startup.hpp"
#include "service.hpp"
#include "remote_actions.hpp"
#include "netgo_remote_client.h"
#include "netgo_remote.h"
#include <gtk/gtk.h>
#include <curl/curl.h>
#include <filesystem>
#include <fstream>
#include <thread>
#include <mutex>
#include "remote_input.hpp"
#include "input_mapping.hpp"
#include "remote_dialogs.hpp"
using netgo::desktop_linux::virtualKey;
using netgo::desktop_linux::extendedKey;
#include <algorithm>
#include <csignal>
#include <cstring>
#include <sys/stat.h>
#include <unistd.h>
#include "host.hpp"
#include "netgo_remote_host.h"
#include "remote_files.hpp"
#include <future>
#include <X11/Xlib.h>

using netgo::desktop_linux::Frame;
using netgo::desktop_linux::Json;
namespace {
struct State {
    GtkApplication *app = nullptr;
    GtkWidget *window = nullptr, *profiles = nullptr, *alias = nullptr, *password = nullptr,
              *notice = nullptr, *hosts = nullptr, *picture = nullptr, *connect = nullptr,
              *remoteButton = nullptr;
    GtkIMContext *ime = nullptr;
    netgo::desktop_linux::ServiceSupervisor service;
    GtkWidget *internet = nullptr;
    GtkWidget *replace = nullptr, *viewOnly = nullptr, *audio = nullptr;
    GtkWidget *resources = nullptr;
    std::string root, socket, site;
    Json profileList = Json::array(), hostList = Json::array();
    std::atomic_bool alive = true, remoteActive = false, jobBusy = false, renderPending = false,
                     remoteStopping = false;
    std::atomic_uint64_t remoteVersion = 0;
    std::atomic_bool forgetUnattended = false;
    std::thread job, remote, cancelJob;
    netgo::desktop_linux::OperationEpoch epoch;
    std::atomic_bool cancelling = false;
    std::mutex mutex;
    std::shared_ptr<const Frame> frame;
    netgo::remote::InputQueue inputs;
    uint32_t caps = 0;
    netgo::desktop_linux::RemoteActions remoteActions;
    std::unique_ptr<netgo::desktop_linux::Decoder> decoder;
    ng_remote_client *client = nullptr;
    ng_remote_host *host = nullptr;
    GtkWidget *unattended = nullptr, *files = nullptr;
    Json remoteFiles = Json::array();
    bool smoke = false, loadingProfiles = false;
    bool restoreChecked = false, autoResume = false, suspended = false;
    int64_t nextRestore = 0, nextShare = 0;
    unsigned timer = 0;
    ~State() {
        alive = false;
        remoteActive = false;
        if (job.joinable())
            job.join();
        if (remote.joinable())
            remote.join();
        if (cancelJob.joinable())
            cancelJob.join();
        service.stop();
        if (ime)
            g_object_unref(ime);
    }
};
State *state = nullptr;
template <class F> GCallback callback(F function) { return reinterpret_cast<GCallback>(function); }
void names(GtkWidget *dropdown, const Json &list, const char *field) {
    auto strings = gtk_string_list_new(nullptr);
    for (const auto &item : list)
        gtk_string_list_append(strings, item.at(field).get<std::string>().c_str());
    gtk_drop_down_set_model(GTK_DROP_DOWN(dropdown), G_LIST_MODEL(strings));
    g_object_unref(strings);
    if (!list.empty())
        gtk_drop_down_set_selected(GTK_DROP_DOWN(dropdown), 0);
}
std::string selected(GtkWidget *dropdown, const Json &list, const char *field) {
    auto i = gtk_drop_down_get_selected(GTK_DROP_DOWN(dropdown));
    return i < list.size() ? list[i].at(field).get<std::string>() : std::string();
}
struct MainAction {
    std::function<void()> action;
};
void mainThread(std::function<void()> action) {
    g_main_context_invoke(
        nullptr,
        [](void *p) -> gboolean {
            std::unique_ptr<MainAction> a(static_cast<MainAction *>(p));
            a->action();
            return G_SOURCE_REMOVE;
        },
        new MainAction{std::move(action)});
}
void status(std::string text) {
    mainThread([text = std::move(text)] {
        if (state->alive)
            gtk_label_set_text(GTK_LABEL(state->notice), text.c_str());
    });
}
Json call(const std::string &method, const std::string &path, const Json &body = nullptr) {
    return netgo::desktop_linux::ipc(state->socket, method, path, body, state->alive);
}
void waitForService() {
    for (int i = 0; i < 100 && state->alive; ++i) {
        try {
            if (state->service.ensure(state->root, state->socket)) {
                call("GET", "/desktop/status");
                return;
            }
        } catch (...) {}
        g_usleep(100000);
    }
    throw std::runtime_error("连接服务暂未就绪，正在自动恢复，请稍后重试");
}
void prepareNetwork() {
    if (netgo::desktop_linux::ensureNetwork()) state->service.restart();
    waitForService();
}
void task(std::function<void()> action) {
    if (state->cancelling || state->jobBusy.exchange(true)) {
        status("当前操作尚未完成，可随时断开连接");
        return;
    }
    if (state->job.joinable())
        state->job.join();
    state->job = std::thread([action = std::move(action)] {
        try {
            action();
        } catch (const std::exception &e) {
            status(e.what());
        }
        if (state->forgetUnattended.exchange(false)) {
            try {
                call("DELETE", "/desktop/unattended");
            } catch (const std::exception &e) {
                state->forgetUnattended = true;
                status(e.what());
            }
        }
        state->jobBusy = false;
    });
}
void disconnectNetwork() {
    state->suspended = true;
    state->epoch.advance();
    state->hostList = Json::array();
    names(state->hosts, state->hostList, "name");
    if (state->cancelling.exchange(true))
        return;
    if (state->cancelJob.joinable())
        state->cancelJob.join();
    state->cancelJob = std::thread([] {
        try {
            call("POST", "/desktop/disconnect");
            status("已断开");
        } catch (const std::exception &e) {
            status(e.what());
        }
        state->cancelling = false;
    });
}
void queue(Json input) {
    if (!state->remoteActive)
        return;
    std::lock_guard lock(state->mutex);
    if (!(state->caps & NG_REMOTE_INPUT))
        return;
    if (!state->inputs.push(std::move(input))) status("输入队列繁忙或内容超限，请重试");
}
void present(Frame frame) {
    {
        std::lock_guard lock(state->mutex);
        state->frame = std::make_shared<const Frame>(std::move(frame));
    }
    if (state->renderPending.exchange(true))
        return;
    mainThread([] {
        state->renderPending = false;
        if (!state->alive)
            return;
        std::shared_ptr<const Frame> frame;
        {
            std::lock_guard lock(state->mutex);
            frame = state->frame;
        }
        netgo::desktop_linux::presentFrame(GTK_PICTURE(state->picture), std::move(frame));
    });
}
bool point(double x, double y, bool clamp) {
    uint32_t w, h;
    {
        std::lock_guard lock(state->mutex);
        if (!state->frame) return false;
        w = state->frame->width;
        h = state->frame->height;
    }
    if (!w || !h)
        return false;
    double vw = gtk_widget_get_width(state->picture), vh = gtk_widget_get_height(state->picture),
           scale = std::min(vw / w, vh / h);
    if (scale <= 0)
        return false;
    auto left = (vw - w * scale) / 2, top = (vh - h * scale) / 2;
    if (!clamp && (x < left || y < top || x >= left + w * scale || y >= top + h * scale))
        return false;
    queue({{"type", "move"},
           {"x", uint32_t(std::clamp((x - left) / (w * scale), 0.0, 1.0) * 65535)},
           {"y", uint32_t(std::clamp((y - top) / (h * scale), 0.0, 1.0) * 65535)}});
    return true;
}
void stopRemote() {
    ++state->remoteVersion;
    if (state->alive) state->remoteActions.enable(0);
    if (state->remoteActive)
        state->remoteStopping = true;
    state->remoteActive = false;
    std::lock_guard lock(state->mutex);
    if (state->client)
        ng_remote_client_cancel(state->client);
    if (state->host)
        ng_remote_host_cancel(state->host);
    state->inputs.clear();
}
void view(std::string host) {
    state->suspended = true;
    if (state->remoteStopping || state->remoteActive.exchange(true))
        return;
    if (state->remote.joinable())
        state->remote.join();
    gtk_widget_set_sensitive(state->remoteButton, FALSE);
    auto site = state->site;
    auto version = ++state->remoteVersion;
    const bool viewOnly = gtk_check_button_get_active(GTK_CHECK_BUTTON(state->viewOnly));
    const bool audio = gtk_check_button_get_active(GTK_CHECK_BUTTON(state->audio));
    state->remote = std::thread([host = std::move(host), site, version, viewOnly, audio] {
        struct ViewerContext {
            std::string site;
            uint64_t version;
            std::unique_ptr<netgo::desktop_linux::AudioPlayer> audio;
        };
        ViewerContext bridge{site, version, {}};
        ng_remote_client_options o{};
        o.context = &bridge;
        o.struct_size = sizeof(o);
        o.abi_version = NG_REMOTE_CLIENT_ABI;
        o.site_id = site.c_str();
        o.host_id = host.c_str();
        o.capabilities = NG_REMOTE_VIEW | NG_REMOTE_CLIPBOARD | NG_REMOTE_UPLOAD | NG_REMOTE_DOWNLOAD |
            (viewOnly ? 0 : NG_REMOTE_INPUT) | (audio ? NG_REMOTE_AUDIO : 0);
        o.active = [](void *) -> int32_t { return state->alive && state->remoteActive ? 1 : 0; };
        o.control = [](void* p, const char* method, const char* path, const char* body, const char* key,
                       char* output, size_t capacity, size_t* length) -> int32_t {
            return netgo::desktop_linux::remoteControl(state->socket, static_cast<ViewerContext *>(p)->site,
                                                      state->remoteActive, method, path, body, key, output, capacity, length);
        };
        o.sign = [](void *p, const uint8_t *message, size_t size, uint8_t output[64]) -> int32_t {
            try {
                auto result = netgo::desktop_linux::ipc(state->socket,
                    "POST", "/desktop/remote/sign",
                    {{"Site", static_cast<ViewerContext *>(p)->site}, {"Message", netgo::remote::encode64(message, size)}}, state->remoteActive, 4000L);
                auto bytes = netgo::remote::decode64(result.at("signature"), 64);
                if (bytes.size() != 64) throw std::runtime_error("invalid_signature");
                std::copy(bytes.begin(), bytes.end(), output);
                return 0;
            } catch (...) {
                return -1;
            }
        };
        o.input = [](void *, char *output, size_t capacity, size_t *length) -> int32_t {
            try {
                std::lock_guard lock(state->mutex);
                auto text = state->inputs.take().dump();
                if (text.size() > capacity)
                    return -1;
                std::copy(text.begin(), text.end(), output);
                *length = text.size();
                return 0;
            } catch (...) {
                return -1;
            }
        };
        o.format = [](void *p, uint32_t w, uint32_t h, uint32_t fps, uint32_t caps) -> int32_t {
            try {
                if (w > 4096 || h > 2160 || fps > 60)
                    return -1;
                state->decoder = std::make_unique<netgo::desktop_linux::Decoder>();
                auto& playback = static_cast<ViewerContext *>(p)->audio;
                playback.reset();
                if (caps & NG_REMOTE_AUDIO) playback = std::make_unique<netgo::desktop_linux::AudioPlayer>();
                std::lock_guard lock(state->mutex);
                state->caps = caps;
                auto version = static_cast<ViewerContext *>(p)->version;
                mainThread([version, caps] {
                    if (state->alive && state->remoteVersion == version && state->remoteActive)
                        state->remoteActions.enable(caps);
                });
                return 0;
            } catch (...) {
                return -1;
            }
        };
        o.video = [](void *, const uint8_t *bytes, size_t size, int64_t) -> int32_t {
            try {
                state->decoder->decode(bytes, size, present);
                return 0;
            } catch (...) {
                return -1;
            }
        };
        o.audio = [](void *p, const uint8_t* bytes, size_t size) -> int32_t {
            auto& playback = static_cast<ViewerContext *>(p)->audio;
            return playback && playback->push(bytes, size) ? 0 : -1;
        };
        o.event = [](void *p, const char *text) {
            try {
                auto e = Json::parse(text);
                auto version = static_cast<ViewerContext *>(p)->version;
                mainThread([e, version] {
                    if (!state->alive || state->remoteVersion != version)
                        return;
                    if (e.contains("error")) {
                        status("操作失败：" + e.at("error").get<std::string>());
                        return;
                    }
                    if (e.value("action", "") == "clipboard.get") {
                        auto text = e.at("text").get<std::string>();
                        gdk_clipboard_set_text(
                            gdk_display_get_clipboard(gtk_widget_get_display(state->window)), text.c_str());
                        status("已复制远端剪贴板");
                    } else if (e.contains("files")) {
                        state->remoteFiles = e.at("files");
                        names(state->files, state->remoteFiles, "name");
                    } else if (e.contains("bytes"))
                        status("传输字节：" + e.at("bytes").dump() + " / " + e.at("size").dump());
                });
            } catch (...) {
            }
        };
        o.status = [](void *, const char *code) {
            status(std::string(code) == "remote_connected"          ? "远程桌面已连接"
                   : std::string(code) == "remote_waiting_consent"  ? "等待主机确认…"
                   : std::string(code) == "remote_host_busy"        ? "对方正在使用远程桌面，请稍后刷新"
                   : std::string(code) == "remote_host_unavailable" ? "对方暂不可连接，请检查 VPN 和桌面共享"
                   : std::string(code) == "remote_view_forbidden"   ? "当前账号没有观看权限"
                                                                    : code);
        };
        ng_remote_client *client = nullptr;
        if (ng_remote_client_create(&o, &client) == 0) {
            {
                std::lock_guard lock(state->mutex);
                state->client = client;
                if (!state->remoteActive)
                    ng_remote_client_cancel(client);
            }
            ng_remote_client_run(client);
            {
                std::lock_guard lock(state->mutex);
                state->client = nullptr;
            }
            ng_remote_client_destroy(client);
        } else
            status("remote_client_failed");
        state->decoder.reset();
        state->remoteActive = false;
        state->remoteStopping = false;
        {
            std::lock_guard lock(state->mutex);
            state->caps = 0;
            state->inputs.clear();
            state->frame = {};
        }
        mainThread([] {
            if (state->alive) {
                gtk_picture_set_paintable(GTK_PICTURE(state->picture), nullptr);
                gtk_widget_set_sensitive(state->remoteButton, TRUE);
                state->remoteActions.enable(0);
            }
        });
    });
}
void submit(const Json &value) {
    std::lock_guard lock(state->mutex);
    if (state->client) {
        auto text = value.dump();
        if (ng_remote_client_command(state->client, text.data(), text.size()) != 0)
            status("操作队列已满或权限不足");
    } else { status("操作尚未提交，远程连接不可用"); }
}
using netgo::desktop_linux::configureStartup;
void shareDesktop() {
    state->suspended = false;
    if (state->site.empty() || state->remoteStopping || state->remoteActive.exchange(true))
        return;
    if (state->remote.joinable())
        state->remote.join();
    auto site = state->site;
    bool unattended = gtk_check_button_get_active(GTK_CHECK_BUTTON(state->unattended));
    state->remote = std::thread([site, unattended] {
        try {
            struct HostContext {
                std::string site;
                netgo::desktop_linux::HostPlatform media;
            };
            HostContext bridge{site, {}};
            auto capabilities =
                bridge.media.prepare(59, std::filesystem::path(state->root) / "portal-restore", unattended,
                                     [] { return state->alive && state->remoteActive; });
            auto folder = std::filesystem::path(state->root) / "Exchange";
            std::filesystem::create_directories(folder);
            chmod(folder.c_str(), 0700);
            auto name = g_get_host_name();
            ng_remote_host_options o{};
            o.struct_size = sizeof(o);
            o.abi_version = NG_REMOTE_HOST_ABI;
            o.site_id = site.c_str();
            o.platform = "linux";
            o.name = name;
            o.exchange_directory = folder.c_str();
            o.capabilities = capabilities;
            o.unattended = unattended;
            o.context = &bridge;
            o.active = [](void *) -> int32_t { return state->alive && state->remoteActive; };
            o.control = [](void* p, const char* method, const char* path, const char* body, const char* key,
                           char* output, size_t capacity, size_t* length) -> int32_t {
                return netgo::desktop_linux::remoteControl(state->socket, static_cast<HostContext *>(p)->site,
                                                          state->remoteActive, method, path, body, key, output, capacity, length);
            };
            o.consent = [](void *, const char *user, uint32_t caps) -> int32_t {
                return netgo::desktop_linux::requestConsent(
                    [] { return GTK_WINDOW(state->window); }, mainThread,
                    [] { return state->alive && state->remoteActive; }, user, caps);
            };
            o.open = [](void *p, uint32_t caps, uint32_t *w, uint32_t *h, uint32_t *fps) -> int32_t {
                try {
                    static_cast<HostContext *>(p)->media.open(caps, *w, *h, *fps);
                    return 0;
                } catch (const std::exception &e) {
                    status(e.what());
                    return -1;
                }
            };
            o.poll = [](void *p, uint32_t *kind, uint8_t *out, size_t capacity, size_t *length) -> int32_t {
                *kind = 5;
                return static_cast<HostContext *>(p)->media.poll(out, capacity, *length) ? 0 : -1;
            };
            o.input = [](void *p, const char *data, size_t size) -> int32_t {
                try {
                    static_cast<HostContext *>(p)->media.input(Json::parse(data, data + size));
                    return 0;
                } catch (const netgo::desktop_linux::UnsupportedInput &) {
                    return 1;
                } catch (...) { return -1; }
            };
            o.close = [](void *p) { static_cast<HostContext *>(p)->media.close(); };
            o.clipboard = [](void *, int32_t set, const char *text, size_t size, char *out, size_t capacity,
                             size_t *length) -> int32_t {
                try {
                    auto value = netgo::desktop_linux::remoteClipboard(
                        [] { return GTK_WINDOW(state->window); }, mainThread,
                        [] { return state->alive && state->remoteActive; }, set != 0, std::string(text, size));
                    if (value.size() > capacity)
                        return -1;
                    std::copy(value.begin(), value.end(), out);
                    *length = value.size();
                    return 0;
                } catch (...) {
                    return -1;
                }
            };
            o.status = [](void *, const char *code) {
                std::string text = code;
                if (text == "remote_host_unattended")
                    text = "无人值守共享已开启；仅接受站点授权账号";
                else if (text == "remote_host_waiting")
                    text = "共享已开启，连接需要本机确认";
                else if (text == "remote_host_connected")
                    text = "正在共享桌面";
                status(text);
            };
            ng_remote_host *host = nullptr;
            if (ng_remote_host_create(&o, &host) != 0)
                throw std::runtime_error("remote_host_failed");
            {
                std::lock_guard lock(state->mutex);
                state->host = host;
                if (!state->remoteActive)
                    ng_remote_host_cancel(host);
            }
            ng_remote_host_run(host);
            {
                std::lock_guard lock(state->mutex);
                state->host = nullptr;
            }
            ng_remote_host_destroy(host);
        } catch (const std::exception &e) {
            status(e.what());
            if (std::string(e.what()).find("remote_portal_") == 0)
                mainThread([] { state->suspended = true; });
        }
        state->remoteActive = false;
        state->remoteStopping = false;
    });
}
void transferChosen(GObject *obj, GAsyncResult *result, void *data) {
    std::unique_ptr<std::string> name(static_cast<std::string *>(data));
    GError *error = nullptr;
    auto file = name->empty() ? gtk_file_dialog_open_finish(GTK_FILE_DIALOG(obj), result, &error)
                              : gtk_file_dialog_save_finish(GTK_FILE_DIALOG(obj), result, &error);
    if (file) {
        auto p = g_file_get_path(file);
        if (p) {
            auto local = std::filesystem::path(p);
            std::error_code pathError;
            const bool exists = !name->empty() && std::filesystem::exists(local, pathError);
            if (exists || pathError) status("请为下载选择新的文件名；下载不会覆盖原文件");
            else submit({{"action", name->empty() ? "upload" : "download"},
                         {"name", name->empty() ? local.filename().string() : *name},
                         {"path", p}});
            g_free(p);
        }
        g_object_unref(file);
    }
    if (error)
        g_error_free(error);
}
void applyProfiles(Json profiles) {
    if (!state->alive) return;
    auto previous = selected(state->profiles, state->profileList, "ID");
    state->loadingProfiles = true;
    state->profileList = std::move(profiles);
    names(state->profiles, state->profileList, "Name");
    for (size_t i = 0; i < state->profileList.size(); ++i)
        if (state->profileList[i].value("ID", "") == previous)
            gtk_drop_down_set_selected(GTK_DROP_DOWN(state->profiles), unsigned(i));
    state->loadingProfiles = false;
}
void refresh() {
    task([] {
        waitForService();
        auto profiles = call("GET", "/desktop/profiles");
        mainThread([profiles = std::move(profiles)]() mutable { applyProfiles(std::move(profiles)); });
    });
}
void chooseProfile(GObject *dialog, GAsyncResult *result, void *) {
    GError *error = nullptr;
    auto file = gtk_file_dialog_open_finish(GTK_FILE_DIALOG(dialog), result, &error);
    if (!file) {
        if (error)
            g_error_free(error);
        return;
    }
    auto path = g_file_get_path(file);
    std::string name = path ? path : "";
    g_free(path);
    g_object_unref(file);
    task([name] {
        std::ifstream input(name, std::ios::binary);
        if (!input || std::filesystem::file_size(name) > 1048576)
            throw std::runtime_error("invalid_site_profile");
        Json profile;
        input >> profile;
        bool changed = false;
        for (const auto &old : call("GET", "/desktop/profiles"))
            if (old.at("ID") == profile.at("ID") && (old.value("Origin", "") != profile.value("Origin", "") ||
                                                     old.value("CA", "") != profile.value("CA", "")))
                changed = true;
        if (!changed) {
            call("POST", "/desktop/import", profile);
            status("站点已导入");
            return;
        }
        mainThread([profile = std::move(profile)] {
            if (!state->alive)
                return;
            auto dialog =
                gtk_alert_dialog_new("文件更改了站点地址或证书。请确认它来自你的服务器管理员后继续。");
            const char *buttons[] = {"取消", "确认导入", nullptr};
            gtk_alert_dialog_set_buttons(dialog, buttons);
            gtk_alert_dialog_set_cancel_button(dialog, 0);
            gtk_alert_dialog_choose(
                dialog, GTK_WINDOW(state->window), nullptr,
                [](GObject *object, GAsyncResult *result, void *context) {
                    std::unique_ptr<Json> value(static_cast<Json *>(context));
                    GError *error = nullptr;
                    auto selected = gtk_alert_dialog_choose_finish(GTK_ALERT_DIALOG(object), result, &error);
                    if (error) {
                        g_error_free(error);
                        return;
                    }
                    if (selected == 1)
                        task([profile = std::move(*value)] {
                            call("POST", "/desktop/import-trusted", profile);
                            status("站点已导入");
                        });
                },
                new Json(std::move(profile)));
            g_object_unref(dialog);
        });
    });
}
void activate(GtkApplication *app, void *) {
    state->app = app;
    state->window = gtk_application_window_new(app);
    gtk_window_set_title(GTK_WINDOW(state->window), "NetGo");
    gtk_window_set_default_size(GTK_WINDOW(state->window), 960, 700);
    auto box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 10);
    gtk_widget_set_margin_start(box, 16);
    gtk_widget_set_margin_end(box, 16);
    gtk_widget_set_margin_top(box, 16);
    gtk_widget_set_margin_bottom(box, 16);
    gtk_window_set_child(GTK_WINDOW(state->window), box);
    auto row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(box), row);
    state->profiles = gtk_drop_down_new(nullptr, nullptr);
    gtk_widget_set_hexpand(state->profiles, TRUE);
    gtk_box_append(GTK_BOX(row), state->profiles);
    auto import = gtk_button_new_with_label("导入站点…");
    gtk_box_append(GTK_BOX(row), import);
    g_signal_connect(import, "clicked", callback(+[](GtkButton *, void *) {
                         auto dialog = gtk_file_dialog_new();
                         gtk_file_dialog_set_title(dialog, "导入 NetGo 站点");
                         gtk_file_dialog_open(dialog, GTK_WINDOW(state->window), nullptr, chooseProfile,
                                              nullptr);
                         g_object_unref(dialog);
                     }),
                     nullptr);
    auto refreshButton = gtk_button_new_with_label("刷新");
    gtk_box_append(GTK_BOX(row), refreshButton);
    g_signal_connect(refreshButton, "clicked", callback(+[](GtkButton *, void *) { refresh(); }), nullptr);
    row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(box), row);
    state->alias = gtk_entry_new();
    gtk_entry_set_placeholder_text(GTK_ENTRY(state->alias), "账号");
    gtk_box_append(GTK_BOX(row), state->alias);
    state->password = gtk_password_entry_new();
    gtk_box_append(GTK_BOX(row), state->password);
    state->replace = gtk_check_button_new_with_label("替换此账号的其他连接");
    gtk_box_append(GTK_BOX(box), state->replace);
    state->internet = gtk_check_button_new_with_label("通过旁路由访问互联网（需要账号授权）");
    gtk_box_append(GTK_BOX(box), state->internet);
    state->connect = gtk_button_new_with_label("连接网络");
    gtk_box_append(GTK_BOX(row), state->connect);
    g_signal_connect(
        state->connect, "clicked", callback(+[](GtkButton *, void *) {
            auto id = selected(state->profiles, state->profileList, "ID");
            if (id.empty()) {
                status("请先导入并选择站点");
                return;
            }
            if (state->remoteActive) {
                status("请先结束远程桌面");
                return;
            }
            state->site = id;
            Json request = {
                {"Site", state->site},
                {"Alias", gtk_editable_get_text(GTK_EDITABLE(state->alias))},
                {"Password", gtk_editable_get_text(GTK_EDITABLE(state->password))},
                {"Replace", bool(gtk_check_button_get_active(GTK_CHECK_BUTTON(state->replace)))},
                {"Internet", bool(gtk_check_button_get_active(GTK_CHECK_BUTTON(state->internet)))},
                {"Remember", bool(gtk_check_button_get_active(GTK_CHECK_BUTTON(state->unattended)))}};
            state->autoResume = request.at("Remember");
            state->suspended = false;
            gtk_editable_set_text(GTK_EDITABLE(state->password), "");
            auto version = state->epoch.advance();
            task([request = std::move(request), version] {
                prepareNetwork();
                if (!state->epoch.matches(version))
                    return;
                call("POST", "/desktop/connect", request);
                if (!state->epoch.matches(version))
                    call("POST", "/desktop/disconnect");
            });
        }),
        nullptr);
    auto disconnect = gtk_button_new_with_label("断开网络");
    gtk_box_append(GTK_BOX(row), disconnect);
    g_signal_connect(disconnect, "clicked", callback(+[](GtkButton *, void *) {
                         stopRemote();
                         disconnectNetwork();
                     }),
                     nullptr);
    state->resources = gtk_label_new("");
    gtk_label_set_wrap(GTK_LABEL(state->resources), TRUE);
    gtk_box_append(GTK_BOX(box), state->resources);
    state->notice = gtk_label_new("导入站点后使用已有账号连接");
    gtk_label_set_xalign(GTK_LABEL(state->notice), 0);
    gtk_box_append(GTK_BOX(box), state->notice);
    row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(box), row);
    state->hosts = gtk_drop_down_new(nullptr, nullptr);
    gtk_widget_set_hexpand(state->hosts, TRUE);
    gtk_box_append(GTK_BOX(row), state->hosts);
    auto hosts = gtk_button_new_with_label("更新远程主机");
    gtk_box_append(GTK_BOX(row), hosts);
    g_signal_connect(hosts, "clicked", callback(+[](GtkButton *, void *) {
                         if (state->site.empty() || state->remoteActive)
                             return;
                         state->hostList = Json::array();
                         names(state->hosts, state->hostList, "name");
                         auto version = state->epoch.current();
                         auto site = state->site;
                         task([version, site] {
                             auto result = netgo::remote::inventory([&](const std::string& path){ return call("POST", "/desktop/remote/call",
                                                {{"site", site},
                                                 {"method", "GET"},
                                                 {"path", path},
                                                 {"body", nullptr},
                                                 {"idempotency", ""}}); });
                             mainThread([result = std::move(result), version] {
                                 if (!state->alive || !state->epoch.matches(version))
                                     return;
                                 state->hostList = Json::array();
                                 for (auto host : result.at("hosts"))
                                     if (host.value("available_capabilities", 0u) & NG_REMOTE_VIEW) {
                                         host["name"] =
                                             host.value("name", "") + " · " + host.value("status", "");
                                         state->hostList.push_back(host);
                                     }
                                 names(state->hosts, state->hostList, "name");
                                 if (state->hostList.empty())
                                     status("暂无可连接的已授权主机；请确认对方已连接 VPN 并开启共享。");
                             });
                         });
                     }),
                     nullptr);
    state->viewOnly = gtk_check_button_new_with_label("本次仅查看");
    gtk_box_append(GTK_BOX(row), state->viewOnly);
    state->audio = gtk_check_button_new_with_label("播放对方声音（主机支持时）");
    gtk_check_button_set_active(GTK_CHECK_BUTTON(state->audio), TRUE);
    gtk_box_append(GTK_BOX(row), state->audio);
    state->remoteButton = gtk_button_new_with_label("打开远程桌面");
    gtk_box_append(GTK_BOX(row), state->remoteButton);
    g_signal_connect(state->remoteButton, "clicked", callback(+[](GtkButton *, void *) {
                         auto id = selected(state->hosts, state->hostList, "id");
                         auto index = gtk_drop_down_get_selected(GTK_DROP_DOWN(state->hosts));
                         if (!id.empty() && index < state->hostList.size() &&
                             state->hostList[index].value("status", "") == "ready")
                             view(id);
                     }),
                     nullptr);
    row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(box), row);
    state->unattended =
        gtk_check_button_new_with_label("无人值守：保存登录凭据并在系统登录后恢复（连接前勾选）");
    gtk_box_append(GTK_BOX(row), state->unattended);
    g_signal_connect(state->unattended, "toggled", callback(+[](GtkCheckButton *button, void *) {
                         bool enabled = gtk_check_button_get_active(button);
                         state->autoResume = enabled;
                         state->suspended = !enabled;
                         try {
                             configureStartup(enabled);
                         } catch (const std::exception &e) {
                             status(e.what());
                         }
                         if (!enabled) {
                             stopRemote();
                             state->forgetUnattended = true;
                             if (!state->jobBusy)
                                 task([] {});
                         }
                     }),
                     nullptr);
    auto share = gtk_button_new_with_label("共享此电脑");
    gtk_box_append(GTK_BOX(row), share);
    g_signal_connect(share, "clicked", callback(+[](GtkButton *, void *) { shareDesktop(); }), nullptr);
    auto clipSend = gtk_button_new_with_label("发送剪贴板");
    gtk_box_append(GTK_BOX(row), clipSend);
    state->remoteActions.add(clipSend, NG_REMOTE_CLIPBOARD);
    g_signal_connect(clipSend, "clicked", callback(+[](GtkButton *, void *) {
                         task([] {
                             auto text = netgo::desktop_linux::remoteClipboard(
                                 [] { return GTK_WINDOW(state->window); }, mainThread,
                                 [] { return state->alive && state->remoteActive; }, false, "");
                             submit({{"action", "clipboard.set"}, {"text", text}});
                         });
                     }),
                     nullptr);
    auto clipGet = gtk_button_new_with_label("获取剪贴板");
    gtk_box_append(GTK_BOX(row), clipGet);
    state->remoteActions.add(clipGet, NG_REMOTE_CLIPBOARD);
    g_signal_connect(clipGet, "clicked",
                     callback(+[](GtkButton *, void *) { submit({{"action", "clipboard.get"}}); }), nullptr);
    row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_append(GTK_BOX(box), row);
    auto upload = gtk_button_new_with_label("上传文件");
    gtk_box_append(GTK_BOX(row), upload);
    state->remoteActions.add(upload, NG_REMOTE_UPLOAD);
    g_signal_connect(upload, "clicked", callback(+[](GtkButton *, void *) {
                         auto d = gtk_file_dialog_new();
                         gtk_file_dialog_open(d, GTK_WINDOW(state->window), nullptr, transferChosen,
                                              new std::string());
                         g_object_unref(d);
                     }),
                     nullptr);
    state->files = gtk_drop_down_new(nullptr, nullptr);
    gtk_box_append(GTK_BOX(row), state->files);
    auto files = gtk_button_new_with_label("刷新文件");
    gtk_box_append(GTK_BOX(row), files);
    state->remoteActions.add(files, NG_REMOTE_DOWNLOAD);
    g_signal_connect(files, "clicked",
                     callback(+[](GtkButton *, void *) { submit({{"action", "file.list"}}); }), nullptr);
    auto download = gtk_button_new_with_label("下载选中文件");
    gtk_box_append(GTK_BOX(row), download);
    state->remoteActions.add(download, NG_REMOTE_DOWNLOAD);
    g_signal_connect(download, "clicked", callback(+[](GtkButton *, void *) {
                         auto name = selected(state->files, state->remoteFiles, "name");
                         if (name.empty())
                             return;
                         auto d = gtk_file_dialog_new();
                         gtk_file_dialog_set_initial_name(d, name.c_str());
                         gtk_file_dialog_set_title(d, "保存下载（请使用新的文件名）");
                         gtk_file_dialog_save(d, GTK_WINDOW(state->window), nullptr, transferChosen,
                                              new std::string(name));
                         g_object_unref(d);
                     }),
                     nullptr);
    auto cancel = gtk_button_new_with_label("取消传输");
    gtk_box_append(GTK_BOX(row), cancel);
    state->remoteActions.add(cancel, NG_REMOTE_VIEW);
    g_signal_connect(cancel, "clicked",
                     callback(+[](GtkButton *, void *) { submit({{"action", "cancel"}}); }), nullptr);
    auto stop = gtk_button_new_with_label("结束远程桌面");
    gtk_box_append(GTK_BOX(row), stop);
    g_signal_connect(stop, "clicked", callback(+[](GtkButton *, void *) {
                         state->suspended = true;
                         stopRemote();
                     }),
                     nullptr);
    state->picture = gtk_picture_new();
    gtk_widget_set_vexpand(state->picture, TRUE);
    gtk_picture_set_content_fit(GTK_PICTURE(state->picture), GTK_CONTENT_FIT_CONTAIN);
    gtk_widget_set_focusable(state->picture, TRUE);
    gtk_box_append(GTK_BOX(box), state->picture);
    auto motion = gtk_event_controller_motion_new();
    g_signal_connect(motion, "motion", callback(+[](GtkEventControllerMotion *, double x, double y, void *) {
                         point(x, y, false);
                     }),
                     nullptr);
    gtk_widget_add_controller(state->picture, motion);
    auto click = gtk_gesture_click_new();
    gtk_gesture_single_set_button(GTK_GESTURE_SINGLE(click), 0);
    g_signal_connect(click, "pressed", callback(+[](GtkGestureClick *g, int, double x, double y, void *) {
                         auto button = gtk_gesture_single_get_current_button(GTK_GESTURE_SINGLE(g));
                         if (point(x, y, false) && button <= 3) {
                             gtk_widget_grab_focus(state->picture);
                             queue({{"type", "button"},
                                    {"button", button == 2   ? 3
                                               : button == 3 ? 2
                                                             : 1},
                                    {"down", true}});
                         }
                     }),
                     nullptr);
    g_signal_connect(click, "released", callback(+[](GtkGestureClick *g, int, double x, double y, void *) {
                         point(x, y, true);
                         auto button = gtk_gesture_single_get_current_button(GTK_GESTURE_SINGLE(g));
                         if (button <= 3)
                             queue({{"type", "button"},
                                    {"button", button == 2   ? 3
                                               : button == 3 ? 2
                                                             : 1},
                                    {"down", false}});
                     }),
                     nullptr);
    g_signal_connect(
        click, "cancel",
        callback(+[](GtkGesture *, GdkEventSequence *, void *) { queue({{"type", "release"}}); }), nullptr);
    gtk_widget_add_controller(state->picture, GTK_EVENT_CONTROLLER(click));
    auto scroll = gtk_event_controller_scroll_new(GTK_EVENT_CONTROLLER_SCROLL_BOTH_AXES);
    g_signal_connect(scroll, "scroll",
                     callback(+[](GtkEventControllerScroll *, double x, double y, void *) -> gboolean {
                         if (y)
                             queue({{"type", "wheel"},
                                    {"delta", int(std::clamp(-y * 120, -1200.0, 1200.0))},
                                    {"horizontal", false}});
                         if (x)
                             queue({{"type", "wheel"},
                                    {"delta", int(std::clamp(x * 120, -1200.0, 1200.0))},
                                    {"horizontal", true}});
                         return TRUE;
                     }),
                     nullptr);
    gtk_widget_add_controller(state->picture, scroll);
    state->ime = gtk_im_multicontext_new();
    gtk_im_context_set_client_widget(state->ime, state->picture);
    g_signal_connect(state->ime, "commit", callback(+[](GtkIMContext *, const char *text, void *) {
                         if (text && std::strlen(text) <= 8192)
                             queue({{"type", "text"}, {"text", text}});
                     }),
                     nullptr);
    auto keys = gtk_event_controller_key_new();
    g_signal_connect(
        keys, "key-pressed",
        callback(+[](GtkEventControllerKey *c, guint key, guint, GdkModifierType mods, void *) -> gboolean {
            if (!(mods & (GDK_CONTROL_MASK | GDK_ALT_MASK)) &&
                gtk_im_context_filter_keypress(
                    state->ime, gtk_event_controller_get_current_event(GTK_EVENT_CONTROLLER(c))))
                return TRUE;
            auto code = virtualKey(key);
            if (code)
                queue({{"type", "key"},
                       {"code", code},
                       {"extended", extendedKey(key)},
                       {"down", true}});
            return TRUE;
        }),
        nullptr);
    g_signal_connect(keys, "key-released",
                     callback(+[](GtkEventControllerKey *, guint key, guint, GdkModifierType, void *) {
                         auto code = virtualKey(key);
                         if (code)
                             queue({{"type", "key"},
                                    {"code", code},
                                    {"extended", extendedKey(key)},
                                    {"down", false}});
                     }),
                     nullptr);
    gtk_widget_add_controller(state->picture, keys);
    auto focus = gtk_event_controller_focus_new();
    g_signal_connect(
        focus, "enter",
        callback(+[](GtkEventControllerFocus *, void *) { gtk_im_context_focus_in(state->ime); }), nullptr);
    g_signal_connect(focus, "leave", callback(+[](GtkEventControllerFocus *, void *) {
                         gtk_im_context_focus_out(state->ime);
                         queue({{"type", "release"}});
                     }),
                     nullptr);
    gtk_widget_add_controller(state->picture, focus);
    g_signal_connect(state->window, "close-request", callback(+[](GtkWindow *, void *) -> gboolean {
                         stopRemote();
                         state->alive = false;
                         return FALSE;
                     }),
                     nullptr);
    g_signal_connect(state->profiles, "notify::selected", callback(+[](GObject *, GParamSpec *, void *) {
                         if (state->loadingProfiles || state->site.empty())
                             return;
                         auto id = selected(state->profiles, state->profileList, "ID");
                         if (id != state->site) {
                             stopRemote();
                             disconnectNetwork();
                             state->site.clear();
                             gtk_label_set_text(GTK_LABEL(state->resources), "");
                         }
                     }),
                     nullptr);
    gtk_window_present(GTK_WINDOW(state->window));
    if (state->smoke) {
        g_timeout_add(
            1000,
            [](void *) -> gboolean {
                g_application_quit(G_APPLICATION(state->app));
                return G_SOURCE_REMOVE;
            },
            nullptr);
        return;
    }
    state->timer = g_timeout_add(
        1000,
        [](void *) -> gboolean {
            if (!state->alive)
                return G_SOURCE_REMOVE;
            if (state->jobBusy || state->cancelling)
                return G_SOURCE_CONTINUE;
            bool initial = !state->restoreChecked;
            bool resume = state->autoResume && !state->suspended;
            bool retry = g_get_monotonic_time() >= state->nextRestore;
            if (retry)
                state->nextRestore = g_get_monotonic_time() + 30000000;
            auto version = state->epoch.current();
            task([initial, resume, retry, version] {
                if (!state->service.ensure(state->root, state->socket)) {
                    status("正在恢复连接服务…");
                    return;
                }
                auto settings = initial ? call("GET", "/desktop/unattended") : Json();
                auto profiles = initial ? call("GET", "/desktop/profiles") : Json();
                auto value = call("GET", "/desktop/status");
                auto phase = value.value("state", "");
                bool configured = initial && !settings.value("site", "").empty();
                if ((configured || (resume && retry)) && (phase == "failed" || phase == "disconnected") &&
                    state->epoch.matches(version)) {
                    try {
                        call("POST", "/desktop/unattended/restore");
                        value = call("GET", "/desktop/status");
                    } catch (const std::exception &e) {
                        status(e.what());
                    }
                }
                mainThread([value = std::move(value), settings = std::move(settings), profiles = std::move(profiles), initial, version]() mutable {
                    if (!state->alive || !state->epoch.matches(version))
                        return;
                    if (initial) {
                        applyProfiles(std::move(profiles));
                        state->restoreChecked = true;
                        auto saved = settings.value("site", "");
                        if (!saved.empty()) {
                            state->site = saved;
                            state->autoResume = true;
                            state->loadingProfiles = true;
                            for (size_t i = 0; i < state->profileList.size(); ++i)
                                if (state->profileList[i].value("ID", "") == saved)
                                    gtk_drop_down_set_selected(GTK_DROP_DOWN(state->profiles), unsigned(i));
                            state->loadingProfiles = false;
                            gtk_check_button_set_active(GTK_CHECK_BUTTON(state->unattended), TRUE);
                        }
                    }
                    std::string resources;
                    for (const auto &r :
                         value.value("lease", Json::object()).value("resources", Json::array()))
                        resources += r.value("name", "") + " · " + r.value("cidr", "") + "  ";
                    gtk_label_set_text(GTK_LABEL(state->resources), resources.c_str());
                    if (!state->remoteActive)
                        status(value.value("state", "") + "  " + value.value("error", ""));
                    if (!value.value("remote_ready", false)) {
                        state->hostList = Json::array();
                        names(state->hosts, state->hostList, "name");
                    } else if (state->autoResume && !state->suspended && !state->remoteActive &&
                               value.value("site_id", "") == state->site &&
                               g_get_monotonic_time() >= state->nextShare) {
                        state->nextShare = g_get_monotonic_time() + 30000000;
                        shareDesktop();
                    }
                });
            });
            return G_SOURCE_CONTINUE;
        },
        nullptr);
    refresh();
}
} // namespace
int main(int argc, char **argv) {
    XInitThreads();
    if (argc == 2 && std::string(argv[1]) == "--version") {
        g_print("{\"version\":\"%s\",\"platform\":\"linux\",\"arch\":\"amd64\"}\n", NETGO_BUILD_VERSION);
        return 0;
    }
    State storage;
    state = &storage;
    auto config = g_get_user_config_dir();
    state->root = std::string(config) + "/NetGo";
    state->socket = state->root + "/client.sock";
    for (int i = 1; i < argc; ++i) {
        if (std::string(argv[i]) == "--smoke")
            state->smoke = true;
        else if (std::string(argv[i]) == "--socket" && i + 1 < argc)
            state->socket = argv[++i];
        else
            return 2;
    }
    curl_global_init(CURL_GLOBAL_DEFAULT);
    auto app = gtk_application_new("org.netgo.desktop", G_APPLICATION_DEFAULT_FLAGS);
    g_signal_connect(app, "activate", callback(activate), nullptr);
    int result = g_application_run(G_APPLICATION(app), 1, argv);
    state->alive = false;
    stopRemote();
    if (state->timer)
        g_source_remove(state->timer);
    if (state->job.joinable())
        state->job.join();
    if (state->remote.joinable())
        state->remote.join();
    if (state->cancelJob.joinable())
        state->cancelJob.join();
    while (g_main_context_pending(nullptr))
        g_main_context_iteration(nullptr, FALSE);
    g_object_unref(app);
    curl_global_cleanup();
    return result;
}
