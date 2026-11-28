[CmdletBinding()]
param([string]$PackageRoot=(Split-Path -Parent (Split-Path -Parent $PSScriptRoot)),[switch]$Plan,[switch]$NetworkOnly)
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
$stateDir=Split-Path -Parent $engineConfig
$serviceRegistry='HKLM:\SYSTEM\CurrentControlSet\Services\NetGoNetBird'
$oldServiceEnvironment=$null
$maintenance=Join-Path $env:ProgramFiles 'NetGoNative\maintenance'
$maintenanceFiles=@('install-client.ps1','deploy\windows\install.ps1','deploy\windows\uninstall.ps1','deploy\windows\common.ps1')
$manifest=Join-Path $source 'SHA256SUMS'
$files=@('NetGo.exe','netbird.exe','wintun.dll')
if($NetworkOnly){$files=@('netbird.exe','wintun.dll')}
$registerApplication=Test-Path -LiteralPath (Join-Path $source 'netgo-maintenance.exe')
if($registerApplication){$files+=@('netgo-maintenance.exe')}
$applicationDir=Join-Path $env:ProgramFiles 'NetGoNative'
$uninstaller=Join-Path $applicationDir 'Uninstall.exe'
foreach($name in $files){if(-not(Test-Path -LiteralPath (Join-Path $source $name))){throw "Missing package file: $name"}}
foreach($name in $maintenanceFiles){if(-not(Test-Path -LiteralPath (Join-Path $PackageRoot $name))){throw "Missing maintenance file: $name"}}
if(-not(Test-Path -LiteralPath $manifest)){throw 'Package SHA256SUMS missing'}
$expected=@{}
foreach($line in Get-Content -LiteralPath $manifest){if($line -match '^([0-9a-fA-F]{64})  ([a-zA-Z0-9_.-]+)$'){$expected[$matches[2]]=$matches[1].ToLower()}}
foreach($name in $files){$actual=(Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $source $name)).Hash.ToLower();if($actual -ne $expected[$name]){throw "Package checksum mismatch: $name"}}
$packageVersion=if($registerApplication){Get-NetGoPackageVersion (Join-Path $source 'netgo-maintenance.exe')}else{''}
if((Get-AuthenticodeSignature -LiteralPath (Join-Path $source 'wintun.dll')).Status -ne 'Valid'){throw 'Wintun signature did not validate'}
$existing=Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'"
if($existing){$oldServiceEnvironment=(Get-ItemProperty -LiteralPath $serviceRegistry -Name Environment -ErrorAction SilentlyContinue).Environment}
if($existing -and -not(Test-NetGoServiceImage $existing.PathName (Join-Path $engineDir 'netbird.exe'))){throw 'A different service owns NetGoNetBird; refusing to replace it'}
$ownerRecord=Join-Path (Split-Path -Parent $engineConfig) 'owner.sid'
$previousApplication=Get-NetGoApplicationRegistration
$registrationNetworkOnly=[bool]$NetworkOnly
if($previousApplication){
 $previousOwner=@($previousApplication.Values|Where-Object Name -eq 'NetGoOwnerSID')
 if($previousOwner.Count -ne 1 -or $previousOwner[0].Value -ne $owner){throw 'Application registration belongs to a different desktop SID'}
 # Preparing the network for a portable client must not hide an installed UI.
 if(@($previousApplication.Values|Where-Object { $_.Name -eq 'NetworkOnly' -and $_.Value -eq 'False' }).Count){$registrationNetworkOnly=$false}
}
if($Plan){[pscustomobject]@{UI=$dest;Engine=$engineDir;Service='NetGoNetBird';Pipe='npipe://netgo-netbird';OwnerSID=$owner;Administrator=$administrator;Checksums='PASS'};return}
if(-not $administrator){throw 'Run this installer from an elevated PowerShell as the intended desktop user. It installs the privileged network service in Program Files.'}
if($existing -and (-not(Test-Path -LiteralPath $ownerRecord) -or (Get-Content -LiteralPath $ownerRecord -Raw).Trim() -ne $owner)){throw 'Managed network service owner is missing or bound to a different desktop SID'}
New-Item -ItemType Directory -Path $dest,$engineDir,(Split-Path -Parent $engineConfig),(Join-Path $maintenance 'deploy\windows') -Force|Out-Null
# A privileged service must never execute a user-writable image or DLL.
& icacls.exe $engineDir '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine directory ACL failed'}
& icacls.exe $maintenance '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Maintenance directory ACL failed'}
& icacls.exe (Split-Path -Parent $engineConfig) '/inheritance:r' '/grant:r' '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Engine config directory ACL failed'}
$binary=Join-Path $engineDir 'netbird.exe'
$shortcutPath=Join-Path ([Environment]::GetFolderPath('Programs')) 'NetGo.lnk'
$stage=Join-Path $engineDir '.install-transaction'
if(Test-Path -LiteralPath $stage){throw "Interrupted install detected. Review protected backups in $stage before retrying."}
New-Item -ItemType Directory -Path $stage|Out-Null
$targets=@((Join-Path $dest 'NetGo.exe'),$binary,(Join-Path $engineDir 'wintun.dll'),$engineConfig,$ownerRecord,$shortcutPath,(Join-Path $stateDir 'service.json'))
if($NetworkOnly){$targets=@($binary,(Join-Path $engineDir 'wintun.dll'),$engineConfig,$ownerRecord,(Join-Path $stateDir 'service.json'))}
if($registerApplication){$targets+=@($uninstaller)}
$targets+=@($maintenanceFiles|ForEach-Object {Join-Path $maintenance $_})
$utf8=New-Object System.Text.UTF8Encoding($false)
$backups=@();$wasRunning=$existing -and $existing.State -eq 'Running';$changed=$false
$oldStateDir=[Environment]::GetEnvironmentVariable('NB_STATE_DIR','Process')
$env:NB_STATE_DIR=$stateDir
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
 [IO.File]::WriteAllText((Join-Path $stage 'journal.json'),(@{ServiceExisted=[bool]$existing;WasRunning=[bool]$wasRunning;PreviousImage=if($existing){$existing.PathName}else{''};PreviousEnvironment=$oldServiceEnvironment;ApplicationRegistration=$previousApplication;Files=$backups}|ConvertTo-Json -Depth 8),$utf8)
 $changed=$true
 foreach($name in @('NetGo')){Get-Process -Name $name -ErrorAction SilentlyContinue|Where-Object {$_.Path -like ($dest+'\*')}|Stop-Process}
 if($existing){& $binary '--service' 'NetGoNetBird' 'service' 'stop';if($LASTEXITCODE -ne 0){throw 'Could not stop the managed network service'}}
 if(-not $NetworkOnly){foreach($name in @('NetGo.exe')){Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $dest $name) -Force}}
 foreach($name in @('netbird.exe','wintun.dll')){Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $engineDir $name) -Force}
 foreach($name in $maintenanceFiles){Copy-Item -LiteralPath (Join-Path $PackageRoot $name) -Destination (Join-Path $maintenance $name) -Force}
 if(-not(Test-Path -LiteralPath $engineConfig)){[IO.File]::WriteAllText($engineConfig,'{"DisableAutoConnect":true,"ManagementURL":{"Scheme":"https","Host":"127.0.0.1:1"}}',$utf8)}
 $engineLog=Join-Path $stateDir 'client.log'
 if(-not $existing){
  & $binary '--service' 'NetGoNetBird' '--config' $engineConfig '--log-file' $engineLog '--daemon-addr' 'npipe://netgo-netbird' 'service' 'install' '--allow-group' $owner '--service-env' ("NB_STATE_DIR="+$stateDir+",NB_LAZY_CONN=false")
  if($LASTEXITCODE -ne 0){throw 'Network service installation failed'}
 }else{
  $serviceImage='"'+$binary+'" service run --log-level info --daemon-addr npipe://netgo-netbird --config "'+$engineConfig+'" --log-file "'+$engineLog+'" --allow-group sid:'+$owner
  $change=Invoke-CimMethod -InputObject $existing -MethodName Change -Arguments @{PathName=$serviceImage}
  if($change.ReturnValue -ne 0){throw 'Could not update owned service arguments'}
  $serviceEnvironment=Get-NetGoServiceEnvironment -StateDirectory $stateDir -Previous $oldServiceEnvironment
  New-ItemProperty -LiteralPath $serviceRegistry -Name Environment -PropertyType MultiString -Value ([string[]]$serviceEnvironment) -Force | Out-Null
 }
 [IO.File]::WriteAllText($ownerRecord,$owner,$utf8)
 & $binary '--service' 'NetGoNetBird' 'service' 'start';if($LASTEXITCODE -ne 0){throw 'Network service did not start'}
 if(-not $NetworkOnly){
  $shell=New-Object -ComObject WScript.Shell
  $shortcut=$shell.CreateShortcut($shortcutPath);$shortcut.TargetPath=Join-Path $dest 'NetGo.exe';$shortcut.WorkingDirectory=$dest;$shortcut.IconLocation=(Join-Path $dest 'NetGo.exe')+',0';$shortcut.Save()
 }
 if($registerApplication){
  Copy-Item -LiteralPath (Join-Path $source 'netgo-maintenance.exe') -Destination $uninstaller -Force
  Register-NetGoApplication -Directory $applicationDir -OwnerSID $owner -NetworkOnly $registrationNetworkOnly -Version $packageVersion
 }
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
   if($registerApplication){Restore-NetGoApplicationRegistration $previousApplication}
   if($existing){
    $recoverService=Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'"
    if(-not $recoverService){throw 'Previous service registration is missing; inspect recovery journal'}
    $restored=Invoke-CimMethod -InputObject $recoverService -MethodName Change -Arguments @{PathName=$existing.PathName}
    if($restored.ReturnValue -ne 0){throw 'Previous service arguments could not be restored'}
    if($null -eq $oldServiceEnvironment){Remove-ItemProperty -LiteralPath $serviceRegistry -Name Environment -ErrorAction SilentlyContinue}
    else{New-ItemProperty -LiteralPath $serviceRegistry -Name Environment -PropertyType MultiString -Value ([string[]]$oldServiceEnvironment) -Force | Out-Null}
   }
   if($wasRunning){& $binary '--service' 'NetGoNetBird' 'service' 'start';if($LASTEXITCODE -ne 0){throw 'Previous service could not restart'}}
   Clear-InstallJournal
  } catch {throw "Install failed and recovery is incomplete. Retained protected journal: $stage. $($_.Exception.Message)"}
 }
 throw $originalError
} finally {
 [Environment]::SetEnvironmentVariable('NB_STATE_DIR',$oldStateDir,'Process')
}
Write-Host "Installed native NetGo for SID $owner. Start NetGo from the Start menu without elevation."
