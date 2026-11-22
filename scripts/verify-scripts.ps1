param([string]$Bash='D:\msys64\usr\bin\bash.exe',[string]$Python='python')
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $root
$powershell=@('scripts/build.ps1','scripts/build-native.ps1','scripts/build-upstream-windows.ps1','scripts/prepare-native.ps1','scripts/verify.ps1','scripts/verify-scripts.ps1','deploy/windows/common.ps1','deploy/windows/install.ps1','deploy/windows/uninstall.ps1')
foreach($file in $powershell){
 $tokens=$null;$errors=$null
 [void][System.Management.Automation.Language.Parser]::ParseFile((Join-Path $root $file),[ref]$tokens,[ref]$errors)
 if($errors.Count){throw "PowerShell syntax failed: $file"}
 Write-Output "PASS PowerShell syntax: $file"
}
. (Join-Path $root 'deploy/windows/common.ps1')
$expected='C:\Program Files\NetGoNative\engine\netbird.exe'
if(-not(Test-NetGoServiceImage ('"'+$expected+'" service run') $expected)){throw 'Valid service image rejected'}
foreach($command in @(('"'+$expected+'.other" service run'),('"C:\other\netbird.exe" --config "'+$expected+'"'))){if(Test-NetGoServiceImage $command $expected){throw 'Foreign service image accepted'}}
Write-Output 'PASS precise Windows service image ownership'
$env:PATH=(Split-Path -Parent $Bash)+';'+$env:PATH
foreach($file in @('scripts/build.sh','scripts/build-upstream.sh','deploy/linux/install.sh','deploy/linux/uninstall.sh','deploy/linux/switch-release.sh','deploy/linux/deploy-certificate.sh')){
 & $Bash -n $file;if($LASTEXITCODE -ne 0){throw "Bash syntax failed: $file"};Write-Output "PASS Bash syntax: $file"
}
& $Bash deploy/linux/install.sh --plan;if($LASTEXITCODE -ne 0){throw 'Linux install plan failed'}
& $Bash deploy/linux/uninstall.sh --plan;if($LASTEXITCODE -ne 0){throw 'Linux uninstall plan failed'}
& $Bash deploy/linux/deploy-certificate.sh --plan control control.site.example;if($LASTEXITCODE -ne 0){throw 'Certificate hook plan failed'}
& $Python -B -c 'import ast,pathlib; files=[p for root in ("scripts","tests/network","tests/tools","apps/windows/tests") for p in pathlib.Path(root).glob("*.py")]; [(ast.parse(p.read_text(encoding="utf-8")), print("PASS Python syntax:",p)) for p in files]'
if($LASTEXITCODE -ne 0){throw 'Python syntax failed'}
$plan=& (Join-Path $root 'deploy/windows/install.ps1') -Plan
$plan.OwnerSID='[redacted]';$plan.UI='[user]/NetGoNative/bin';$plan.Engine='[ProgramFiles]/NetGoNative/engine'
$plan|ConvertTo-Json
Write-Output 'PASS script syntax, ownership cases and installation plans. Actual installation/rollback NOT-RUN.'
