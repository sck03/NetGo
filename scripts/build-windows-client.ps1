param([string]$Toolchain='D:\msys64\ucrt64',[string]$Python='python',[switch]$SkipDependencies,[switch]$SkipUpstream)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
. "$PSScriptRoot/project-environment.ps1"
if(-not(Test-Path -LiteralPath $Python)){$Python=(Get-Command $Python -ErrorAction Stop).Source}
if(Test-Path -LiteralPath "$root/.tools/go/bin/go.exe"){$env:PATH="$root/.tools/go/bin;"+$env:PATH}
$env:PATH=(Join-Path $Toolchain 'bin')+';'+$env:PATH
if(-not $SkipDependencies){& "$PSScriptRoot/prepare-native.ps1" -Toolchain $Toolchain}
if(-not $SkipUpstream){& "$PSScriptRoot/build-upstream-windows.ps1" -Toolchain $Toolchain}
& "$PSScriptRoot/build-native.ps1" -SkipDependencies -Toolchain $Toolchain -Python $Python
& $Python -B "$PSScriptRoot/package-windows-client.py"
if($LASTEXITCODE -ne 0){throw 'Windows client packaging failed'}
Write-Host 'Windows client: dist/windows-client (NetGo.exe / Setup / portable).'
