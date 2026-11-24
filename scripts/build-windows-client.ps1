param([string]$Toolchain='D:\msys64\ucrt64',[string]$Python='python',[switch]$SkipDependencies,[switch]$SkipUpstream,[string]$Version='',[ValidateSet('amd64')][string]$Arch='amd64',[switch]$Plan)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
$Toolchain=[IO.Path]::GetFullPath($Toolchain)
. "$PSScriptRoot/project-environment.ps1"
if(-not(Test-Path -LiteralPath $Python)){$Python=(Get-Command $Python -ErrorAction Stop).Source}
$configArgs=@('--target','windows','--arch',$Arch,'--field','version')
if($Version){$configArgs+=@('--version',$Version)}
$env:NETGO_VERSION=& $Python -B "$PSScriptRoot/build_config.py" @configArgs
if($LASTEXITCODE -ne 0){throw 'Invalid Windows build configuration'}
$env:NETGO_ARCH=$Arch
if($Plan){Write-Host "Windows client version=$env:NETGO_VERSION arch=$Arch; Setup and portable; publishing is controlled separately by the workflow.";return}
if(Test-Path -LiteralPath "$root/.tools/go/bin/go.exe"){$env:PATH="$root/.tools/go/bin;"+$env:PATH}
$env:PATH=(Join-Path $Toolchain 'bin')+';'+$env:PATH
if(-not $SkipDependencies){& "$PSScriptRoot/prepare-native.ps1" -Toolchain $Toolchain}
if(-not $SkipUpstream){& "$PSScriptRoot/build-upstream-windows.ps1" -Toolchain $Toolchain}
& "$PSScriptRoot/build-native.ps1" -SkipDependencies -Toolchain $Toolchain -Python $Python
& $Python -B "$PSScriptRoot/package-windows-client.py" --version $env:NETGO_VERSION --arch $Arch
if($LASTEXITCODE -ne 0){throw 'Windows client packaging failed'}
Write-Host 'Windows client: dist/windows-client (NetGo.exe / Setup / portable).'
