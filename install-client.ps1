#requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('Menu','Install','Uninstall','Status')][string]$Action='Menu',
    [string]$PackageRoot,
    [string]$Package,
    [string]$Sha256,
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$')][string]$Version='0.2.4-dev',
    [switch]$Plan,
    [switch]$Yes
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$repository='https://github.com/sck03/NetGo'
$maintenance=Join-Path $env:ProgramFiles 'NetGoNative\maintenance'
$scratch=$null
if($PackageRoot -and $Package){throw 'PackageRoot and Package cannot be combined'}
if($Sha256 -and -not $Package){throw 'Sha256 requires Package'}
if(($PackageRoot -or $Package) -and $Action -ne 'Install'){throw 'Package parameters apply only to Install'}
if($Action -eq 'Menu'){
    if($Plan){$Action='Install'}
    else{
        Write-Host 'NetGo Windows client (Linux server: install-server.sh)'
        Write-Host '1) Install  2) Status  3) Uninstall (retain profiles)  0) Exit'
        switch(Read-Host 'Select [1]'){
            '' {$Action='Install'} '1' {$Action='Install'} '2' {$Action='Status'}
            '3' {$Action='Uninstall'} '0' {return} default {throw 'Invalid selection'}
        }
    }
}
if($Action -eq 'Status'){
    Get-CimInstance Win32_Service -Filter "Name='NetGoNetBird'" | Select-Object Name,State,StartMode
    $desktop=Join-Path $env:LOCALAPPDATA 'NetGoNative\bin\netgo-desktop.exe'
    [pscustomobject]@{Installed=(Test-Path -LiteralPath $desktop);Desktop=$desktop;Maintenance=$maintenance}
    return
}
if($Action -eq 'Uninstall'){
    $uninstaller=Join-Path $maintenance 'deploy\windows\uninstall.ps1'
    if(-not(Test-Path -LiteralPath $uninstaller)){
        $uninstaller=Join-Path $PSScriptRoot 'deploy\windows\uninstall.ps1'
    }
    if(-not(Test-Path -LiteralPath $uninstaller)){throw 'Local uninstaller missing; inspect installation before removing anything'}
    if(-not $Plan -and -not $Yes){
        if((Read-Host 'Remove NetGo programs/service, retaining profiles and credentials? Type yes') -ne 'yes'){return}
    }
    & $uninstaller -Plan:$Plan
    return
}
if($Plan -and -not $PackageRoot){
    [pscustomobject]@{Action='Install';Platform='Windows x64';Version=$Version;Package="NetGo-$Version-windows-amd64.zip";Downloads=$false;Changes=$false;Requires='Elevation as intended desktop user for actual install'}
    return
}
function Get-NetGoDigest([string]$Manifest,[string]$Name){
    $matchesFound=@(Get-Content -LiteralPath $Manifest | Where-Object {$_ -match ('^[0-9a-fA-F]{64}  '+[regex]::Escape($Name)+'$')})
    if($matchesFound.Count -ne 1){throw 'Package checksum is missing or duplicated'}
    return $matchesFound[0].Substring(0,64).ToLowerInvariant()
}
function Expand-NetGoPackage([string]$Archive,[string]$Destination){
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip=[IO.Compression.ZipFile]::OpenRead($Archive)
    try{
        $seen=New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        $total=[long]0
        if($zip.Entries.Count -gt 200000){throw 'Too many package entries'}
        foreach($entry in $zip.Entries){
            $name=$entry.FullName.TrimEnd('/')
            if(-not $name -or $name.StartsWith('/') -or $name.Contains('\') -or $name.Contains(':') -or
                $name -match '(^|/)(\.|\.\.)(/|$)' -or $name.Contains('//') -or -not $seen.Add($name)){
                throw 'Unsafe or duplicate package path'
            }
            foreach($part in $name.Split('/')){
                if($part -match '[\x00-\x1f<>"|?*]' -or $part.EndsWith('.') -or $part.EndsWith(' ') -or
                    $part -match '^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(\.|$)'){throw 'Invalid Windows package path'}
            }
            $kind=($entry.ExternalAttributes -shr 16) -band 61440
            if($kind -notin @(0,32768,16384)){throw 'Package links and special files are forbidden'}
            $total+=$entry.Length
            if($total -gt 2GB){throw 'Package exceeds extraction limit'}
        }
        foreach($entry in $zip.Entries){
            $path=Join-Path $Destination $entry.FullName
            if($entry.FullName.EndsWith('/')){[IO.Directory]::CreateDirectory($path)|Out-Null;continue}
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($path))|Out-Null
            $inputStream=$entry.Open()
            try{
                $outputStream=[IO.File]::Open($path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
                try{$inputStream.CopyTo($outputStream)}finally{$outputStream.Dispose()}
            }finally{$inputStream.Dispose()}
        }
    }finally{$zip.Dispose()}
}
function Test-NetGoPackage([string]$Root){
    $manifest=Join-Path $Root 'SHA256SUMS'
    if(-not(Test-Path -LiteralPath $manifest)){throw 'Package manifest missing'}
    $seen=New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
    foreach($line in Get-Content -LiteralPath $manifest){
        if($line -notmatch '^([0-9a-f]{64})  ([a-zA-Z0-9_./-]+)$'){throw 'Invalid package manifest'}
        $expected=$matches[1];$name=$matches[2]
        if($name.StartsWith('/') -or $name -match '(^|/)\.\.(/|$)' -or -not $seen.Add($name)){throw 'Unsafe package checksum path'}
        $file=Join-Path $Root $name
        if(-not(Test-Path -LiteralPath $file -PathType Leaf) -or (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected){throw "Package checksum mismatch: $name"}
    }
    foreach($file in Get-ChildItem -LiteralPath $Root -Recurse -File){
        $name=$file.FullName.Substring($Root.TrimEnd('\','/').Length+1).Replace('\','/')
        if($name -ne 'SHA256SUMS' -and -not $seen.Contains($name)){throw "Unverified package file: $name"}
    }
}
try{
    if(-not $PackageRoot){
        $scratch=Join-Path ([IO.Path]::GetTempPath()) ('NetGoClient.'+[guid]::NewGuid().ToString('N'))
        [IO.Directory]::CreateDirectory($scratch)|Out-Null
        $name="NetGo-$Version-windows-amd64.zip"
        [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
        if(-not $Package){
            $Package=Join-Path $scratch $name
            Invoke-WebRequest -UseBasicParsing -Uri "$repository/releases/download/v$Version/$name" -OutFile $Package
            $manifest=Join-Path $scratch 'SHA256SUMS'
            Invoke-WebRequest -UseBasicParsing -Uri "$repository/releases/download/v$Version/SHA256SUMS" -OutFile $manifest
            $Sha256=Get-NetGoDigest $manifest $name
        }
        if($Sha256 -notmatch '^[0-9a-fA-F]{64}$'){throw 'An offline Package requires its trusted Sha256'}
        if((Get-FileHash -LiteralPath $Package -Algorithm SHA256).Hash -ne $Sha256){throw 'Download checksum mismatch'}
        $PackageRoot=Join-Path $scratch 'package'
        [IO.Directory]::CreateDirectory($PackageRoot)|Out-Null
        Expand-NetGoPackage $Package $PackageRoot
        Test-NetGoPackage $PackageRoot
    }
    $PackageRoot=(Resolve-Path -LiteralPath $PackageRoot).Path
    $installer=Join-Path $PackageRoot 'deploy\windows\install.ps1'
    if(-not(Test-Path -LiteralPath $installer)){throw 'Complete Windows package required'}
    & $installer -PackageRoot $PackageRoot -Plan:$Plan
}finally{
    if($scratch -and (Test-Path -LiteralPath $scratch)){
        $resolved=[IO.Path]::GetFullPath($scratch)
        $parent=[IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\','/')+[IO.Path]::DirectorySeparatorChar
        if(-not $resolved.StartsWith($parent,[StringComparison]::OrdinalIgnoreCase) -or
            [IO.Path]::GetFileName($resolved) -notmatch '^NetGoClient\.[0-9a-f]{32}$'){throw 'Refusing cleanup outside this installer temporary directory'}
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}
