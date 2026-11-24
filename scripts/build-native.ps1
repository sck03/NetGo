param([string]$Toolchain='D:\msys64\ucrt64',[switch]$SkipDependencies,[string]$Python='')
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
$Toolchain=[IO.Path]::GetFullPath($Toolchain)
. (Join-Path $PSScriptRoot 'project-environment.ps1')
. (Join-Path $PSScriptRoot 'publish-artifact.ps1')
if(-not $Python){$Python='python'}
if(-not(Test-Path -LiteralPath $Python)){$Python=(Get-Command $Python -ErrorAction Stop).Source}
if(-not $SkipDependencies){& "$PSScriptRoot\prepare-native.ps1" -Toolchain $Toolchain}
$env:PATH=(Join-Path $Toolchain 'bin')+';'+$env:PATH
function Check-Exit{if($LASTEXITCODE -ne 0){throw "Native build/check failed: $LASTEXITCODE"}}
$ssl=Join-Path $root '.tools\openssl-native'
$nativeVersion=& $Python -B "$PSScriptRoot/build_config.py" --target windows --field version
Check-Exit
cmake -S apps/windows -B .cache/native-build -G Ninja -DCMAKE_BUILD_TYPE=Release "-DNETGO_VERSION=$nativeVersion" "-DCMAKE_CXX_FLAGS_RELEASE=-O2 -DNDEBUG" "-DJSON_INCLUDE=$root/.tools/native-json" "-DNETGO_OPENSSL_ROOT=$ssl"
Check-Exit
cmake --build .cache/native-build -j 4
Check-Exit
ctest --test-dir .cache/native-build --output-on-failure
Check-Exit
if(Test-Path -LiteralPath $Python){& $Python apps/windows/tests/tls_fixture.py .cache/native-build/native-tests.exe;Check-Exit}else{throw 'A Python interpreter with cryptography is required for TLS verification'}
# Keep the baseline VPN client small; new optional modules need their own budget.
$budgets=@{'NetGo.exe'=10MB}
foreach($name in $budgets.Keys){
 $artifact=Get-Item -LiteralPath (Join-Path "$root\.cache\native-build" $name)
 if($artifact.Length -gt $budgets[$name]){throw "Native size budget exceeded: $name ($($artifact.Length) bytes)"}
 $imports=& (Join-Path $Toolchain 'bin\objdump.exe') -p $artifact.FullName;Check-Exit
 $dlls=@($imports|ForEach-Object {if($_ -match 'DLL Name:\s*(\S+)'){$Matches[1].ToLowerInvariant()}})
 if(-not $dlls.Count){throw "Missing PE import table: $name"}
 $system=@('advapi32.dll','bcrypt.dll','comctl32.dll','comdlg32.dll','crypt32.dll','gdi32.dll','imm32.dll','iphlpapi.dll','kernel32.dll','shell32.dll','user32.dll','uxtheme.dll','ws2_32.dll','d3d11.dll','dxgi.dll','mfplat.dll','ole32.dll','oleaut32.dll')
 foreach($dll in $dlls){if($dll -notin $system -and $dll -notmatch '^api-ms-win-crt-[a-z0-9-]+\.dll$'){throw "Unexpected runtime dependency: $name -> $dll"}}
 Write-Host "$name : $($artifact.Length) bytes; system DLL imports verified"
}
$dest=Join-Path $root 'dist\windows-amd64'
New-Item -ItemType Directory -Path $dest -Force|Out-Null
Publish-NetGoArtifact -Source "$root\.cache\native-build\NetGo.exe" -Destination (Join-Path $dest 'NetGo.exe')
# Retire only the three superseded product images in this output directory.
foreach($name in @('netgo-desktop.exe','netgo-agent.exe','netgo-remote.exe')){
 $old=Join-Path $dest $name
 if(Test-Path -LiteralPath $old){Remove-Item -LiteralPath $old}
}
Publish-NetGoArtifact -Source "$root\.tools\wintun-package\wintun\bin\amd64\wintun.dll" -Destination (Join-Path $dest 'wintun.dll')
Get-ChildItem -LiteralPath $dest -File|Where-Object {$_.Name -ne 'SHA256SUMS'}|Get-FileHash -Algorithm SHA256|ForEach-Object {"$($_.Hash.ToLower())  $([IO.Path]::GetFileName($_.Path))"}|Set-Content -Encoding utf8NoBOM (Join-Path $dest 'SHA256SUMS')
Write-Host 'Built and verified the single NetGo.exe entry, internal modes and system DLL imports. Build patched NetBird before packaging.'
