#pragma once
#include "common.hpp"
int desktopMain(HINSTANCE instance, PWSTR args, int show);
int agentMain(const std::filesystem::path &root, bool test);
int remoteMain(HINSTANCE instance, int show, const std::wstring &site, bool autoHost = false);
