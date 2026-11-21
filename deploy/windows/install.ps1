[CmdletBinding()]
param([string]$PackageRoot=(Split-Path -Parent (Split-Path -Parent $PSScriptRoot)),[switch]$Plan)
$ErrorActionPreference='Stop'
$identity=[System.Security.Principal.WindowsIdentity]::GetCurrent()
$owner=$identity.User.Value
$administrator=([System.Security.Principal.WindowsPrincipal]$identity).IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
$source=Join-Path $PackageRoot 'dist\windows-amd64'
if(-not(Test-Path -LiteralPath $source)){$source=Join-Path $PackageRoot 'bin'}
$dest=Join-Path $env:LOCALAPPDATA 'NetGoNative\bin'
$engineDir=Join-Path $env:ProgramFiles 'NetGoNative\engine'
$engineConfig=Join-Path $env:ProgramData 'NetGoNativeEngine\config.json'
$manifest=Join-Path $source 'SHA256SUMS'
$files=@('netgo-desktop.exe','netgo-agent.exe','netbird.exe','wintun.dll')
foreach($name in $files){if(-not(Test-Path -LiteralPath (Join-Path $source $name))){throw "Missing package file: $name"}}
if(-not(Test-Path -LiteralPath $manifest)){throw 'Package SHA256SUMS missing'}
$expected=@{}
foreach($line in Get-Content -LiteralPath $manifest){if($line -match '^([0-9a-fA-F]{64})  ([a-zA-Z0-9_.-]+)$'){$expected[$matches[2]]=$matches[1].ToLower()}}
foreach($name in $files){$actual=(Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $source $name)).Hash.ToLower();if($actual -ne $expected[$name]){throw "Package checksum mismatch: $name"}}
if((Get-AuthenticodeSignature -LiteralPath (Join-Path $source 'wintun.dll')).Status -ne 'Valid'){throw 'Wintun signature did not validate'}
$existing=Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'"
if($existing -and $existing.PathName -notlike ('*'+$engineDir+'\netbird.exe*')){throw 'A different service owns NetGoNetBird; refusing to replace it'}
$ownerRecord=Join-Path (Split-Path -Parent $engineConfig) 'owner.sid'
if($existing -and (Test-Path -LiteralPath $ownerRecord) -and (Get-Content -LiteralPath $ownerRecord -Raw).Trim() -ne $owner){throw 'Managed network service is bound to a different desktop SID'}
if($Plan){[pscustomobject]@{UI=$dest;Engine=$engineDir;Service='NetGoNetBird';Pipe='npipe://netgo-netbird';OwnerSID=$owner;Administrator=$administrator;Checksums='PASS'};return}
if(-not $administrator){throw 'Run this installer from an elevated PowerShell as the intended desktop user. It installs the privileged network service in Program Files.'}
foreach($name in @('netgo-desktop','netgo-agent')){Get-Process -Name $name -ErrorAction SilentlyContinue|Where-Object {$_.Path -like ($dest+'\*')}|Stop-Process}
if($existing){& (Join-Path $engineDir 'netbird.exe') '--service' 'NetGoNetBird' 'service' 'stop';if($LASTEXITCODE -ne 0){throw 'Could not stop the managed network service'}}
New-Item -ItemType Directory -Path $dest,$engineDir,(Split-Path -Parent $engineConfig) -Force|Out-Null
# A privileged service must never execute a user-writable image or DLL.
& icacls.exe $engineDir '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine directory ACL failed'}
& icacls.exe (Split-Path -Parent $engineConfig) '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine config directory ACL failed'}
foreach($name in @('netgo-desktop.exe','netgo-agent.exe')){Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $dest $name) -Force}
foreach($name in @('netbird.exe','wintun.dll')){Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $engineDir $name) -Force}
$binary=Join-Path $engineDir 'netbird.exe'
if(-not(Test-Path -LiteralPath $engineConfig)){
 # Bootstrap is offline. The native Login RPC supplies the verified site later.
 '{"DisableAutoConnect":true,"ManagementURL":"https://127.0.0.1:1"}'|Set-Content -LiteralPath $engineConfig -Encoding utf8NoBOM
}
if(-not $existing){& $binary '--service' 'NetGoNetBird' '--config' $engineConfig '--daemon-addr' 'npipe://netgo-netbird' 'service' 'install' '--allow-group' $owner;if($LASTEXITCODE -ne 0){throw 'Network service installation failed'}}
$owner|Set-Content -LiteralPath $ownerRecord -Encoding utf8NoBOM
& $binary '--service' 'NetGoNetBird' 'service' 'start'
if($LASTEXITCODE -ne 0){throw 'Network service did not start'}
$shortcutPath=Join-Path ([Environment]::GetFolderPath('Programs')) 'NetGo.lnk'
$shell=New-Object -ComObject WScript.Shell
$shortcut=$shell.CreateShortcut($shortcutPath);$shortcut.TargetPath=Join-Path $dest 'netgo-desktop.exe';$shortcut.WorkingDirectory=$dest;$shortcut.Save()
Write-Host "Installed native NetGo for SID $owner. Start NetGo from the Start menu without elevation."
