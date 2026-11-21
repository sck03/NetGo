[CmdletBinding()]
param([switch]$Plan)
$ErrorActionPreference='Stop'
$dest=Join-Path $env:LOCALAPPDATA 'NetGoNative\bin'
$engineDir=Join-Path $env:ProgramFiles 'NetGoNative\engine'
$engine=Join-Path $engineDir 'netbird.exe'
$service=Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'"
if($service -and $service.PathName -notlike ('*'+$engine+'*')){throw 'Unrecognized service executable; uninstall refused'}
if($Plan){[pscustomobject]@{UI=$dest;Engine=$engineDir;Service='NetGoNetBird';RetainData=$true};return}
$id=[System.Security.Principal.WindowsIdentity]::GetCurrent()
if(-not([System.Security.Principal.WindowsPrincipal]$id).IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run elevated as the intended desktop user'}
foreach($name in @('netgo-desktop','netgo-agent')){Get-Process -Name $name -ErrorAction SilentlyContinue|Where-Object {$_.Path -like ($dest+'\*')}|Stop-Process}
if($service){
 & $engine '--daemon-addr' 'npipe://netgo-netbird' 'down'
 if($LASTEXITCODE -ne 0){throw 'Network cleanup failed; retained installed files'}
 & $engine '--service' 'NetGoNetBird' 'service' 'stop';if($LASTEXITCODE -ne 0){throw 'Service stop failed'}
 & $engine '--service' 'NetGoNetBird' 'service' 'uninstall';if($LASTEXITCODE -ne 0){throw 'Service removal failed'}
}
Remove-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'NetGoNative' -ErrorAction SilentlyContinue
$link=Join-Path ([Environment]::GetFolderPath('Programs')) 'NetGo.lnk';if(Test-Path -LiteralPath $link){Remove-Item -LiteralPath $link}
# Delete only exact known binaries. Profile/config directories are retained for recovery.
foreach($file in @((Join-Path $dest 'netgo-desktop.exe'),(Join-Path $dest 'netgo-agent.exe'),$engine,(Join-Path $engineDir 'wintun.dll'))){if(Test-Path -LiteralPath $file){Remove-Item -LiteralPath $file}}
Write-Host 'Protected profile data was retained. Use the client Forget/Delete commands before uninstalling to remove saved credentials.'
Write-Host 'Removed native program and managed network service; retained configuration for recovery.'
