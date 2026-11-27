#pragma once
#include "client.hpp"
#include <gtk/gtk.h>
#include <memory>

namespace netgo::desktop_linux {
// The texture retains the immutable pixels until GTK releases them.
void presentFrame(GtkPicture *picture, std::shared_ptr<const Frame> frame);
}
