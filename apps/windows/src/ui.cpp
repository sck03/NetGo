#include "common.hpp"
#include <commctrl.h>
#include <commdlg.h>
#include <shellapi.h>
#include <uxtheme.h>
#include <map>
#include <algorithm>
#include "ui.hpp"
#include "ui_worker.hpp"
#include "startup.hpp"

using namespace netgo;
namespace {
constexpr UINT resultMessage = WM_APP + 1, trayMessage = WM_APP + 2;
enum {
    Servers = 100,
    Alias,
    Password,
    Remember,
    Replace,
    Connect,
    Import,
    Export,
    Edit,
    Remove,
    Info,
    Resources,
    StatusText,
    Detail,
    Log,
    Tabs,
    ShowPassword,
    LogText,
    StatusDetail,
    RefreshLog,
    Internet,
    MenuNew = 200,
    MenuImport,
    MenuExport,
    MenuEdit,
    MenuDelete,
    MenuForget,
    MenuExit,
    MenuShow,
    MenuDisconnect,
    MenuAutoStart,
    MenuAutoConnect,
    MenuTray,
    MenuAbout,
    MenuAdvanced,
    MenuMain,
    MenuInfo,
    MenuResources,
    MenuMinimizeTray,
    MenuNetworkSetup,
    MenuRemote
};
HWND window = nullptr;
HFONT font = nullptr, titleFont = nullptr;
HBRUSH background = nullptr, sidebar = nullptr;
UINT dpi = 96;
int scale(int value) { return MulDiv(value, int(dpi), 96); }
void fonts() {
    HFONT old = font, oldTitle = titleFont;
    font =
        CreateFontW(-scale(13), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE, DEFAULT_CHARSET, OUT_DEFAULT_PRECIS,
                    CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH, L"Microsoft YaHei UI");
    titleFont = CreateFontW(-scale(16), 0, 0, 0, FW_SEMIBOLD, FALSE, FALSE, FALSE, DEFAULT_CHARSET,
                            OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY, DEFAULT_PITCH,
                            L"Microsoft YaHei UI");
    if (old)
        DeleteObject(old);
    if (oldTitle)
        DeleteObject(oldTitle);
}
std::map<int, HWND> controls;
Json sites = Json::array(), currentStatus = Json::object(), preferences = Json::object();
std::string chosen;
bool saved = false, exiting = false, firstLoad = true, pending = false, logsPending = false;
std::atomic_bool polling = false;
UINT taskbarCreated = 0;
std::string lastResources;
UiWorker pump;
std::wstring text(HWND h) {
    int n = GetWindowTextLengthW(h);
    std::wstring s(n + 1, L'\0');
    GetWindowTextW(h, s.data(), n + 1);
    s.resize(n);
    return s;
}
void submit(Json request) {
    const auto action = request.value("action", "");
    if (pump.submit(std::move(request))) return;
    if (action == "status") polling = false;
    if (action == "logs") logsPending = false;
    if (action == "disconnect") pending = false;
    if (action == "shutdown") { exiting = false; EnableWindow(window, TRUE); }
    throw Error("请求较多，请稍后重试");
}
void set(int id, const std::wstring &s) { SetWindowTextW(controls.at(id), s.c_str()); }
bool active() {
    auto state = currentStatus.value("state", "disconnected");
    return pending || (state != "disconnected" && state != "failed") ||
           currentStatus.value("error", "") == "engine_cleanup_failed";
}
void showTab(int page) {
    TabCtrl_SetCurSel(controls[Tabs], page);
    for (int id : std::initializer_list<int>{Servers, Alias, Password, Remember, Replace, Internet, Connect,
                                             Import, Edit, Detail, Log, ShowPassword, 400, 401, 402})
        ShowWindow(controls[id], page == 0 ? SW_SHOW : SW_HIDE);
    for (int id : std::initializer_list<int>{403, Info})
        ShowWindow(controls[id], page == 1 ? SW_SHOW : SW_HIDE);
    for (int id : std::initializer_list<int>{Resources, 404})
        ShowWindow(controls[id], page == 2 ? SW_SHOW : SW_HIDE);
    for (int id : std::initializer_list<int>{405, LogText, RefreshLog, 300, 301})
        ShowWindow(controls[id], page == 3 ? SW_SHOW : SW_HIDE);
    if (page == 3 && !logsPending) {
        logsPending = true;
        submit({{"action", "logs"}});
    }
}
void startPump() {
    pump.start(window, resultMessage);
}
std::filesystem::path fileDialog(bool save, const wchar_t *title) {
    wchar_t path[32768]{};
    OPENFILENAMEW f{};
    f.lStructSize = sizeof(f);
    f.hwndOwner = window;
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
const Json *selected() {
    for (const auto &p : sites)
        if (p.at("ID") == chosen)
            return &p;
    return nullptr;
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
            auto p = Json::parse(utf8(text(data->edit)));
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
void editProfile(bool create, bool advanced = false) {
    Json p =
        create
            ? Json{{"Protocol", 2},
                   {"ConfigRevision", 1},
                   {"ID", "site1"},
                   {"Name", "我的服务器"},
                   {"Origin", "https://control.example.com:10443"},
                   {"Candidates",
                    Json::array(
                        {{{"ID", "direct"}, {"Address", "control.example.com:10443"}, {"Kind", "direct"}}})},
                   {"GatewayPeerID", ""},
                   {"GatewayIP", "100.96.0.1"},
                   {"CA", ""},
                   {"LocalRelay", "relay.example.com"},
                   {"AuxiliaryRelay", ""}}
            : (selected() ? *selected() : Json());
    if (p.is_null())
        return;
    if (!advanced) {
        if (auto result = profileDialog(window, p, create)) {
            chosen = result->at("ID");
            submit({{"action", "import"}, {"profile", *result}});
        }
        return;
    }
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
        WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU, 200, 120, 720, 590, window, nullptr, c.hInstance, &data);
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
    EnableWindow(window, FALSE);
    ShowWindow(h, SW_SHOW);
    MSG msg{};
    while (!data.done && GetMessageW(&msg, nullptr, 0, 0) > 0) {
        if (!IsDialogMessageW(h, &msg)) {
            TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
    }
    EnableWindow(window, TRUE);
    SetForegroundWindow(window);
    if (data.accepted) {
        chosen = data.value.at("ID");
        submit({{"action", "import"}, {"profile", data.value}});
    }
}
void logsDialog(const Json &events) {
    std::wstring value;
    for (const auto &e : events) {
        std::time_t t = e.value("time", std::time_t(0));
        std::tm local{};
        localtime_s(&local, &t);
        wchar_t time[64];
        wcsftime(time, 64, L"%m-%d %H:%M:%S", &local);
        value += time;
        value += L"  " + stateText(e.value("state", ""));
        auto error = e.value("error", "");
        if (!error.empty())
            value += L" · " + errorText(error);
        value += L"\r\n";
    }
    if (value.empty())
        value = L"暂无连接记录。";
    set(LogText, value);
}
std::string unattendedSite() {
    try {
        return Json::parse(readFile(dataRoot() / L".remote-unattended", 4096)).value("site", "");
    } catch (...) {
        return {};
    }
}
void launchUnattended() {
    auto site = unattendedSite();
    if (site.empty() || site != currentStatus.value("site_id", "") ||
        FindWindowW(L"NetGoRemoteWindow", nullptr))
        return;
    auto binary = executableDir() / L"NetGo.exe";
    auto args = L"--remote --site " + wide(site) + L" --host";
    ShellExecuteW(window, L"open", binary.c_str(), args.c_str(), executableDir().c_str(), SW_SHOWMINIMIZED);
}
void persist() {
    secureDirectory(dataRoot());
    atomicFile(dataRoot() / L".preferences", preferences.dump(2));
}
void tray(bool add) {
    NOTIFYICONDATAW n{};
    n.cbSize = sizeof(n);
    n.hWnd = window;
    n.uID = 1;
    n.uFlags = NIF_ICON | NIF_MESSAGE | NIF_TIP;
    n.uCallbackMessage = trayMessage;
    n.hIcon = LoadIconW(GetModuleHandleW(nullptr), MAKEINTRESOURCEW(101));
    std::wstring tip = L"NetGo · " + stateText(currentStatus.value("state", "disconnected"));
    wcsncpy_s(n.szTip, tip.c_str(), _TRUNCATE);
    Shell_NotifyIconW(add ? NIM_ADD : NIM_MODIFY, &n);
}
void refreshSelection() {
    const auto *p = selected();
    set(Detail, p ? wide(p->value("Origin", "")) : L"请先导入管理员提供的服务器配置。");
    SendMessageW(controls[Servers], CB_SETCURSEL, -1, 0);
    for (size_t i = 0; i < sites.size(); ++i)
        if (sites[i]["ID"] == chosen)
            SendMessageW(controls[Servers], CB_SETCURSEL, i, 0);
    set(Password, L"");
    saved = false;
    set(Alias, L"");
    SendMessageW(controls[Remember], BM_SETCHECK, BST_UNCHECKED, 0);
    SendMessageW(controls[ShowPassword], BM_SETCHECK, BST_UNCHECKED, 0);
    SendMessageW(controls[Password], EM_SETPASSWORDCHAR, L'●', 0);
    if (p)
        submit({{"action", "credential-info"}, {"site", chosen}});
    for (int id : {Connect, Edit, Alias, Password, Remember, Replace, Internet, ShowPassword})
        EnableWindow(controls[id], p != nullptr && !active());
    preferences["selected"] = chosen;
    persist();
}
void drawStatus(Json status) {
    currentStatus = std::move(status);
    auto s = currentStatus.value("state", "disconnected");
    set(StatusText, stateText(s));
    bool busy = active();
    set(Connect, busy ? (s == "connected" || s == "recovering" ? L"断开连接" : L"取消连接") : L"连接");
    EnableWindow(controls[Connect], !pending && (busy || selected() != nullptr));
    EnableWindow(controls[Servers], !busy);
    EnableWindow(controls[Import], !busy);
    for (int id : {Alias, Password, Remember, Replace, Internet, Edit, ShowPassword})
        EnableWindow(controls[id], !busy && selected() != nullptr);
    auto d = currentStatus.value("data", Json::object());
    auto path = d.value("path", "negotiating");
    std::wstring route = path == "direct"            ? L"直连"
                         : path == "local_relay"     ? L"本地中继"
                         : path == "auxiliary_relay" ? L"VPS 中继"
                         : path == "relay_unknown"   ? L"中继（待识别）"
                                                     : L"等待连接";
    auto control = currentStatus.value("control", "");
    std::wstring entrance = control == "direct"      ? L"服务器直达入口"
                            : control == "auxiliary" ? L"VPS 备用入口"
                                                     : L"等待探测";
    auto candidate = currentStatus.value("control_candidate", "");
    if (!candidate.empty())
        entrance += L"（" + wide(candidate) + L"）";
    std::wstring details = L"连接状态：" + stateText(s) + L"\r\n\r\n控制入口：" + entrance +
                           L"\r\n\r\n数据路径：" + route + L"\r\n\r\n虚拟地址：" + wide(d.value("ip", "—")) +
                           L"\r\n\r\n接收 / 发送：" + std::to_wstring(d.value("rx", int64_t(0)) / 1024) +
                           L" / " + std::to_wstring(d.value("tx", int64_t(0)) / 1024) + L" KB";
    auto error = currentStatus.value("error", "");
    if (!error.empty())
        details += L"\r\n\r\n" + errorText(error);
    set(Info, details);
    set(StatusDetail, !error.empty() ? errorText(error)
                                     : (s == "connected" ? route + L" · 仅转发获准的局域网流量"
                                        : selected()     ? L"选择服务器，填写账号和密码后连接。"
                                                         : L"从“导入”或“配置”菜单添加服务器。"));
    auto lease = currentStatus.value("lease", Json::object());
    int row = 0;
    auto resources = lease.value("resources", Json::array());
    auto serialized = resources.dump();
    if (serialized != lastResources) {
        lastResources = serialized;
        ListView_DeleteAllItems(controls[Resources]);
        for (const auto &r : resources) {
            std::wstring name = wide(r.value("name", ""));
            LVITEMW item{};
            item.mask = LVIF_TEXT;
            item.iItem = row;
            item.pszText = name.data();
            ListView_InsertItem(controls[Resources], &item);
            std::wstring cidr = wide(r.value("cidr", "")), protocol = wide(r.value("protocol", ""));
            for (const auto &port : r.value("ports", Json::array()))
                protocol += L" " + wide(port.dump());
            ListView_SetItemText(controls[Resources], row, 1, cidr.data());
            ListView_SetItemText(controls[Resources], row, 2, protocol.data());
            ++row;
        }
    }
    tray(false);
}
void command(int id) {
    if ((id == MenuNew || id == Import || id == MenuImport || id == Edit || id == MenuEdit ||
         id == MenuAdvanced || id == Remove || id == MenuDelete || id == MenuForget) &&
        active())
        throw Error("disconnect_before_edit");
    switch (id) {
    case MenuShow:
        ShowWindow(window, SW_RESTORE);
        SetForegroundWindow(window);
        break;
    case IDOK:
    case Connect: {
        if (pending)
            break;
        if (active()) {
            pending = true;
            submit({{"action", "disconnect"}});
            drawStatus(currentStatus);
            break;
        }
        if (!selected())
            break;
        if (std::filesystem::is_regular_file(executableDir() / L"netgo-portable.json") &&
            !WaitNamedPipeW(L"\\\\.\\pipe\\netgo-netbird", 100)) {
            command(MenuNetworkSetup);
            break;
        }
        if (text(controls[Alias]).empty() || (!saved && text(controls[Password]).empty()))
            throw Error("credentials_required");
        std::string password = utf8(text(controls[Password]));
        submit({{"action", "connect"},
                {"site", chosen},
                {"alias", utf8(text(controls[Alias]))},
                {"password", password},
                {"internet", SendMessageW(controls[Internet], BM_GETCHECK, 0, 0) == BST_CHECKED},
                {"remember", SendMessageW(controls[Remember], BM_GETCHECK, 0, 0) == BST_CHECKED},
                {"replace", SendMessageW(controls[Replace], BM_GETCHECK, 0, 0) == BST_CHECKED}});
        pending = true;
        wipe(password);
        set(Password, L"");
        drawStatus(currentStatus);
        preferences["selected"] = chosen;
        persist();
        break;
    }
    case MenuDisconnect:
        if (!pending) {
            pending = true;
            submit({{"action", "disconnect"}});
            drawStatus(currentStatus);
        }
        break;
    case ShowPassword:
        SendMessageW(controls[Password], EM_SETPASSWORDCHAR,
                     SendMessageW(controls[ShowPassword], BM_GETCHECK, 0, 0) == BST_CHECKED ? 0 : L'●', 0);
        InvalidateRect(controls[Password], nullptr, TRUE);
        break;
    case Import:
    case MenuImport: {
        auto file = fileDialog(false, L"导入管理员提供的站点");
        if (!file.empty()) {
            auto p = Json::parse(readFile(file));
            validateProfile(p);
            bool trust = false;
            for (const auto &old : sites)
                if (old.at("ID") == p.at("ID") &&
                    (originHost(old.at("Origin")) != originHost(p.at("Origin")) ||
                     old.value("CA", "") != p.value("CA", ""))) {
                    auto warning = L"管理员提供的文件更改了本站点的地址或证书。\n\n原地址：" +
                                   wide(old.at("Origin")) + L"\n新地址：" + wide(p.at("Origin")) +
                                   L"\n证书摘要：" + wide(sha256(p.value("CA", "")).substr(0, 16)) +
                                   L"\n\n确认此文件来自你的服务器管理员后继续。";
                    if (MessageBoxW(window, warning.c_str(), L"确认新的站点身份",
                                    MB_YESNO | MB_ICONQUESTION) != IDYES)
                        return;
                    trust = true;
                }
            chosen = p["ID"];
            submit({{"action", "import"}, {"profile", p}, {"replace_trust", trust}});
        }
        break;
    }
    case Export:
    case MenuExport: {
        if (!selected())
            break;
        auto file = fileDialog(true, L"导出公共站点配置");
        if (!file.empty())
            atomicFile(file, selected()->dump(2));
        break;
    }
    case Edit:
    case MenuEdit:
        editProfile(false);
        break;
    case MenuNew:
        editProfile(true);
        break;
    case MenuAdvanced:
        editProfile(false, true);
        break;
    case Remove:
    case MenuDelete:
        if (!selected())
            break;
        if (MessageBoxW(window, L"删除此服务器配置及本用户保存的密码、设备密钥？", L"删除服务器",
                        MB_YESNO | MB_ICONQUESTION) == IDYES)
            submit({{"action", "delete"}, {"site", chosen}});
        break;
    case MenuForget:
        if (selected())
            submit({{"action", "forget"}, {"site", chosen}});
        break;
    case Log:
        showTab(3);
        break;
    case RefreshLog:
        if (!logsPending) {
            logsPending = true;
            submit({{"action", "logs"}});
        }
        break;
    case MenuMain:
        showTab(0);
        break;
    case MenuInfo:
        showTab(1);
        break;
    case MenuResources:
        showTab(2);
        break;
    case MenuRemote: {
        if (!currentStatus.value("remote_ready", false) || chosen != currentStatus.value("site_id", ""))
            throw Error("remote_site_disconnected");
        auto binary = executableDir() / L"NetGo.exe";
        auto parameters = L"--remote --site " + wide(chosen);
        if (reinterpret_cast<INT_PTR>(ShellExecuteW(window, L"open", binary.c_str(), parameters.c_str(),
                                                    executableDir().c_str(), SW_SHOWNORMAL)) <= 32)
            throw Error("remote_program_missing");
        break;
    }
    case MenuAutoStart: {
        bool enabled = !preferences.value("startup", false);
        startup(enabled);
        preferences["startup"] = enabled;
        persist();
        CheckMenuItem(GetMenu(window), MenuAutoStart, MF_BYCOMMAND | (enabled ? MF_CHECKED : MF_UNCHECKED));
        break;
    }
    case MenuAutoConnect: {
        bool enabled = !(preferences.value("auto_connect", false) &&
                         preferences.value("auto_connect_site", "") == chosen);
        if (enabled && (!selected() || !saved))
            throw Error("auto_connect_requires_saved_password");
        preferences["auto_connect"] = enabled;
        preferences["auto_connect_site"] = enabled ? chosen : "";
        persist();
        break;
    }
    case MenuTray: {
        bool enabled = !preferences.value("close_to_tray", true);
        preferences["close_to_tray"] = enabled;
        persist();
        CheckMenuItem(GetMenu(window), MenuTray, MF_BYCOMMAND | (enabled ? MF_CHECKED : MF_UNCHECKED));
        break;
    }
    case MenuMinimizeTray: {
        preferences["minimize_to_tray"] = !preferences.value("minimize_to_tray", false);
        persist();
        break;
    }
    case MenuAbout:
        MessageBoxW(
            window,
            (L"NetGo " + wide(appVersion) +
             L"\n原生 C++ / Win32 "
             L"客户端\n网络核心：NetBird\n登录入口和数据路径分别显示。\n\n诊断导出不包含密码、令牌或密钥。")
                .c_str(),
            L"关于 NetGo", MB_OK);
        break;
    case MenuNetworkSetup: {
        if (active())
            throw Error("disconnect_before_edit");
        auto setup = executableDir() / L".netgo/NetGo-Network-Setup.exe";
        if (!std::filesystem::is_regular_file(setup))
            throw Error("network_setup_missing");
        if (reinterpret_cast<INT_PTR>(ShellExecuteW(window, L"open", setup.c_str(), nullptr,
                                                    executableDir().c_str(), SW_SHOWNORMAL)) <= 32)
            throw Error("network_setup_missing");
        break;
    }
    case MenuExit:
        exiting = true;
        EnableWindow(window, FALSE);
        submit({{"action", "shutdown"}});
        break;
    default:
        if (id == 300) {
            submit({{"action", "diagnostic"}});
        } else if (id == 301)
            submit({{"action", "clear-logs"}});
        break;
    }
}
HWND control(const wchar_t *cls, const wchar_t *caption, int id, DWORD style = 0, DWORD extra = 0) {
    auto h = CreateWindowExW(extra, cls, caption, WS_CHILD | WS_VISIBLE | style, 0, 0, 10, 10, window,
                             reinterpret_cast<HMENU>(intptr_t(id)), GetModuleHandleW(nullptr), nullptr);
    SendMessageW(h, WM_SETFONT, reinterpret_cast<WPARAM>(font), TRUE);
    controls[id] = h;
    return h;
}
void layout() {
    RECT r{};
    GetClientRect(window, &r);
    int width = MulDiv(r.right, 96, int(dpi)), height = MulDiv(r.bottom, 96, int(dpi));
    auto move = [](int id, int x, int y, int w, int h) {
        MoveWindow(controls[id], scale(x), scale(y), scale(w), scale(h), TRUE);
    };
    move(Tabs, 12, 10, width - 24, height - 88);
    move(400, 28, 55, 58, 24);
    move(Servers, 90, 50, width - 242, 220);
    move(Edit, width - 144, 50, 54, 26);
    move(Import, width - 82, 50, 54, 26);
    move(401, 28, 100, 58, 24);
    move(Alias, 90, 95, width - 122, 26);
    move(402, 28, 141, 58, 24);
    move(Password, 90, 136, width - 224, 26);
    move(ShowPassword, width - 124, 139, 100, 23);
    move(Remember, 90, 176, 158, 24);
    move(Replace, 254, 176, width - 284, 24);
    move(Internet, 90, 205, width - 122, 24);
    move(Connect, 90, 238, 184, 32);
    move(Log, 284, 238, 110, 32);
    move(Detail, 90, 278, width - 120, 30);
    move(403, 28, 46, width - 56, 22);
    move(Info, 28, 72, width - 56, height - 166);
    move(Resources, 28, 48, width - 56, height - 176);
    move(404, 28, height - 120, width - 56, 24);
    move(405, 28, 46, width - 56, 22);
    move(LogText, 28, 72, width - 56, height - 208);
    move(RefreshLog, 28, height - 126, 82, 28);
    move(301, 120, height - 126, 90, 28);
    move(300, width - 144, height - 126, 116, 28);
    move(StatusText, 16, height - 69, width - 32, 24);
    move(StatusDetail, 16, height - 40, width - 32, 34);
    ListView_SetColumnWidth(controls[Resources], 0, scale(std::max(110, (width - 70) / 3)));
    ListView_SetColumnWidth(controls[Resources], 1, scale(150));
    ListView_SetColumnWidth(controls[Resources], 2,
                            scale(std::max(110, width - 70 - 150 - (width - 70) / 3)));
}
void createUI() {
    dpi = GetDpiForWindow(window);
    fonts();
    background = CreateSolidBrush(GetSysColor(COLOR_BTNFACE));
    sidebar = CreateSolidBrush(GetSysColor(COLOR_WINDOW));
    HMENU menu = CreateMenu(), config = CreatePopupMenu(), view = CreatePopupMenu(),
          options = CreatePopupMenu();
    for (auto p : {std::pair{MenuNew, L"新建服务器…\tCtrl+N"}, std::pair{MenuImport, L"导入站点…\tCtrl+O"},
                   std::pair{MenuExport, L"导出配置…"}, std::pair{MenuEdit, L"编辑服务器…\tCtrl+E"},
                   std::pair{MenuAdvanced, L"高级配置…"}, std::pair{MenuDelete, L"删除服务器"},
                   std::pair{MenuForget, L"忘记保存的密码"}, std::pair{MenuExit, L"退出\tCtrl+Q"}})
        AppendMenuW(config, MF_STRING, p.first, p.second);
    AppendMenuW(view, MF_STRING, MenuMain, L"连接");
    AppendMenuW(view, MF_STRING, MenuInfo, L"VPN 信息");
    AppendMenuW(view, MF_STRING, MenuResources, L"可访问资源");
    AppendMenuW(view, MF_STRING, Log, L"查看日志\tCtrl+L");
    AppendMenuW(view, MF_STRING, 300, L"导出诊断…");
    AppendMenuW(view, MF_STRING, MenuRemote, L"远程桌面…");
    AppendMenuW(options, MF_STRING | (preferences.value("startup", false) ? MF_CHECKED : 0), MenuAutoStart,
                L"登录 Windows 后启动");
    AppendMenuW(options, MF_STRING | (preferences.value("auto_connect", false) ? MF_CHECKED : 0),
                MenuAutoConnect, L"启动后使用已保存凭据连接");
    AppendMenuW(options, MF_STRING | (preferences.value("close_to_tray", true) ? MF_CHECKED : 0), MenuTray,
                L"关闭窗口时保留托盘");
    AppendMenuW(options, MF_STRING, MenuMinimizeTray, L"最小化到托盘");
    if (std::filesystem::is_regular_file(executableDir() / L"netgo-portable.json"))
        AppendMenuW(options, MF_STRING, MenuNetworkSetup, L"准备网络服务…");
    AppendMenuW(menu, MF_POPUP, reinterpret_cast<UINT_PTR>(config), L"配置");
    AppendMenuW(menu, MF_POPUP, reinterpret_cast<UINT_PTR>(view), L"查看");
    AppendMenuW(menu, MF_POPUP, reinterpret_cast<UINT_PTR>(options), L"选项");
    AppendMenuW(menu, MF_STRING, MenuAbout, L"关于");
    SetMenu(window, menu);
    control(WC_TABCONTROLW, L"", Tabs, WS_TABSTOP | WS_CLIPSIBLINGS);
    int page = 0;
    for (auto caption : {L"连接", L"VPN 信息", L"可访问资源", L"日志"}) {
        TCITEMW item{};
        item.mask = TCIF_TEXT;
        item.pszText = const_cast<wchar_t *>(caption);
        TabCtrl_InsertItem(controls[Tabs], page++, &item);
    }
    control(WC_COMBOBOXW, L"", Servers, WS_TABSTOP | WS_VSCROLL | CBS_DROPDOWNLIST);
    const std::pair<int, const wchar_t *> buttons[] = {{Import, L"导入"},   {Edit, L"编辑"},
                                                       {Log, L"查看日志"},  {RefreshLog, L"刷新"},
                                                       {300, L"导出诊断…"}, {301, L"清空日志"}};
    for (auto p : buttons)
        control(L"BUTTON", p.second, p.first, WS_TABSTOP);
    control(L"STATIC", L"未连接", StatusText);
    SendMessageW(controls[StatusText], WM_SETFONT, reinterpret_cast<WPARAM>(titleFont), TRUE);
    control(L"STATIC", L"导入站点后即可连接。", StatusDetail);
    control(L"STATIC", L"导入管理员提供的服务器配置，即可开始连接", Detail);
    control(L"STATIC", L"服务器：", 400);
    control(L"STATIC", L"账号", 401);
    control(L"EDIT", L"", Alias, WS_TABSTOP | ES_AUTOHSCROLL, WS_EX_CLIENTEDGE);
    control(L"STATIC", L"密码", 402);
    control(L"EDIT", L"", Password, WS_TABSTOP | ES_AUTOHSCROLL | ES_PASSWORD, WS_EX_CLIENTEDGE);
    SendMessageW(controls[Alias], EM_SETLIMITTEXT, 256, 0);
    SendMessageW(controls[Password], EM_SETLIMITTEXT, 2048, 0);
    SendMessageW(controls[Password], EM_SETCUEBANNER, 0,
                 reinterpret_cast<LPARAM>(L"输入密码，已记住时可留空"));
    control(L"BUTTON", L"显示密码", ShowPassword, WS_TABSTOP | BS_AUTOCHECKBOX);
    control(L"BUTTON", L"记住密码", Remember, WS_TABSTOP | BS_AUTOCHECKBOX);
    control(L"BUTTON", L"替换旧连接", Replace, WS_TABSTOP | BS_AUTOCHECKBOX);
    control(L"BUTTON", L"通过旁路由访问互联网（需要账号授权）", Internet, WS_TABSTOP | BS_AUTOCHECKBOX);
    control(L"BUTTON", L"连接", Connect, WS_TABSTOP | BS_DEFPUSHBUTTON);
    control(L"STATIC", L"VPN 信息", 403);
    control(L"EDIT", L"尚未连接。", Info,
            WS_TABSTOP | WS_VSCROLL | ES_MULTILINE | ES_READONLY | ES_AUTOVSCROLL, WS_EX_CLIENTEDGE);
    control(L"STATIC", L"连接后显示服务器授予的地址和端口。", 404);
    control(WC_LISTVIEWW, L"", Resources, WS_TABSTOP | LVS_REPORT | LVS_SINGLESEL, WS_EX_CLIENTEDGE);
    control(L"STATIC", L"连接日志", 405);
    control(L"EDIT", L"暂无连接记录。", LogText,
            WS_TABSTOP | WS_VSCROLL | ES_MULTILINE | ES_READONLY | ES_AUTOVSCROLL, WS_EX_CLIENTEDGE);
    ListView_SetExtendedListViewStyle(controls[Resources], LVS_EX_FULLROWSELECT | LVS_EX_DOUBLEBUFFER);
    int i = 0;
    for (auto p : {std::pair{L"资源", 150}, std::pair{L"地址", 150}, std::pair{L"协议 / 端口", 150}}) {
        LVCOLUMNW col{};
        col.mask = LVCF_TEXT | LVCF_WIDTH;
        col.cx = p.second;
        col.pszText = const_cast<wchar_t *>(p.first);
        ListView_InsertColumn(controls[Resources], i++, &col);
    }
    layout();
    refreshSelection();
    showTab(0);
    tray(true);
    taskbarCreated = RegisterWindowMessageW(L"TaskbarCreated");
}
LRESULT CALLBACK proc(HWND h, UINT m, WPARAM w, LPARAM l) {
    try {
        if (taskbarCreated && m == taskbarCreated) {
            tray(true);
            return 0;
        }
        switch (m) {
        case WM_CREATE:
            window = h;
            createUI();
            return 0;
        case WM_SIZE:
            if (w == SIZE_MINIMIZED && preferences.value("minimize_to_tray", false))
                ShowWindow(h, SW_HIDE);
            else if (controls.contains(StatusDetail) && controls.contains(Resources))
                layout();
            return 0;
        case WM_GETMINMAXINFO: {
            RECT r{0, 0, scale(500), scale(410)};
            AdjustWindowRectExForDpi(&r, WS_OVERLAPPEDWINDOW, TRUE, 0, dpi);
            auto info = reinterpret_cast<MINMAXINFO *>(l);
            info->ptMinTrackSize = {r.right - r.left, r.bottom - r.top};
            return 0;
        }
        case WM_DPICHANGED: {
            dpi = HIWORD(w);
            fonts();
            for (const auto &[id, control] : controls)
                SendMessageW(control, WM_SETFONT,
                             reinterpret_cast<WPARAM>(id == StatusText ? titleFont : font), TRUE);
            auto rect = reinterpret_cast<RECT *>(l);
            SetWindowPos(h, nullptr, rect->left, rect->top, rect->right - rect->left,
                         rect->bottom - rect->top, SWP_NOZORDER | SWP_NOACTIVATE);
            layout();
            return 0;
        }
        case WM_CTLCOLORSTATIC:
        case WM_CTLCOLOREDIT: {
            auto child = reinterpret_cast<HWND>(l);
            bool edit = m == WM_CTLCOLOREDIT || child == controls[Info] || child == controls[LogText];
            SetTextColor(reinterpret_cast<HDC>(w), GetSysColor(COLOR_WINDOWTEXT));
            SetBkColor(reinterpret_cast<HDC>(w), GetSysColor(edit ? COLOR_WINDOW : COLOR_BTNFACE));
            return reinterpret_cast<LRESULT>(edit ? sidebar : background);
        }
        case WM_ERASEBKGND: {
            RECT r{};
            GetClientRect(h, &r);
            FillRect(reinterpret_cast<HDC>(w), &r, background);
            return TRUE;
        }
        case WM_DRAWITEM: {
            auto item = reinterpret_cast<DRAWITEMSTRUCT *>(l);
            if (item->CtlID == Connect) {
                HBRUSH b = CreateSolidBrush((item->itemState & ODS_DISABLED) ? RGB(160, 177, 180)
                                                                             : RGB(0, 120, 135));
                FillRect(item->hDC, &item->rcItem, b);
                DeleteObject(b);
                SetBkMode(item->hDC, TRANSPARENT);
                SetTextColor(item->hDC, RGB(255, 255, 255));
                SelectObject(item->hDC, font);
                auto caption = text(item->hwndItem);
                DrawTextW(item->hDC, caption.c_str(), -1, &item->rcItem,
                          DT_CENTER | DT_VCENTER | DT_SINGLELINE);
                if (item->itemState & ODS_FOCUS)
                    DrawFocusRect(item->hDC, &item->rcItem);
                return TRUE;
            }
            break;
        }
        case WM_COMMAND:
            if (LOWORD(w) == Servers && HIWORD(w) == CBN_SELCHANGE) {
                auto index = SendMessageW(controls[Servers], CB_GETCURSEL, 0, 0);
                if (index >= 0 && size_t(index) < sites.size()) {
                    chosen = sites[size_t(index)].at("ID");
                    refreshSelection();
                }
                return 0;
            }
            command(LOWORD(w));
            return 0;
        case WM_NOTIFY:
            if (reinterpret_cast<NMHDR *>(l)->idFrom == Tabs &&
                reinterpret_cast<NMHDR *>(l)->code == TCN_SELCHANGE) {
                showTab(TabCtrl_GetCurSel(controls[Tabs]));
                return 0;
            }
            break;
        case WM_INITMENUPOPUP: {
            auto menu = GetMenu(h);
            for (int id : {MenuNew, MenuImport, MenuEdit, MenuAdvanced, MenuDelete, MenuForget})
                EnableMenuItem(menu, id,
                               MF_BYCOMMAND |
                                   (!active() && ((id == MenuNew || id == MenuImport) || selected())
                                        ? MF_ENABLED
                                        : MF_GRAYED));
            EnableMenuItem(menu, MenuExport, MF_BYCOMMAND | (selected() ? MF_ENABLED : MF_GRAYED));
            CheckMenuItem(menu, MenuAutoConnect,
                          MF_BYCOMMAND | ((preferences.value("auto_connect", false) &&
                                           preferences.value("auto_connect_site", "") == chosen)
                                              ? MF_CHECKED
                                              : MF_UNCHECKED));
            CheckMenuItem(menu, MenuMinimizeTray,
                          MF_BYCOMMAND |
                              (preferences.value("minimize_to_tray", false) ? MF_CHECKED : MF_UNCHECKED));
            break;
        }
        case DM_GETDEFID:
            return MAKELRESULT(Connect, DC_HASDEFID);
        case WM_TIMER:
            if (!polling.exchange(true))
                submit({{"action", "status"}});
            if (TabCtrl_GetCurSel(controls[Tabs]) == 3 && !logsPending) {
                logsPending = true;
                submit({{"action", "logs"}});
            }
            return 0;
        case WM_POWERBROADCAST:
            if (w == PBT_APMRESUMEAUTOMATIC || w == PBT_APMRESUMESUSPEND) {
                if (!polling.exchange(true))
                    submit({{"action", "status"}});
            }
            return TRUE;
        case trayMessage:
            if (l == WM_LBUTTONDBLCLK) {
                command(MenuShow);
                return 0;
            }
            if (l == WM_RBUTTONUP) {
                auto menu = CreatePopupMenu();
                AppendMenuW(menu, MF_STRING, MenuShow, L"打开 NetGo");
                AppendMenuW(menu, MF_STRING, MenuDisconnect, L"断开连接");
                AppendMenuW(menu, MF_STRING, MenuExit, L"退出");
                POINT p{};
                GetCursorPos(&p);
                SetForegroundWindow(window);
                int id = TrackPopupMenu(menu, TPM_RETURNCMD | TPM_RIGHTBUTTON, p.x, p.y, 0, window, nullptr);
                DestroyMenu(menu);
                if (id)
                    command(id);
                return 0;
            }
            break;
        case resultMessage: {
            auto r = pump.takeResult();
            if (!r) return 0;
            if (r->action == "status")
                polling = false;
            if (r->action == "logs")
                logsPending = false;
            if (r->action == "connect" || r->action == "disconnect")
                pending = false;
            if (exiting && r->action == "shutdown") {
                if (!r->error.empty()) {
                    exiting = false;
                    EnableWindow(h, TRUE);
                    MessageBoxW(h, errorText(r->error).c_str(), L"NetGo", MB_ICONWARNING);
                    return 0;
                }
                DestroyWindow(h);
                return 0;
            }
            if (!r->error.empty()) {
                if (r->action == "status") {
                    set(StatusText, L"后台不可用");
                    set(Info, errorText(r->error));
                    set(StatusDetail, errorText(r->error));
                } else {
                    drawStatus(currentStatus);
                    MessageBoxW(h, errorText(r->error).c_str(), L"NetGo", MB_ICONWARNING);
                }
                return 0;
            }
            if (r->action == "profiles") {
                sites = r->value;
                if (!selected())
                    chosen = sites.empty() ? "" : sites[0].at("ID").get<std::string>();
                SendMessageW(controls[Servers], CB_RESETCONTENT, 0, 0);
                for (const auto &p : sites)
                    SendMessageW(controls[Servers], CB_ADDSTRING, 0,
                                 reinterpret_cast<LPARAM>(wide(p.value("Name", "")).c_str()));
                refreshSelection();
            } else if (r->action == "import" || r->action == "delete") {
                submit({{"action", "profiles"}});
            } else if (r->action == "credential-info") {
                if (r->site != chosen)
                    return 0;
                saved = r->value.value("remembered", false);
                if (text(controls[Alias]).empty())
                    set(Alias, wide(r->value.value("alias", "")));
                SendMessageW(controls[Remember], BM_SETCHECK, saved ? BST_CHECKED : BST_UNCHECKED, 0);
                if (firstLoad) {
                    firstLoad = false;
                    if (saved && preferences.value("auto_connect", false) &&
                        preferences.value("auto_connect_site", "") == chosen && !active())
                        command(Connect);
                }
            } else if (r->action == "forget") {
                saved = false;
                refreshSelection();
            } else if (r->action == "status" || r->action == "connect" || r->action == "disconnect") {
                bool wasConnected = currentStatus.value("state", "") == "connected";
                drawStatus(r->value);
                if (!wasConnected && currentStatus.value("state", "") == "connected") {
                    submit({{"action", "credential-info"}, {"site", chosen}});
                    launchUnattended();
                }
            } else if (r->action == "logs")
                logsDialog(r->value);
            else if (r->action == "clear-logs") {
                logsPending = true;
                submit({{"action", "logs"}});
            } else if (r->action == "diagnostic") {
                auto path = fileDialog(true, L"导出脱敏诊断");
                if (!path.empty())
                    atomicFile(path, r->value.dump(2));
            }
            return 0;
        }
        case WM_CLOSE:
            if (preferences.value("close_to_tray", true)) {
                ShowWindow(h, SW_HIDE);
                return 0;
            }
            command(MenuExit);
            return 0;
        case WM_DESTROY: {
            KillTimer(h, 1);
            NOTIFYICONDATAW n{};
            n.cbSize = sizeof(n);
            n.hWnd = h;
            n.uID = 1;
            Shell_NotifyIconW(NIM_DELETE, &n);
            PostQuitMessage(0);
            return 0;
        }
        }
    } catch (const std::exception &e) {
        MessageBoxW(h, errorText(e.what()).c_str(), L"NetGo", MB_ICONWARNING);
    }
    return DefWindowProcW(h, m, w, l);
}
void launchAgent() {
    try {
        pipeCall({{"action", "status"}});
        return;
    } catch (...) {
    }
    auto file = executableDir() / L"NetGo.exe";
    std::wstring command = L"\"" + file.wstring() + L"\" --agent";
    STARTUPINFOW start{};
    start.cb = sizeof(start);
    PROCESS_INFORMATION p{};
    if (!CreateProcessW(file.c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW, nullptr,
                        file.parent_path().c_str(), &start, &p))
        throw Error("agent_unavailable");
    CloseHandle(p.hThread);
    CloseHandle(p.hProcess);
}
} // namespace
int desktopMain(HINSTANCE instance, PWSTR args, int show) {
    try {
        Handle mutex(CreateMutexW(nullptr, TRUE, wide("Local\\NetGoNativeUI-" + sid()).c_str()));
        if (GetLastError() == ERROR_ALREADY_EXISTS) {
            auto h = FindWindowW(L"NetGoNativeWindow", nullptr);
            if (h) {
                ShowWindow(h, SW_RESTORE);
                SetForegroundWindow(h);
            }
            return 0;
        }
        INITCOMMONCONTROLSEX common{sizeof(common),
                                    ICC_LISTVIEW_CLASSES | ICC_TAB_CLASSES | ICC_STANDARD_CLASSES};
        InitCommonControlsEx(&common);
        WSADATA data{};
        WSAStartup(MAKEWORD(2, 2), &data);
        try {
            preferences = Json::parse(readFile(dataRoot() / L".preferences", 8192));
        } catch (...) {
            preferences = Json::object();
        }
        chosen = preferences.value("selected", "");
        auto remoteSite = unattendedSite();
        if (!remoteSite.empty()) {
            preferences["auto_connect"] = true;
            preferences["auto_connect_site"] = remoteSite;
        }
        if (preferences.value("auto_connect", false)) {
            if (!preferences.contains("auto_connect_site"))
                preferences["auto_connect_site"] = chosen;
            chosen = preferences.value("auto_connect_site", chosen);
        }
        launchAgent();
        WNDCLASSW c{};
        c.hInstance = instance;
        c.lpfnWndProc = proc;
        c.lpszClassName = L"NetGoNativeWindow";
        c.hCursor = LoadCursorW(nullptr, IDC_ARROW);
        c.hIcon = LoadIconW(instance, MAKEINTRESOURCEW(101));
        RegisterClassW(&c);
        dpi = GetDpiForSystem();
        RECT rect{0, 0, scale(520), scale(370)}, work{};
        AdjustWindowRectExForDpi(&rect, WS_OVERLAPPEDWINDOW, TRUE, 0, dpi);
        SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
        int width = rect.right - rect.left, height = rect.bottom - rect.top;
        window =
            CreateWindowExW(WS_EX_CONTROLPARENT, c.lpszClassName, L"NetGo · 局域网远程接入",
                            WS_OVERLAPPEDWINDOW, std::max(work.left, (work.right + work.left - width) / 2),
                            std::max(work.top, (work.bottom + work.top - height) / 2), width, height, nullptr,
                            nullptr, instance, nullptr);
        if (!window)
            throw Error("window_failed");
        startPump();
        submit({{"action", "profiles"}});
        SetTimer(window, 1, 2000, nullptr);
        if (std::wstring(args) != L"--tray")
            ShowWindow(window, show);
        UpdateWindow(window);
        ACCEL shortcuts[] = {{FVIRTKEY | FCONTROL, 'N', MenuNew},
                             {FVIRTKEY | FCONTROL, 'O', MenuImport},
                             {FVIRTKEY | FCONTROL, 'E', MenuEdit},
                             {FVIRTKEY | FCONTROL, 'L', Log},
                             {FVIRTKEY | FCONTROL, 'Q', MenuExit}};
        auto accelerators = CreateAcceleratorTableW(shortcuts, int(std::size(shortcuts)));
        MSG msg{};
        while (GetMessageW(&msg, nullptr, 0, 0) > 0) {
            if (!TranslateAcceleratorW(window, accelerators, &msg) && !IsDialogMessageW(window, &msg)) {
                TranslateMessage(&msg);
                DispatchMessageW(&msg);
            }
        }
        DestroyAcceleratorTable(accelerators);
        pump.stop();
        DeleteObject(font);
        DeleteObject(titleFont);
        DeleteObject(background);
        DeleteObject(sidebar);
        WSACleanup();
        return 0;
    } catch (const std::exception &e) {
        MessageBoxW(nullptr, errorText(e.what()).c_str(), L"NetGo", MB_ICONERROR);
        return 1;
    }
}
