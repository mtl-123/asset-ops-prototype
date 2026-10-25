[CmdletBinding()]
param(
    [switch]$Test
)

$ErrorActionPreference = 'Stop'
$scriptDir = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($scriptDir)) { $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$root = Split-Path -Parent $scriptDir
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $root '.gocache' }
if (-not $env:GOTMPDIR) { $env:GOTMPDIR = Join-Path $root '.gotmp' }
Push-Location $scriptDir
try {
    if ($Test) {
        go test -buildvcs=false ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    }
    go run -buildvcs=false ./cmd/build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
    go build -buildvcs=false -o qiyun.exe ./cmd/qiyun
    if ($LASTEXITCODE -ne 0) { throw 'qiyun.exe build failed.' }
    go build -buildvcs=false -ldflags '-H=windowsgui' -o qiyun-launcher.exe ./cmd/launcher
    if ($LASTEXITCODE -ne 0) { throw 'qiyun-launcher.exe build failed.' }
    go build -buildvcs=false -ldflags '-H=windowsgui' -o qiyun-connector.exe ./cmd/connector
    if ($LASTEXITCODE -ne 0) { throw 'qiyun-connector.exe build failed.' }
    Write-Host 'Built qiyun.exe, qiyun-launcher.exe, qiyun-connector.exe and index.html.'
}
finally {
    Pop-Location
}