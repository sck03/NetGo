param([string]$Toolchain='D:\msys64\ucrt64',[switch]$UsePreparedCache)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
if(Test-Path -LiteralPath "$root\.tools\go\bin\go.exe"){$env:PATH="$root\.tools\go\bin;"+$env:PATH}
$env:PATH=(Join-Path $Toolchain 'bin')+';'+$env:PATH
if($UsePreparedCache){$source=Join-Path $root '.cache\netbird'}else{
 $prepared=python -B "$PSScriptRoot\prepare-upstream.py" | ConvertFrom-Json;if($LASTEXITCODE -ne 0){throw 'Upstream preparation failed'}
 $source=$prepared.netbird
}
Push-Location -LiteralPath $source
try {
 $env:CGO_ENABLED='1';$env:CC=Join-Path $Toolchain 'bin\gcc.exe'
 go test ./client/grpc ./client/cmd -run TestNetGo -count=1
 if($LASTEXITCODE -ne 0){throw 'Upstream adapter tests failed'}
 $env:CGO_ENABLED='0';$env:GOOS='windows';$env:GOARCH='amd64'
 New-Item -ItemType Directory -Path "$root\dist\windows-amd64" -Force|Out-Null
 go build -trimpath -o "$root\dist\windows-amd64\netbird.exe" ./client
 if($LASTEXITCODE -ne 0){throw 'NetBird client build failed'}
 Get-ChildItem -LiteralPath "$root\dist\windows-amd64" -File|Where-Object {$_.Name -ne 'SHA256SUMS'}|Get-FileHash -Algorithm SHA256|ForEach-Object {"$($_.Hash.ToLower())  $([IO.Path]::GetFileName($_.Path))"}|Set-Content -Encoding utf8NoBOM "$root\dist\windows-amd64\SHA256SUMS"
}finally{Pop-Location;Remove-Item Env:GOOS -ErrorAction SilentlyContinue}
