# Only reproducible outputs are eligible. Credentials, signing keys, installed
# engines, site data and Git history are deliberately outside this allowlist.
[CmdletBinding()]
param([switch]$Apply, [switch]$IncludeSDK)

$ErrorActionPreference = 'Stop'
$taskRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
if ((Get-Item -LiteralPath $taskRoot -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
    throw 'Cleanup workspace must not be a junction or symlink'
}
$taskPrefix = $taskRoot.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
$taskTargets = @(
    'node_modules', 'dist',
    '.cache/toolchain/go-build', '.cache/toolchain/go', '.cache/toolchain/npm-cache',
    '.cache/toolchain/tmp', '.cache/toolchain/gradle', '.cache/toolchain/android-local',
    '.cache/refactor-native', '.cache/refactor-linux',
    'apps/android/.gradle', 'apps/android/.kotlin', 'apps/android/.cxx',
    'apps/android/build', 'apps/android/app/build', 'apps/android/app/.cxx',
    'apps/android/app/libs/netgo-mobile.aar', 'apps/android/app/libs/netgo-mobile-sources.jar',
    'scripts/__pycache__', 'tests/tools/__pycache__'
)
if ($IncludeSDK) {
    $taskTargets += @('.tools/android/ndk', '.tools/android/platforms', '.tools/android/build-tools',
        '.tools/android/cmake', '.tools/openssl-android-arm64', '.tools/bin', '.tools/python-libs')
}

function Get-TaskTarget([string]$Relative) {
    $target = [IO.Path]::GetFullPath((Join-Path $taskRoot $Relative))
    if (-not $target.StartsWith($taskPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Cleanup target outside workspace: $Relative"
    }
    if (-not (Test-Path -LiteralPath $target)) { return }
    # Refuse junctions/symlinks at the target, in its parents, or below it.
    $current = Get-Item -LiteralPath $target -Force
    while ($current -and $current.FullName -ne $taskRoot) {
        if ($current.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Linked cleanup target: $Relative" }
        $current = if ($current.PSIsContainer) { $current.Parent } else { $current.Directory }
    }
    $item = Get-Item -LiteralPath $target -Force
    if ($item.PSIsContainer) {
        $bytes = 0L
        Get-ChildItem -LiteralPath $target -Force -Recurse | ForEach-Object {
            if ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Linked content in cleanup target: $Relative" }
            if (-not $_.PSIsContainer) { $bytes += $_.Length }
        }
    } else { $bytes = $item.Length }
    [PSCustomObject]@{ Path = $target; Bytes = [long]$bytes }
}

$taskPlan = @($taskTargets | ForEach-Object { Get-TaskTarget $_ })
if ($Apply) {
    # Java and Node may live outside the workspace while holding build outputs.
    $taskProcesses = @(Get-CimInstance Win32_Process | Where-Object {
        $_.ProcessId -ne $PID -and $_.Name -match '^(go|compile|link|gofmt|gcc|g[+][+]|clang.*|java|node|ninja|cmake|NetGo|netbird)[.]exe$'
    })
    foreach ($item in $taskPlan) {
        foreach ($process in $taskProcesses) {
            $command = (($process.ExecutablePath + ' ' + $process.CommandLine) -replace '/', '\')
            if ($command.IndexOf(($item.Path -replace '/', '\'), [StringComparison]::OrdinalIgnoreCase) -ge 0) {
                throw "Cleanup target in use by $($process.Name) (PID $($process.ProcessId)): $($item.Path)"
            }
        }
    }
    foreach ($item in $taskPlan) {
        for ($attempt = 0; $attempt -lt 3; $attempt++) {
            $checked = Get-TaskTarget ([IO.Path]::GetRelativePath($taskRoot, $item.Path))
            if (-not $checked) { break }
            $removalErrors = @()
            # Windows may briefly retain empty directories after deleting their
            # children. Finish the pass, then retry; never report a partial delete as done.
            Remove-Item -LiteralPath $checked.Path -Recurse -Force -ErrorAction SilentlyContinue -ErrorVariable removalErrors
            if (-not (Test-Path -LiteralPath $checked.Path)) { break }
            if ($attempt -eq 2) { throw "Cleanup incomplete: $($checked.Path); $($removalErrors | Select-Object -First 1)" }
            Start-Sleep -Milliseconds (200 * ($attempt + 1))
        }
    }
}
$taskPlan | Format-Table Path, Bytes -AutoSize
[PSCustomObject]@{ Applied = [bool]$Apply; Targets = $taskPlan.Count; LogicalBytes = [long](($taskPlan | Measure-Object Bytes -Sum).Sum) }
