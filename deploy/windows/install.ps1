[CmdletBinding()]
param([string]$PackageRoot=(Split-Path -Parent (Split-Path -Parent $PSScriptRoot)),[switch]$Plan)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'common.ps1')
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
if($existing -and -not(Test-NetGoServiceImage $existing.PathName (Join-Path $engineDir 'netbird.exe'))){throw 'A different service owns NetGoNetBird; refusing to replace it'}
$ownerRecord=Join-Path (Split-Path -Parent $engineConfig) 'owner.sid'
if($existing -and (-not(Test-Path -LiteralPath $ownerRecord) -or (Get-Content -LiteralPath $ownerRecord -Raw).Trim() -ne $owner)){throw 'Managed network service owner is missing or bound to a different desktop SID'}
if($Plan){[pscustomobject]@{UI=$dest;Engine=$engineDir;Service='NetGoNetBird';Pipe='npipe://netgo-netbird';OwnerSID=$owner;Administrator=$administrator;Checksums='PASS'};return}
if(-not $administrator){throw 'Run this installer from an elevated PowerShell as the intended desktop user. It installs the privileged network service in Program Files.'}
New-Item -ItemType Directory -Path $dest,$engineDir,(Split-Path -Parent $engineConfig) -Force|Out-Null
# A privileged service must never execute a user-writable image or DLL.
& icacls.exe $engineDir '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine directory ACL failed'}
& icacls.exe (Split-Path -Parent $engineConfig) '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine config directory ACL failed'}
$binary=Join-Path $engineDir 'netbird.exe'
$shortcutPath=Join-Path ([Environment]::GetFolderPath('Programs')) 'NetGo.lnk'
$stage=Join-Path $engineDir '.install-transaction'
if(Test-Path -LiteralPath $stage){throw "Interrupted install detected. Review protected backups in $stage before retrying."}
New-Item -ItemType Directory -Path $stage|Out-Null
$targets=@((Join-Path $dest 'netgo-desktop.exe'),(Join-Path $dest 'netgo-agent.exe'),$binary,(Join-Path $engineDir 'wintun.dll'),$engineConfig,$ownerRecord,$shortcutPath)
$backups=@();$wasRunning=$existing -and $existing.State -eq 'Running';$changed=$false
function Clear-InstallJournal {
 foreach($entry in $backups){if(Test-Path -LiteralPath $entry.Backup){Remove-Item -LiteralPath $entry.Backup}}
 Remove-Item -LiteralPath (Join-Path $stage 'journal.json') -ErrorAction SilentlyContinue
 Remove-Item -LiteralPath $stage
}
try {
 for($i=0;$i -lt $targets.Count;$i++){
  $backup=Join-Path $stage "file-$i.backup";$exists=Test-Path -LiteralPath $targets[$i]
  if($exists){Copy-Item -LiteralPath $targets[$i] -Destination $backup}
  $backups+=[pscustomobject]@{Target=$targets[$i];Backup=$backup;Existed=$exists}
 }
 @{ServiceExisted=[bool]$existing;WasRunning=[bool]$wasRunning;Files=$backups}|ConvertTo-Json -Depth 5|Set-Content -LiteralPath (Join-Path $stage 'journal.json') -Encoding utf8NoBOM
 $changed=$true
 foreach($name in @('netgo-desktop','netgo-agent')){Get-Process -Name $name -ErrorAction SilentlyContinue|Where-Object {$_.Path -like ($dest+'\*')}|Stop-Process}
 if($existing){& $binary '--service' 'NetGoNetBird' 'service' 'stop';if($LASTEXITCODE -ne 0){throw 'Could not stop the managed network service'}}
 foreach($name in @('netgo-desktop.exe','netgo-agent.exe')){Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $dest $name) -Force}
 foreach($name in @('netbird.exe','wintun.dll')){Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $engineDir $name) -Force}
 if(-not(Test-Path -LiteralPath $engineConfig)){'{"DisableAutoConnect":true,"ManagementURL":"https://127.0.0.1:1"}'|Set-Content -LiteralPath $engineConfig -Encoding utf8NoBOM}
 if(-not $existing){& $binary '--service' 'NetGoNetBird' '--config' $engineConfig '--daemon-addr' 'npipe://netgo-netbird' 'service' 'install' '--allow-group' $owner;if($LASTEXITCODE -ne 0){throw 'Network service installation failed'}}
 $owner|Set-Content -LiteralPath $ownerRecord -Encoding utf8NoBOM
 & $binary '--service' 'NetGoNetBird' 'service' 'start';if($LASTEXITCODE -ne 0){throw 'Network service did not start'}
 $shell=New-Object -ComObject WScript.Shell
 $shortcut=$shell.CreateShortcut($shortcutPath);$shortcut.TargetPath=Join-Path $dest 'netgo-desktop.exe';$shortcut.WorkingDirectory=$dest;$shortcut.Save()
 $changed=$false
 Clear-InstallJournal
} catch {
 $originalError=$_
 if($changed){
  try {
   $partial=Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'"
   if($partial){
    if(-not(Test-NetGoServiceImage $partial.PathName $binary)){throw 'Unexpected service appeared during recovery'}
    & $binary '--service' 'NetGoNetBird' 'service' 'stop';if($LASTEXITCODE -ne 0){throw 'Recovery service stop failed'}
    if(-not $existing){& $binary '--service' 'NetGoNetBird' 'service' 'uninstall';if($LASTEXITCODE -ne 0){throw 'Recovery service removal failed'}}
   }
   foreach($entry in $backups){
    if($entry.Existed){Copy-Item -LiteralPath $entry.Backup -Destination $entry.Target -Force}
    elseif(Test-Path -LiteralPath $entry.Target){Remove-Item -LiteralPath $entry.Target}
   }
   if($wasRunning){& $binary '--service' 'NetGoNetBird' 'service' 'start';if($LASTEXITCODE -ne 0){throw 'Previous service could not restart'}}
   Clear-InstallJournal
  } catch {throw "Install failed and recovery is incomplete. Retained protected journal: $stage. $($_.Exception.Message)"}
 }
 throw $originalError
}
Write-Host "Installed native NetGo for SID $owner. Start NetGo from the Start menu without elevation."
