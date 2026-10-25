[CmdletBinding()]
param(
    [switch]$SkipBuild,
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'
$scriptDir = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($scriptDir)) { $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
if ([string]::IsNullOrWhiteSpace($OutputDir)) { $OutputDir = Join-Path $scriptDir 'dist' }
$root = Split-Path -Parent $scriptDir
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $root '.gocache' }
if (-not $env:GOTMPDIR) { $env:GOTMPDIR = Join-Path $root '.gotmp' }
Push-Location $scriptDir
try {
    if (-not $SkipBuild) {
        & (Join-Path $scriptDir 'build-qiyun.ps1')
        if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
        & (Join-Path $scriptDir 'package-connector.ps1') -SkipBuild
        if ($LASTEXITCODE -ne 0) { throw 'Connector package failed.' }
    }
    $required = @('qiyun.exe', 'index.html', 'start-central.ps1', 'install-central.ps1', 'uninstall-central.ps1', 'central.config.example.json', 'IT-CHECKLIST.md')
    foreach ($name in $required) {
        if (-not (Test-Path -LiteralPath (Join-Path $scriptDir $name))) { throw "Missing $name" }
    }
    $connectorZip = Join-Path $scriptDir 'downloads\QiyunConnector.zip'
    if (-not (Test-Path -LiteralPath $connectorZip)) { throw 'downloads\QiyunConnector.zip is missing. Run package-connector.ps1 first.' }

    $stage = Join-Path $OutputDir 'QiyunCentral'
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage, (Join-Path $stage 'downloads') | Out-Null
    foreach ($name in $required) {
        Copy-Item -LiteralPath (Join-Path $scriptDir $name) -Destination $stage
    }
    New-Item -ItemType Directory -Force -Path (Join-Path $stage 'vendor') | Out-Null
    Copy-Item -LiteralPath (Join-Path $scriptDir 'vendor\xterm') -Destination (Join-Path $stage 'vendor\xterm') -Recurse
    Copy-Item -LiteralPath (Join-Path $scriptDir 'assets') -Destination (Join-Path $stage 'assets') -Recurse
    Copy-Item -LiteralPath $connectorZip -Destination (Join-Path $stage 'downloads\QiyunConnector.zip') -Force

    $zip = Join-Path $OutputDir 'QiyunCentral.zip'
    if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal
    Write-Host "Central package: $zip"
    Get-Item -LiteralPath $zip | Select-Object FullName, Length, LastWriteTime
}
finally {
    Pop-Location
}