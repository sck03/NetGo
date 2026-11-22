param(
 [string]$Output='',
 [string]$Toolchain='D:\msys64\ucrt64',
 [string]$Python='D:\Python\Python313\python.exe',
 [string]$Bash='D:\msys64\usr\bin\bash.exe',
 [switch]$SkipNative,
 [switch]$SkipUpstream
)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
if(-not $Output){$Output=Join-Path $root ('.cache/verification-'+[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss'))}
$Output=[IO.Path]::GetFullPath($Output)
if(Test-Path -LiteralPath $Output){throw 'Verification output must be a new directory'}
New-Item -ItemType Directory -Path $Output|Out-Null
$checks=[Collections.Generic.List[object]]::new()
$saved=@{}
foreach($name in @('PATH','GOOS','GOARCH','CGO_ENABLED','CC')){$saved[$name]=[Environment]::GetEnvironmentVariable($name,'Process')}
function Run-Check([string]$Name,[scriptblock]$Action){
 $log=Join-Path $Output ($Name+'.txt')
 $timer=[Diagnostics.Stopwatch]::StartNew()
 $status='FAIL'
 try {
  $global:LASTEXITCODE=0
  & $Action *> $log
  if($LASTEXITCODE -ne 0){throw "Command exited with $LASTEXITCODE"}
  $status='PASS'
 } catch {
  Add-Content -LiteralPath $log -Value ('Verification error: '+$_.Exception.Message)
  throw
 } finally {
  $timer.Stop()
  $checks.Add([ordered]@{name=$Name;status=$status;seconds=[math]::Round($timer.Elapsed.TotalSeconds,3);log=($Name+'.txt');sha256=(Get-FileHash -LiteralPath $log -Algorithm SHA256).Hash.ToLower()})
  Write-Output "$status $Name"
 }
}
function Check-Command { if($LASTEXITCODE -ne 0){throw "Command failed ($LASTEXITCODE)"} }
$success=$false
try {
 $env:PATH=(Join-Path $root '.tools/go/bin')+';'+(Join-Path $root '.tools/node-v26.13.1-win-x64')+';'+(Join-Path $Toolchain 'bin')+';'+$env:PATH
 Remove-Item Env:GOOS -ErrorAction SilentlyContinue
 Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
 $env:CGO_ENABLED='1';$env:CC=Join-Path $Toolchain 'bin/gcc.exe'
 Run-Check 'tool-versions' {
  $lock=Get-Content -LiteralPath (Join-Path $root 'upstream/versions.lock.json') -Raw | ConvertFrom-Json
  $go=go version;Check-Command;$go
  if($go -notmatch [regex]::Escape('go'+$lock.toolchain.go)){throw 'Go toolchain differs from lock'}
  $node=node --version;Check-Command;$node
  if($node -ne ('v'+$lock.toolchain.node)){throw 'Node toolchain differs from lock'}
  & $Python --version;Check-Command
  cmake --version;Check-Command
 }
 Run-Check 'source-inputs' { & $Python -B "$PSScriptRoot/fingerprint.py";Check-Command }
 $sourceInputs=Get-Content -LiteralPath (Join-Path $Output 'source-inputs.txt') -Raw | ConvertFrom-Json
 Run-Check 'tools-tests' { & $Python -B -m unittest discover -s tests/tools -v;Check-Command }
 Run-Check 'go-race' { go test -race ./internal/... ./cmd/...;Check-Command }
 Run-Check 'go-vet' { go vet ./internal/... ./cmd/...;Check-Command }
 Run-Check 'frontend' { npm test;Check-Command;npm run build;Check-Command }
 Run-Check 'scripts' { & "$PSScriptRoot/verify-scripts.ps1" -Bash $Bash -Python $Python }
 Run-Check 'certificates' { & $Python -B "$PSScriptRoot/verify-certificates.py" --bash $Bash;Check-Command }
 $ctl=Join-Path $Output 'netgoctl.exe'
 Run-Check 'cli-build' { $env:CGO_ENABLED='0';go build -trimpath -o $ctl ./cmd/netgoctl;Check-Command }
 Run-Check 'deploy-validators' {
  & $Python -B "$PSScriptRoot/verify-deploy.py" --netgoctl $ctl --caddy "$root/.tools/caddy/caddy.exe" --frpc "$root/.tools/frp/frp_0.72.0_windows_amd64/frpc.exe" --frps "$root/.tools/frp/frp_0.72.0_windows_amd64/frps.exe" --output "$Output/deployment"
  Check-Command
 }
 if($SkipNative){$checks.Add(@{name='native';status='NOT-RUN'})}else{
  Run-Check 'native' { & "$PSScriptRoot/build-native.ps1" -SkipDependencies -Toolchain $Toolchain -Python $Python }
 }
 if($SkipUpstream){$checks.Add(@{name='upstream';status='NOT-RUN'})}else{
  Run-Check 'upstream' { & "$PSScriptRoot/build-upstream-windows.ps1" -Toolchain $Toolchain }
 }
 Run-Check 'product-build' { & "$PSScriptRoot/build.ps1" -SkipFrontend -SkipNative -SkipUpstream -SkipChecks -Toolchain $Toolchain -Python $Python }
 Run-Check 'release-manifest' {
  $hash=(Get-FileHash -LiteralPath "$root/dist/linux-amd64/SHA256SUMS" -Algorithm SHA256).Hash.ToLower()
  & $ctl release verify --input "$root/dist/linux-amd64" --sha256 $hash;Check-Command
 }
 Run-Check 'artifact-inputs' { & $Python -B "$PSScriptRoot/verification.py" snapshot --platform windows;Check-Command }
 $finalInputs=& $Python -B "$PSScriptRoot/fingerprint.py" | ConvertFrom-Json
 Check-Command
 if($finalInputs.code_sha256 -ne $sourceInputs.code_sha256){throw 'Source inputs changed during verification; rerun against a stable workspace'}
 $success=$true
} finally {
 $status=if(-not $success){'FAIL'}elseif($SkipNative -or $SkipUpstream){'INCOMPLETE'}else{'PASS'}
 [ordered]@{schema=2;platform='windows';status=$status;generated_utc=[DateTime]::UtcNow.ToString('o');code_sha256=$sourceInputs.code_sha256;checks=$checks.ToArray();real_network_acceptance='NOT-RUN';limits=@('Debian 13 upstream CGO/runtime, systemd, nftables and HAProxy need the target lab','A01-A33, cross-user installation and 72-hour reliability remain NOT-RUN')} | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $Output 'verification.json') -Encoding utf8NoBOM
 foreach($name in $saved.Keys){[Environment]::SetEnvironmentVariable($name,$saved[$name],'Process')}
}
Write-Output "Verification $status. Report: $Output/verification.json"
