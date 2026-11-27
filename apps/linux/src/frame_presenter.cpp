#include "frame_presenter.hpp"

namespace netgo::desktop_linux {
void presentFrame(GtkPicture *picture, std::shared_ptr<const Frame> frame) {
    if (!frame || !frame->bgra || frame->bgra->empty()) return;
    auto owner = new std::shared_ptr<const Frame>(std::move(frame));
    const auto &pixels = **owner;
    auto bytes = g_bytes_new_with_free_func(pixels.bgra->data(), pixels.bgra->size(),
        [](gpointer p) { delete static_cast<std::shared_ptr<const Frame> *>(p); }, owner);
    auto texture = gdk_memory_texture_new(int(pixels.width), int(pixels.height),
        GDK_MEMORY_B8G8R8A8, bytes, pixels.width * 4);
    g_bytes_unref(bytes);
    gtk_picture_set_paintable(picture, GDK_PAINTABLE(texture));
    g_object_unref(texture);
}
}
