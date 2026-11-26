#include "ui.hpp"
#include <map>

namespace netgo {
std::wstring errorText(const std::string &e) {
    static const std::map<std::string, std::wstring> messages = {
        {"agent_unavailable", L"连接后台未启动。请重新打开客户端，或重新安装程序。"},
        {"engine_missing", L"网络服务尚未安装。绿色版请从“选项 → 准备网络服务”完成一次授权。"},
        {"network_setup_missing", L"缺少网络服务安装程序。请重新解压完整绿色版，或使用安装版。"},
        {"engine_rpc_failed",
         L"无法操作网络服务。请确认已安装本套装的 NetBird 服务并授权当前 Windows 用户。"},
        {"engine_cleanup_failed",
         L"尚未确认网络服务已断开。请恢复 NetGoNetBird 服务后再次断开，暂时不要切换服务器。"},
        {"engine_connect_timeout",
         L"网络服务未在规定时间内连接。请检查服务端 Management、Signal 和网络核心日志后重试。"},
        {"no_trusted_control_entry", L"未找到可验证的服务器入口。内网 IP 仅在内网可达；外网请配置公网直达或 "
                                     L"VPS 入口，并核对端口和证书。"},
        {"tls_certificate_failed", L"服务器证书验证失败。请核对域名、公共 CA 和系统时间。"},
        {"unauthorized", L"账号认证失败或已失效，请检查账号密码。"},
        {"forbidden", L"账号没有访问权限或已禁用。"},
        {"internet_forbidden", L"账号尚未获得互联网权限。请取消互联网选项，或联系管理员授权。"},
        {"internet_not_configured", L"服务器尚未设置旁路由和 DNS。请联系管理员设置。"},
        {"rate_limited", L"登录请求过于频繁，请稍候一分钟再试。"},
        {"session_conflict", L"该账号已有连接。如需替换，请勾选“替换旧连接”后重试。"},
        {"lease_expired", L"无法续期，连接已断开。请检查服务器入口后重新连接。"},
        {"route_conflict", L"当前局域网与远端网段重叠。请联系管理员调整网段或使用单主机资源。"},
        {"credentials_required", L"请填写账号和密码；已记住密码时可留空。"},
        {"site_trust_changed_use_new_id",
         L"该站点的域名或 CA 已改变。请使用新的站点 ID，重新核对服务器身份。"},
        {"profile_revision_stale", L"站点配置版本过旧。请向管理员取得最新站点文件。"},
        {"profile_revision_conflict", L"同一版本的站点配置内容不一致。请由管理员递增配置版本后重新导出。"},
        {"protocol_incompatible", L"客户端与服务端协议不兼容，请更新为匹配版本后重新连接。"},
        {"disconnect_before_edit", L"请先断开连接，再修改服务器或清理凭据。"},
        {"invalid_profile", L"服务器配置无效，请重新向管理员获取配置。"},
        {"site_identity_mismatch", L"服务器身份与导入配置不一致，请联系管理员核对。"},
        {"invalid_https_origin", L"控制地址须为 HTTPS 地址，可包含端口，不要填写路径。"},
        {"invalid_candidate", L"连接入口须为主机名或 IP 加端口，例如 vpn.example.com:10443。"},
        {"invalid_gateway_ip", L"请填写管理员提供的路由节点虚拟 IPv4 地址。"},
        {"invalid_ca", L"公共 CA 格式无效，请导入 PEM 格式证书。"},
        {"auto_connect_requires_saved_password",
         L"请先勾选“记住密码”并成功连接一次，再为此服务器开启自动连接。"},
    };
    auto i = messages.find(e);
    return i != messages.end() ? i->second : L"操作未完成（" + wide(e) + L"）。";
}
std::wstring stateText(const std::string &s) {
    static const std::map<std::string, std::wstring> names = {
        {"disconnected", L"未连接"},          {"disconnecting", L"正在断开"},
        {"probing", L"正在寻找服务器"},       {"authenticating", L"正在验证账号"},
        {"negotiating", L"正在建立数据连接"}, {"connected", L"已连接"},
        {"recovering", L"正在恢复连接"},      {"failed", L"连接失败"}};
    auto i = names.find(s);
    return i == names.end() ? L"等待状态" : i->second;
}
}
