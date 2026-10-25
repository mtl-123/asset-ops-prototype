[CmdletBinding()]
param(
    [string]$Central = 'http://127.0.0.1:8770',
    [string]$DataDir = ''
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($root)) { $root = Split-Path -Parent $MyInvocation.MyCommand.Path }
$repoRoot = Split-Path -Parent $root
$env:GOCACHE = Join-Path $repoRoot '.gocache'
$env:GOTMPDIR = Join-Path $repoRoot '.gotmp'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null
if ([string]::IsNullOrWhiteSpace($DataDir)) { $DataDir = Join-Path $root 'connector-dev' }
if ([IO.Path]::IsPathRooted($DataDir)) { $DataDir = [IO.Path]::GetFullPath($DataDir) }
else { $DataDir = [IO.Path]::GetFullPath((Join-Path $root $DataDir)) }
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Set-Location $root
Write-Host "Starting connector from source: central=$Central data=$DataDir"
Write-Host 'Listening on http://127.0.0.1:8766 (must be free).'
go run -buildvcs=false ./cmd/connector -central $Central -data $DataDir