#pragma once
#include <gtk/gtk.h>
#include <cstdint>
#include <utility>
#include <vector>

namespace netgo::desktop_linux {
// GUI-thread bindings for negotiated remote capabilities. Core authorization
// remains enforced by the remote client even when a queued dialog completes.
class RemoteActions {
    std::vector<std::pair<GtkWidget *, uint32_t>> actions_;
public:
    void add(GtkWidget *widget, uint32_t capability) {
        actions_.emplace_back(widget, capability);
        gtk_widget_set_sensitive(widget, FALSE);
    }
    void enable(uint32_t capabilities) const {
        for (const auto &[widget, required] : actions_)
            gtk_widget_set_sensitive(widget, (capabilities & required) != 0);
    }
};
}
