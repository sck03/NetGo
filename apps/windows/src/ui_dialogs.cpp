#include "ui.hpp"
#include <commdlg.h>

namespace netgo {
namespace {
std::wstring windowText(HWND h) {
    int n = GetWindowTextLengthW(h);
    std::wstring s(n + 1, L'\0');
    GetWindowTextW(h, s.data(), n + 1);
    s.resize(n);
    return s;
}
struct Editor {
    HWND edit = nullptr;
    Json value;
    bool accepted = false, done = false;
};
LRESULT CALLBACK editorProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    auto data = reinterpret_cast<Editor *>(GetWindowLongPtrW(h, GWLP_USERDATA));
    if (m == WM_NCCREATE) {
        data = static_cast<Editor *>(reinterpret_cast<CREATESTRUCTW *>(l)->lpCreateParams);
        SetWindowLongPtrW(h, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(data));
    }
    if (m == WM_COMMAND && LOWORD(w) == IDOK) {
        try {
            auto p = Json::parse(utf8(windowText(data->edit)));
            validateProfile(p);
            data->value = std::move(p);
            data->accepted = true;
            DestroyWindow(h);
        } catch (const std::exception &) {
            MessageBoxW(h,
                        L"配置无效。请核对站点 ID、HTTPS 域名、直达入口、路由节点 IP 和公共 "
                        L"CA；文件不应包含账号密码。",
                        L"检查站点配置", MB_ICONWARNING);
        }
        return 0;
    }
    if (m == WM_COMMAND && LOWORD(w) == IDCANCEL) {
        DestroyWindow(h);
        return 0;
    }
    if (m == WM_CLOSE) {
        DestroyWindow(h);
        return 0;
    }
    if (m == WM_DESTROY) {
        data->done = true;
        return 0;
    }
    return DefWindowProcW(h, m, w, l);
}
}
std::filesystem::path profileFileDialog(HWND owner, bool save, const wchar_t *title) {
    wchar_t path[32768]{};
    OPENFILENAMEW f{};
    f.lStructSize = sizeof(f);
    f.hwndOwner = owner;
    f.lpstrFile = path;
    f.nMaxFile = 32768;
    f.lpstrFilter = L"NetGo 站点或诊断 (*.json)\0*.json\0所有文件\0*.*\0";
    f.lpstrDefExt = L"json";
    f.lpstrTitle = title;
    f.Flags = OFN_NOCHANGEDIR | OFN_PATHMUSTEXIST | (save ? OFN_OVERWRITEPROMPT : OFN_FILEMUSTEXIST);
    if (save ? GetSaveFileNameW(&f) : GetOpenFileNameW(&f))
        return path;
    return {};
}
std::optional<Json> advancedProfileDialog(HWND owner, HFONT font, const Json &p, bool create) {
    WNDCLASSW c{};
    c.lpfnWndProc = editorProc;
    c.hInstance = GetModuleHandleW(nullptr);
    c.lpszClassName = L"NetGoProfileEditor";
    c.hCursor = LoadCursorW(nullptr, IDC_ARROW);
    c.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    RegisterClassW(&c);
    Editor data;
    auto h = CreateWindowExW(
        WS_EX_DLGMODALFRAME, c.lpszClassName, create ? L"新建服务器配置" : L"编辑服务器配置",
        WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU, 200, 120, 720, 590, owner, nullptr, c.hInstance, &data);
    HWND label = CreateWindowW(L"STATIC",
                               L"建议从管理员导入配置。高级编辑用于维护域名、直达 / 备用入口及公共 "
                               L"CA。\n导出仅包含连接信息；域名或 CA 改变时，请使用新的站点 ID。",
                               WS_CHILD | WS_VISIBLE, 20, 16, 670, 44, h, nullptr, c.hInstance, nullptr);
    SendMessageW(label, WM_SETFONT, reinterpret_cast<WPARAM>(font), TRUE);
    data.edit = CreateWindowExW(WS_EX_CLIENTEDGE, L"EDIT", wide(p.dump(2)).c_str(),
                                WS_CHILD | WS_VISIBLE | WS_VSCROLL | WS_HSCROLL | ES_MULTILINE |
                                    ES_AUTOVSCROLL | ES_AUTOHSCROLL,
                                20, 72, 670, 410, h, nullptr, c.hInstance, nullptr);
    SendMessageW(data.edit, WM_SETFONT, reinterpret_cast<WPARAM>(font), TRUE);
    SendMessageW(data.edit, EM_SETLIMITTEXT, maxJson, 0);
    for (auto pair : {std::pair{IDOK, L"保存配置"}, std::pair{IDCANCEL, L"取消"}}) {
        auto b = CreateWindowW(L"BUTTON", pair.second, WS_CHILD | WS_VISIBLE | WS_TABSTOP,
                               pair.first == IDOK ? 474 : 584, 498, 104, 34, h,
                               reinterpret_cast<HMENU>(intptr_t(pair.first)), c.hInstance, nullptr);
        SendMessageW(b, WM_SETFONT, reinterpret_cast<WPARAM>(font), TRUE);
    }
    EnableWindow(owner, FALSE);
    ShowWindow(h, SW_SHOW);
    MSG msg{};
    while (!data.done && GetMessageW(&msg, nullptr, 0, 0) > 0) {
        if (!IsDialogMessageW(h, &msg)) {
            TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
    }
    EnableWindow(owner, TRUE);
    SetForegroundWindow(owner);
    if (data.accepted) return data.value;
    return std::nullopt;
}
}
