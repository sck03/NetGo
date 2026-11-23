param([switch]$SkipFrontend,[switch]$SkipNative,[switch]$SkipUpstream,[switch]$SkipChecks,[string]$Toolchain='D:\msys64\ucrt64',[string]$Python='')
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
. (Join-Path $PSScriptRoot 'project-environment.ps1')
if(Test-Path -LiteralPath "$root\.tools\go\bin\go.exe"){$env:PATH="$root\.tools\go\bin;"+$env:PATH}
if(Test-Path -LiteralPath "$root\.tools\node-v26.13.1-win-x64\node.exe"){$env:PATH="$root\.tools\node-v26.13.1-win-x64;"+$env:PATH}
function Check-Exit{if($LASTEXITCODE -ne 0){throw "Build command failed ($LASTEXITCODE)"}}
if(-not $SkipFrontend){npm ci --no-fund;Check-Exit;npm run build;Check-Exit;npm test;Check-Exit}
$env:CGO_ENABLED='0'
if(-not $SkipChecks){go test ./internal/... ./cmd/...;Check-Exit;go vet ./internal/... ./cmd/...;Check-Exit}
$releaseVersion=go run ./cmd/netgoctl version | ConvertFrom-Json;Check-Exit
New-Item -ItemType Directory -Path "$root\dist\linux-amd64" -Force|Out-Null
$env:GOOS='linux';$env:GOARCH='amd64'
foreach($name in @('netgoctl','netgo-server','netgo-helper','netgo-client-service','netgo-ddns')){go build -trimpath -ldflags '-s -w' -o "dist/linux-amd64/$name" "./cmd/$name";Check-Exit}
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
[ordered]@{version=$releaseVersion.version;protocol=$releaseVersion.protocol;schema=$releaseVersion.schema;os='linux';arch='amd64'} | ConvertTo-Json | Set-Content -LiteralPath "$root\dist\linux-amd64\release.json" -Encoding utf8NoBOM
Get-ChildItem -LiteralPath "$root\dist\linux-amd64" -File | Where-Object {$_.Name -ne 'SHA256SUMS'} | Get-FileHash -Algorithm SHA256 | ForEach-Object {"$($_.Hash.ToLower())  $([IO.Path]::GetFileName($_.Path))"} | Set-Content -LiteralPath "$root\dist\linux-amd64\SHA256SUMS" -Encoding utf8NoBOM
if(-not $SkipNative){& "$PSScriptRoot\build-native.ps1" -Toolchain $Toolchain -Python $Python}
if(-not $SkipUpstream){& "$PSScriptRoot\build-upstream-windows.ps1" -Toolchain $Toolchain}
$windows=Join-Path $root 'dist\windows-amd64'
if(Test-Path -LiteralPath $windows){
 Get-ChildItem -LiteralPath $windows -File|Where-Object {$_.Name -ne 'SHA256SUMS'}|Get-FileHash -Algorithm SHA256|ForEach-Object {"$($_.Hash.ToLower())  $([IO.Path]::GetFileName($_.Path))"}|Set-Content -Encoding utf8NoBOM (Join-Path $windows 'SHA256SUMS')
}
$artifactFiles=@(Get-ChildItem -LiteralPath "$root\dist\linux-amd64" -File)
if(Test-Path -LiteralPath $windows){$artifactFiles+=Get-ChildItem -LiteralPath $windows -File}
$artifactFiles|Where-Object {$_.Name -ne 'SHA256SUMS'}|Get-FileHash -Algorithm SHA256|ForEach-Object {"$($_.Hash.ToLower())  $($_.Path.Substring($root.Length+6).Replace('\','/'))"}|Set-Content -Encoding utf8NoBOM "$root\dist\SHA256SUMS"
Write-Host 'Product source checks and builds complete. Linux upstream CGO services and network acceptance require a Linux lab.'
