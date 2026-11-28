# Only reproducible outputs are eligible. Credentials, signing keys, installed
# engines, site data and Git history are deliberately outside this allowlist.
[CmdletBinding()]
param([switch]$Apply, [switch]$IncludeSDK, [switch]$IncludeTestData, [switch]$IncludeToolchains)

$ErrorActionPreference = 'Stop'
$taskRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
if ((Get-Item -LiteralPath $taskRoot -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
    throw 'Cleanup workspace must not be a junction or symlink'
}
$taskPrefix = $taskRoot.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
$tracked = @(& git -C $taskRoot ls-files)
if ($LASTEXITCODE -ne 0) { throw 'Cannot verify tracked project files' }
$taskTargets = @(
    'node_modules', 'dist', '.cache/native-build', '.cache/review',
    '.cache/toolchain/go-build', '.cache/toolchain/go', '.cache/toolchain/npm-cache',
    '.cache/toolchain/tmp', '.cache/toolchain/gradle', '.cache/toolchain/android-local',
    '.cache/refactor-native', '.cache/refactor-linux', '.cache/refactor-server', '.cache/apple-tests',
    '.cache/refactor-check', '.cache/refactor-native-build', '.cache/audit-linux-build', '.cache/audit-remote-core', '.cache/audit-web-dist',
    'apps/android/.gradle', 'apps/android/.kotlin', 'apps/android/.cxx',
    'apps/android/build', 'apps/android/app/build', 'apps/android/app/.cxx',
    'apps/android/app/libs/netgo-mobile.aar', 'apps/android/app/libs/netgo-mobile-sources.jar',
    'scripts/__pycache__', 'tests/tools/__pycache__', 'tests/__pycache__',
    'apps/windows/tests/__pycache__', 'apps/ui/.cache'
)
if ($IncludeTestData) {
    # Generated installer fixtures; these are not installed station data.
    $taskTargets += @('.cache/installer-lab', '.cache/cleanup-before.json', '.cache/cleanup-after.json',
        '.cache/cleanup-plan.json', '.cache/cleanup-recycled.json', '.cache/cleanup-measurement.json',
        '.cache/audit-probes', '.cache/audit-relay-probe', '.cache/windows-project', '.cache/openssl-build.log')
    $reportRoot = Join-Path $taskRoot '.cache'
    if (Test-Path -LiteralPath $reportRoot) {
        Get-ChildItem -LiteralPath $reportRoot -Directory | Where-Object {
            $_.Name -match '^(review-[0-9]{8}-(native|linux|python|web)|upstream-verified-[a-f0-9]{16})$'
        } | ForEach-Object { $taskTargets += '.cache/' + $_.Name }
        Get-ChildItem -LiteralPath $reportRoot -File | Where-Object { $_.Name -match '^(audit|refactor)-[a-z0-9-]+[.](txt|log)$' } | ForEach-Object {
            $taskTargets += '.cache/' + $_.Name
        }
    }
}
if ($IncludeSDK) {
    $taskTargets += @('.tools/android/ndk', '.tools/android/platforms', '.tools/android/build-tools',
        '.tools/android/cmake', '.tools/openssl-android-arm64', '.tools/bin', '.tools/python-libs')
}

if ($IncludeToolchains) {
    $taskTargets += '.cache/audit-tools'
    $taskTargets += @('.tools/go', '.tools/android', '.tools/native-json', '.tools/openssl-native', '.tools/openssl-source', '.tools/openssl-validation',
        '.tools/wintun-package', '.tools/wintun-0.14.1.zip')
    $toolRoot = Join-Path $taskRoot '.tools'
    if (Test-Path -LiteralPath $toolRoot) {
        Get-ChildItem -LiteralPath $toolRoot -Directory -Filter 'node-v*-win-*' | ForEach-Object {
            if ($_.Name -match '^node-v[0-9]+[.][0-9]+[.][0-9]+-win-(x64|arm64)$') {
                $taskTargets += '.tools/' + $_.Name
            }
        }
    }
    Get-ChildItem -LiteralPath (Join-Path $taskRoot '.cache') -File -Filter '*.zip' -ErrorAction SilentlyContinue | Where-Object {
        $_.Name -match '^(go[0-9]+[.][0-9]+[.][0-9]+[.]windows-(amd64|arm64)|node-v[0-9]+[.][0-9]+[.][0-9]+-win-(x64|arm64))[.]zip$'
    } | ForEach-Object { $taskTargets += '.cache/' + $_.Name }
}
# Parent entries supersede nested SDK/cache entries; never count or remove twice.
$taskTargets = @($taskTargets | Sort-Object -Unique | Where-Object {
    $candidate = $_.Replace('\', '/').TrimEnd('/')
    -not @($taskTargets | Where-Object {
        $parent = $_.Replace('\', '/').TrimEnd('/')
        $parent -ne $candidate -and $candidate.StartsWith($parent + '/', [StringComparison]::OrdinalIgnoreCase)
    }).Count
})

function Get-TaskTarget([string]$Relative, [bool]$CheckRepository = $true) {
    $trackedPrefix = $Relative.Replace('\', '/').TrimEnd('/') + '/'
    if ($tracked | Where-Object { $_ -eq $Relative -or $_.StartsWith($trackedPrefix) }) {
        throw "Cleanup target contains tracked source: $Relative"
    }
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
    # Empty directory remnants reclaim no file bytes; leave them alone.
    if ($item.PSIsContainer -and $bytes -eq 0) { return }
    if ($CheckRepository -and $Relative.Replace('\', '/') -in @('.tools/openssl-source','.tools/openssl-validation') -and (Test-Path -LiteralPath (Join-Path $target '.git'))) {
        $changed = @(git -C $target status --porcelain --untracked-files=normal)
        if ($LASTEXITCODE -ne 0 -or $changed.Count) { throw "OpenSSL source contains uncommitted work: $target" }
    }
    [PSCustomObject]@{ Path = $target; Bytes = [long]$bytes }
}

$taskPlan = @($taskTargets | ForEach-Object { Get-TaskTarget $_ })
if ($Apply) {
    # CIM omits image paths for some elevated/test processes. Limited-query
    # access still identifies them without requesting debug privileges.
    if (-not ('NetGoCleanup.ProcessImage' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;
namespace NetGoCleanup {
    public static class ProcessImage {
        [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(uint access, bool inherit, uint id);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern bool QueryFullProcessImageName(IntPtr process, uint flags, StringBuilder path, ref uint size);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr process);
        public static string Read(uint id) {
            var process = OpenProcess(0x1000, false, id);
            if (process == IntPtr.Zero) return "";
            try {
                var path = new StringBuilder(32768); uint size = 32768;
                return QueryFullProcessImageName(process, 0, path, ref size) ? path.ToString() : "";
            } finally { CloseHandle(process); }
        }
    }
}
'@
    }
    # Java and Node may live outside the workspace while holding build outputs.
    $taskProcesses = @(Get-CimInstance Win32_Process | Where-Object {
        $_.ProcessId -ne $PID -and $_.Name -match '^(go|compile|link|gofmt|gcc|g[+][+]|clang.*|java|node|ninja|cmake|NetGo|netbird)[.]exe$'
    } | ForEach-Object {
        $image = $_.ExecutablePath
        if (-not $image) { $image = [NetGoCleanup.ProcessImage]::Read($_.ProcessId) }
        [PSCustomObject]@{ Name = $_.Name; ProcessId = $_.ProcessId; ExecutablePath = $image; CommandLine = $_.CommandLine }
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
            # A partial removal may already have deleted the nested .git data;
            # only the first attempt can meaningfully inspect that repository.
            $checked = Get-TaskTarget ([IO.Path]::GetRelativePath($taskRoot, $item.Path)) ($attempt -eq 0)
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
