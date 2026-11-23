$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '../../scripts/publish-artifact.ps1')
$taskDirectory = Join-Path ([IO.Path]::GetTempPath()) ('netgo-publish-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($taskDirectory) | Out-Null
$taskSource = Join-Path $taskDirectory 'source.bin'
$taskTarget = Join-Path $taskDirectory 'target.bin'
try {
    [IO.File]::WriteAllText($taskSource, 'original')
    Publish-NetGoArtifact $taskSource $taskTarget
    $taskLock = [IO.File]::Open($taskTarget, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        Publish-NetGoArtifact $taskSource $taskTarget
        [IO.File]::WriteAllText($taskSource, 'changed')
        $taskRejected = $false
        try { Publish-NetGoArtifact $taskSource $taskTarget } catch { $taskRejected = $true }
        if (-not $taskRejected) { throw 'Changed locked artifact was not rejected' }
        if ([IO.File]::ReadAllText($taskTarget) -ne 'original') { throw 'Failed publish damaged original artifact' }
    } finally { $taskLock.Dispose() }
    Publish-NetGoArtifact $taskSource $taskTarget
    if ([IO.File]::ReadAllText($taskTarget) -ne 'changed') { throw 'Unlocked changed artifact was not published' }
    if (@(Get-ChildItem -LiteralPath $taskDirectory -Filter '*.tmp').Count) { throw 'Temporary output leaked' }
    Write-Output 'PASS artifact publication: locked identical, locked changed, preserved original, unlocked replacement'
} finally {
    foreach ($taskFile in @($taskSource, $taskTarget)) { if (Test-Path -LiteralPath $taskFile) { Remove-Item -LiteralPath $taskFile -Force } }
    [IO.Directory]::Delete($taskDirectory, $false)
}
