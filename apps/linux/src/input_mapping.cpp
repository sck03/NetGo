#include "input_mapping.hpp"
#include <gdk/gdkkeysyms.h>
namespace netgo::desktop_linux {
uint32_t virtualKey(unsigned key) {
    if (key >= GDK_KEY_a && key <= GDK_KEY_z)
        return key - GDK_KEY_a + 'A';
    if (key >= GDK_KEY_A && key <= GDK_KEY_Z)
        return key;
    if (key >= GDK_KEY_0 && key <= GDK_KEY_9)
        return key;
    if (key >= GDK_KEY_F1 && key <= GDK_KEY_F24)
        return 112 + key - GDK_KEY_F1;
    switch (key) {
    case GDK_KEY_Return:
        return 13;
    case GDK_KEY_BackSpace:
        return 8;
    case GDK_KEY_Tab:
        return 9;
    case GDK_KEY_Escape:
        return 27;
    case GDK_KEY_space:
        return 32;
    case GDK_KEY_Left:
        return 37;
    case GDK_KEY_Up:
        return 38;
    case GDK_KEY_Right:
        return 39;
    case GDK_KEY_Down:
        return 40;
    case GDK_KEY_Delete:
        return 46;
    case GDK_KEY_Home:
        return 36;
    case GDK_KEY_End:
        return 35;
    case GDK_KEY_Page_Up:
        return 33;
    case GDK_KEY_Page_Down:
        return 34;
    case GDK_KEY_Shift_L:
    case GDK_KEY_Shift_R:
        return 16;
    case GDK_KEY_Control_L:
    case GDK_KEY_Control_R:
        return 17;
    case GDK_KEY_Alt_L:
    case GDK_KEY_Alt_R:
        return 18;
    default:
        return 0;
    }
}
}
