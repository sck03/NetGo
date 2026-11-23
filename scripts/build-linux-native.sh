#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
build=${NETGO_NATIVE_BUILD:-.cache/linux-native-build}
json=${NETGO_JSON_INCLUDE:-.tools/native-json}
cmake -S apps/linux -B "$build" -G Ninja -DCMAKE_BUILD_TYPE=Release -DJSON_INCLUDE="$PWD/$json" -DNETGO_TRANSPORT_TESTS=ON -DBUILD_TESTING=ON
cmake --build "$build" --parallel 4
ctest --test-dir "$build" --output-on-failure --no-tests=error
mkdir -p dist/linux-desktop-amd64
cp "$build/netgo-desktop" dist/linux-desktop-amd64/netgo-desktop
(cd dist/linux-desktop-amd64 && sha256sum netgo-desktop > SHA256SUMS)
