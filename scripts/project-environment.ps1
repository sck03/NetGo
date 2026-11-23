function Set-NetGoProjectEnvironment {
    $taskRoot=Split-Path -Parent $PSScriptRoot
    $taskCache=Join-Path $taskRoot '.cache\toolchain'
    $env:GOCACHE=Join-Path $taskCache 'go-build'
    $env:GOPATH=Join-Path $taskCache 'go'
    $env:GOMODCACHE=Join-Path $env:GOPATH 'pkg\mod'
    $env:GOTMPDIR=Join-Path $taskCache 'tmp'
    $env:TEMP=$env:GOTMPDIR
    $env:TMP=$env:GOTMPDIR
    $env:npm_config_cache=Join-Path $taskCache 'npm-cache'
    foreach($taskPath in @($env:GOCACHE,$env:GOMODCACHE,$env:GOTMPDIR,$env:npm_config_cache)){
        [IO.Directory]::CreateDirectory($taskPath)|Out-Null
    }
}
Set-NetGoProjectEnvironment
