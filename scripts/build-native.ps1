param([string]$Toolchain='D:\msys64\ucrt64',[switch]$SkipDependencies,[string]$Python='')
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
if(-not $Python){$Python=(Get-Command python -ErrorAction Stop).Source}
if(-not $SkipDependencies){& "$PSScriptRoot\prepare-native.ps1" -Toolchain $Toolchain}
$env:PATH=(Join-Path $Toolchain 'bin')+';'+$env:PATH
function Check-Exit{if($LASTEXITCODE -ne 0){throw "Native build/check failed: $LASTEXITCODE"}}
$ssl=Join-Path $root '.tools\openssl-native'
cmake -S apps/windows -B .cache/native-build -G Ninja -DCMAKE_BUILD_TYPE=Release "-DCMAKE_CXX_FLAGS_RELEASE=-O2 -DNDEBUG" "-DJSON_INCLUDE=$root/.tools/native-json" "-DOPENSSL_INCLUDE_DIR=$ssl/include" "-DOPENSSL_SSL_LIBRARY=$ssl/lib64/libssl.a" "-DOPENSSL_CRYPTO_LIBRARY=$ssl/lib64/libcrypto.a"
Check-Exit
cmake --build .cache/native-build -j 4
Check-Exit
ctest --test-dir .cache/native-build --output-on-failure
Check-Exit
if(Test-Path -LiteralPath $Python){& $Python apps/windows/tests/tls_fixture.py .cache/native-build/native-tests.exe;Check-Exit}else{throw 'A Python interpreter with cryptography is required for TLS verification'}
$dest=Join-Path $root 'dist\windows-amd64'
New-Item -ItemType Directory -Path $dest -Force|Out-Null
foreach($name in @('netgo-desktop.exe','netgo-agent.exe')){Copy-Item -LiteralPath (Join-Path "$root\.cache\native-build" $name) -Destination (Join-Path $dest $name) -Force}
Copy-Item -LiteralPath "$root\.tools\wintun-package\wintun\bin\amd64\wintun.dll" -Destination $dest -Force
Get-ChildItem -LiteralPath $dest -File|Where-Object {$_.Name -ne 'SHA256SUMS'}|Get-FileHash -Algorithm SHA256|ForEach-Object {"$($_.Hash.ToLower())  $([IO.Path]::GetFileName($_.Path))"}|Set-Content -Encoding utf8NoBOM (Join-Path $dest 'SHA256SUMS')
Write-Host 'Built and verified native Windows UI and agent. Build patched NetBird before packaging.'
