#include "input_mapping.hpp"
#include <gdk/gdkkeysyms.h>
namespace netgo::desktop_linux {
bool extendedKey(unsigned key) {
    switch (virtualKey(key)) {
    case 33: case 34: case 35: case 36: case 37: case 38: case 39: case 40:
    case 45: case 46: case 91: case 92: case 93: case 111: case 144: case 163: case 165:
        return true;
    default: return key == GDK_KEY_KP_Enter;
    }
}
uint32_t virtualKey(unsigned key) {
    if (key >= GDK_KEY_a && key <= GDK_KEY_z)
        return key - GDK_KEY_a + 'A';
    if (key >= GDK_KEY_A && key <= GDK_KEY_Z)
        return key;
    if (key >= GDK_KEY_0 && key <= GDK_KEY_9)
        return key;
    if (key >= GDK_KEY_F1 && key <= GDK_KEY_F24)
        return 112 + key - GDK_KEY_F1;
    if (key >= GDK_KEY_KP_0 && key <= GDK_KEY_KP_9)
        return 96 + key - GDK_KEY_KP_0;
    switch (key) {
    case GDK_KEY_KP_Enter:
        return 13;
    case GDK_KEY_Pause:
        return 19;
    case GDK_KEY_Caps_Lock:
        return 20;
    case GDK_KEY_Print:
        return 44;
    case GDK_KEY_Insert:
        return 45;
    case GDK_KEY_Super_L:
    case GDK_KEY_Meta_L:
        return 91;
    case GDK_KEY_Super_R:
    case GDK_KEY_Meta_R:
        return 92;
    case GDK_KEY_Menu:
        return 93;
    case GDK_KEY_Num_Lock:
        return 144;
    case GDK_KEY_Scroll_Lock:
        return 145;
    case GDK_KEY_KP_Insert:
        return 96;
    case GDK_KEY_KP_End:
        return 97;
    case GDK_KEY_KP_Down:
        return 98;
    case GDK_KEY_KP_Page_Down:
        return 99;
    case GDK_KEY_KP_Left:
        return 100;
    case GDK_KEY_KP_Begin:
        return 101;
    case GDK_KEY_KP_Right:
        return 102;
    case GDK_KEY_KP_Home:
        return 103;
    case GDK_KEY_KP_Up:
        return 104;
    case GDK_KEY_KP_Page_Up:
        return 105;
    case GDK_KEY_KP_Multiply:
        return 106;
    case GDK_KEY_KP_Add:
        return 107;
    case GDK_KEY_KP_Separator:
        return 108;
    case GDK_KEY_KP_Subtract:
        return 109;
    case GDK_KEY_KP_Decimal:
    case GDK_KEY_KP_Delete:
        return 110;
    case GDK_KEY_KP_Divide:
        return 111;
    case GDK_KEY_semicolon:
    case GDK_KEY_colon:
        return 186;
    case GDK_KEY_equal:
    case GDK_KEY_plus:
        return 187;
    case GDK_KEY_comma:
    case GDK_KEY_less:
        return 188;
    case GDK_KEY_minus:
    case GDK_KEY_underscore:
        return 189;
    case GDK_KEY_period:
    case GDK_KEY_greater:
        return 190;
    case GDK_KEY_slash:
    case GDK_KEY_question:
        return 191;
    case GDK_KEY_grave:
    case GDK_KEY_asciitilde:
        return 192;
    case GDK_KEY_bracketleft:
    case GDK_KEY_braceleft:
        return 219;
    case GDK_KEY_backslash:
    case GDK_KEY_bar:
        return 220;
    case GDK_KEY_bracketright:
    case GDK_KEY_braceright:
        return 221;
    case GDK_KEY_apostrophe:
    case GDK_KEY_quotedbl:
        return 222;

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
    case GDK_KEY_Shift_L: return 160;
    case GDK_KEY_Shift_R: return 161;
    case GDK_KEY_Control_L: return 162;
    case GDK_KEY_Control_R: return 163;
    case GDK_KEY_Alt_L: return 164;
    case GDK_KEY_Alt_R: return 165;
    default:
        return 0;
    }
}
}
