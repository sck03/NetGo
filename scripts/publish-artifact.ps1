function Publish-NetGoArtifact {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$Source, [Parameter(Mandatory)][string]$Destination)
    $taskSource = [IO.Path]::GetFullPath($Source)
    $taskDestination = [IO.Path]::GetFullPath($Destination)
    if (-not (Test-Path -LiteralPath $taskSource -PathType Leaf)) { throw "Build output missing: $taskSource" }
    if (Test-Path -LiteralPath $taskDestination) {
        if (-not (Test-Path -LiteralPath $taskDestination -PathType Leaf)) { throw "Artifact target is not a file: $taskDestination" }
        if ((Get-FileHash -LiteralPath $taskSource -Algorithm SHA256).Hash -eq (Get-FileHash -LiteralPath $taskDestination -Algorithm SHA256).Hash) {
            return # A running Windows image may be locked even when its bytes are unchanged.
        }
    }
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($taskDestination)) | Out-Null
    $taskTemporary = $taskDestination + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    $taskBackupRoot = Join-Path (Split-Path -Parent $PSScriptRoot) '.cache/artifact-previous'
    [IO.Directory]::CreateDirectory($taskBackupRoot) | Out-Null
    $taskBackup = Join-Path $taskBackupRoot ([Guid]::NewGuid().ToString('N') + '-' + [IO.Path]::GetFileName($taskDestination))
    try {
        [IO.File]::Copy($taskSource, $taskTemporary, $false)
        if ([IO.File]::Exists($taskDestination)) {
            [IO.File]::Replace($taskTemporary, $taskDestination, $taskBackup)
        } else {
            [IO.File]::Move($taskTemporary, $taskDestination)
        }
    } catch {
        throw "Cannot publish changed artifact '$taskDestination'. Close the project process using this file, then rebuild. The existing artifact is retained. $($_.Exception.Message)"
    } finally {
        if ([IO.File]::Exists($taskTemporary)) { Remove-Item -LiteralPath $taskTemporary -Force }
        if ([IO.File]::Exists($taskBackup)) {
            try { Remove-Item -LiteralPath $taskBackup -Force -ErrorAction Stop }
            catch { Write-Warning "Previous running artifact retained at '$taskBackup'. Restart its project process to use the newly built file." }
        }
    }
}
