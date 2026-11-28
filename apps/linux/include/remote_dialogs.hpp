#pragma once
#include <gtk/gtk.h>
#include <cstdint>
#include <functional>
#include <string>

namespace netgo::desktop_linux {
using MainDispatch = std::function<void(std::function<void()>)>;
using WindowProvider = std::function<GtkWindow *()>;
using DialogActive = std::function<bool()>;
std::string remoteClipboard(WindowProvider window, MainDispatch dispatch, DialogActive active,
                            bool set, const std::string &text);
bool requestConsent(WindowProvider window, MainDispatch dispatch, DialogActive active,
                    const std::string &account, uint32_t capabilities);
}
