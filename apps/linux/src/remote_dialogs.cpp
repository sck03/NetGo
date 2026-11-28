#include "remote_dialogs.hpp"
#include <atomic>
#include <chrono>
#include <future>
#include <memory>
#include <stdexcept>
#include <thread>

namespace netgo::desktop_linux {
namespace {
struct DialogReply {
    std::atomic_bool expired = false;
    GCancellable *cancel = g_cancellable_new();
    ~DialogReply() { g_object_unref(cancel); }
    void stop() { expired = true; g_cancellable_cancel(cancel); }
};
struct ClipboardReply : DialogReply { std::promise<std::string> value; };
struct ConsentReply : DialogReply { std::atomic_bool done = false, allowed = false; };
}

std::string remoteClipboard(WindowProvider window, MainDispatch dispatch, DialogActive active,
                            bool set, const std::string &text) {
    auto reply = std::make_shared<ClipboardReply>();
    auto future = reply->value.get_future();
    dispatch([reply, window, active, set, text] {
        if (reply->expired || !active()) { reply->value.set_value(""); return; }
        auto clip = gdk_display_get_clipboard(gtk_widget_get_display(GTK_WIDGET(window())));
        if (set) {
            gdk_clipboard_set_text(clip, text.c_str());
            reply->value.set_value("");
            return;
        }
        gdk_clipboard_read_text_async(clip, reply->cancel,
            [](GObject *obj, GAsyncResult *result, void *user) {
                std::unique_ptr<std::shared_ptr<ClipboardReply>> reply(
                    static_cast<std::shared_ptr<ClipboardReply> *>(user));
                GError *error = nullptr;
                auto text = gdk_clipboard_read_text_finish(GDK_CLIPBOARD(obj), result, &error);
                std::string value = text ? text : "";
                g_free(text);
                if (error) g_error_free(error);
                (*reply)->value.set_value(std::move(value));
            }, new std::shared_ptr<ClipboardReply>(reply));
    });
    auto until = std::chrono::steady_clock::now() + std::chrono::seconds(2);
    while (active() && std::chrono::steady_clock::now() < until) {
        if (future.wait_for(std::chrono::milliseconds(50)) == std::future_status::ready) {
            auto out = future.get();
            if (out.size() > 8192) throw std::runtime_error("remote_clipboard_too_large");
            return out;
        }
    }
    reply->stop();
    throw std::runtime_error(active() ? "remote_clipboard_timeout" : "remote_closed");
}

bool requestConsent(WindowProvider window, MainDispatch dispatch, DialogActive active,
                    const std::string &account, uint32_t caps) {
    auto result = std::make_shared<ConsentReply>();
    dispatch([result, window, active, account, caps] {
        if (result->expired || !active()) { result->done = true; return; }
        auto text = "站点账号 " + account + " 请求查看屏幕" +
                    std::string((caps & 2) ? "、控制键鼠" : "") +
                    ((caps & 8) ? "、读写剪贴板" : "") +
                    ((caps & 48) ? "、收发交换目录文件" : "");
        auto dialog = gtk_alert_dialog_new("%s", text.c_str());
        const char *buttons[] = {"拒绝", "允许", nullptr};
        gtk_alert_dialog_set_buttons(dialog, buttons);
        gtk_alert_dialog_set_cancel_button(dialog, 0);
        gtk_alert_dialog_set_default_button(dialog, 0);
        gtk_alert_dialog_set_modal(dialog, TRUE);
        gtk_alert_dialog_choose(dialog, window(), result->cancel,
            [](GObject *obj, GAsyncResult *reply, void *user) {
                std::unique_ptr<std::shared_ptr<ConsentReply>> result(
                    static_cast<std::shared_ptr<ConsentReply> *>(user));
                GError *error = nullptr;
                auto choice = gtk_alert_dialog_choose_finish(GTK_ALERT_DIALOG(obj), reply, &error);
                (*result)->allowed = !error && choice == 1 && !(*result)->expired;
                (*result)->done = true;
                if (error) g_error_free(error);
            }, new std::shared_ptr<ConsentReply>(result));
        g_object_unref(dialog);
    });
    auto until = std::chrono::steady_clock::now() + std::chrono::seconds(15);
    while (!result->done && active() && std::chrono::steady_clock::now() < until)
        std::this_thread::sleep_for(std::chrono::milliseconds(50));
    if (result->done && active()) return result->allowed;
    result->stop();
    return false;
}
