#include "common.hpp"

namespace netgo {
std::string profileTrustIdentity(const Json& profile) {
    return sha256(Json::array({profile.at("Protocol"), profile.at("ID"),
                              originHost(profile.at("Origin")), profile.value("CA", "")}).dump());
}

Json decodeRememberedLogin(const Json& profile, const std::string& encoded) {
    auto saved = Json::parse(encoded, nullptr, false);
    if (!saved.is_object() || !saved.contains("profile") ||
        saved["profile"] != profileTrustIdentity(profile) ||
        !saved.contains("alias") || !saved["alias"].is_string() ||
        !saved.contains("password") || !saved["password"].is_string())
        return Json::object();
    if (saved["alias"].get_ref<const std::string&>().empty() ||
        saved["password"].get_ref<const std::string&>().empty())
        return Json::object();
    return saved;
}

Json rememberedLogin(const Json& profile) {
    auto encoded = credentialRead("password/" + profile.at("ID").get<std::string>());
    auto result = decodeRememberedLogin(profile, encoded);
    wipe(encoded);
    return result;
}

void rememberLogin(const Json& profile, const std::string& alias, const std::string& password) {
    auto encoded = Json({{"alias", alias}, {"password", password},
                         {"profile", profileTrustIdentity(profile)}}).dump();
    try { credentialWrite("password/" + profile.at("ID").get<std::string>(), encoded); }
    catch (...) { wipe(encoded); throw; }
    wipe(encoded);
}
}
