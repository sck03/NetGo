[CmdletBinding()]
param([switch]$Plan)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'common.ps1')
$dest=Join-Path $env:LOCALAPPDATA 'NetGoNative\bin'
$engineDir=Join-Path $env:ProgramFiles 'NetGoNative\engine'
$engine=Join-Path $engineDir 'netbird.exe'
$service=Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'"
if($service -and -not(Test-NetGoServiceImage $service.PathName $engine)){throw 'Unrecognized service executable; uninstall refused'}
$ownerRecord=Join-Path $env:ProgramData 'NetGoNativeEngine\owner.sid'
if($Plan){[pscustomobject]@{UI=$dest;Engine=$engineDir;Service='NetGoNetBird';RetainData=$true};return}
$id=[System.Security.Principal.WindowsIdentity]::GetCurrent()
if(-not([System.Security.Principal.WindowsPrincipal]$id).IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run elevated as the intended desktop user'}
if($service -and (-not(Test-Path -LiteralPath $ownerRecord) -or (Get-Content -LiteralPath $ownerRecord -Raw).Trim() -ne $id.User.Value)){throw 'Managed service belongs to a different desktop SID'}
$oldStateDir=[Environment]::GetEnvironmentVariable('NB_STATE_DIR','Process')
$env:NB_STATE_DIR=Join-Path $env:ProgramData 'NetGoNativeEngine'
try {
foreach($name in @('NetGo')){Get-Process -Name $name -ErrorAction SilentlyContinue|Where-Object {$_.Path -like ($dest+'\*')}|Stop-Process}
if($service){
 & $engine '--daemon-addr' 'npipe://netgo-netbird' 'down'
 if($LASTEXITCODE -ne 0){throw 'Network cleanup failed; retained installed files'}
 & $engine '--service' 'NetGoNetBird' 'service' 'stop';if($LASTEXITCODE -ne 0){throw 'Service stop failed'}
 & $engine '--service' 'NetGoNetBird' 'service' 'uninstall';if($LASTEXITCODE -ne 0){throw 'Service removal failed'}
}
Remove-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'NetGoNative' -ErrorAction SilentlyContinue
$link=Join-Path ([Environment]::GetFolderPath('Programs')) 'NetGo.lnk';if(Test-Path -LiteralPath $link){Remove-Item -LiteralPath $link}
# Delete only exact known binaries. Profile/config directories are retained for recovery.
foreach($file in @((Join-Path $dest 'NetGo.exe'),$engine,(Join-Path $engineDir 'wintun.dll'))){if(Test-Path -LiteralPath $file){Remove-Item -LiteralPath $file}}
$applicationDir=Join-Path $env:ProgramFiles 'NetGoNative'
$maintenance=Join-Path $applicationDir 'maintenance'
# The native uninstaller runs from private staging so its installed image is free.
foreach($name in @('Uninstall.exe','maintenance\install-client.ps1','maintenance\deploy\windows\install.ps1','maintenance\deploy\windows\uninstall.ps1','maintenance\deploy\windows\common.ps1')){
 $file=Join-Path $applicationDir $name
 if(Test-Path -LiteralPath $file){Remove-Item -LiteralPath $file}
}
$appKey='HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\NetGoNative'
if(Test-Path -LiteralPath $appKey){Remove-Item -LiteralPath $appKey}
# Empty directories only: never recursively remove unknown user files.
foreach($directory in @((Join-Path $maintenance 'deploy\windows'),(Join-Path $maintenance 'deploy'),$maintenance,$engineDir,$applicationDir,$dest)){
 if((Test-Path -LiteralPath $directory) -and -not(Get-ChildItem -LiteralPath $directory -Force|Select-Object -First 1)){Remove-Item -LiteralPath $directory}
}
Write-Host 'Protected profile data was retained. Use the client Forget/Delete commands before uninstalling to remove saved credentials.'
Write-Host 'Removed native program and managed network service; retained configuration for recovery.'
} finally {
 [Environment]::SetEnvironmentVariable('NB_STATE_DIR',$oldStateDir,'Process')
}
