[CmdletBinding()]
param(
    [ValidateSet('local', 'central')]
    [string]$Mode = 'local',
    [int]$Port = 8770,
    [string]$DataDir = '',
    [string]$AdminUser = 'admin',
    [string]$AdminPassword = '',
    [switch]$AllowAllIPs,
    [switch]$NoBuild
)

$ErrorActionPreference = 'Stop'
if ($AllowAllIPs -and $Mode -ne 'central') {
    Write-Host 'AllowAllIPs requires login; switching mode to central.'
    $Mode = 'central'
}
$listenHost = if ($AllowAllIPs) { '0.0.0.0' } else { '127.0.0.1' }
$root = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($root)) { $root = Split-Path -Parent $MyInvocation.MyCommand.Path }
$repoRoot = Split-Path -Parent $root
$env:GOCACHE = Join-Path $repoRoot '.gocache'
$env:GOTMPDIR = Join-Path $repoRoot '.gotmp'
New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null
if ([string]::IsNullOrWhiteSpace($DataDir)) { $DataDir = Join-Path $root 'data-dev' }
if ([IO.Path]::IsPathRooted($DataDir)) { $DataDir = [IO.Path]::GetFullPath($DataDir) }
else { $DataDir = [IO.Path]::GetFullPath((Join-Path $root $DataDir)) }
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Set-Location $root

if (-not $NoBuild) {
    Write-Host 'Building frontend from source (core.js/auth-ui.js/infra.js/... -> index.html)...'
    go run -buildvcs=false ./cmd/build
    if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' }
}

$env:QIYUN_AUTH_MODE = if ($Mode -eq 'central') { 'on' } else { 'off' }
$env:QIYUN_ALLOWED_ORIGINS = '127.0.0.1:' + $Port
$env:QIYUN_ADMIN_USER = $AdminUser
if ($Mode -eq 'central') {
    if ([string]::IsNullOrWhiteSpace($AdminPassword)) {
        $secure = Read-Host -AsSecureString 'Initial admin password (first run only, at least 8 chars)'
        $AdminPassword = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
    }
    $env:QIYUN_ADMIN_PASSWORD = $AdminPassword
} else {
    Remove-Item Env:QIYUN_ADMIN_PASSWORD -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "Starting qiyun from source: mode=$Mode listen=${listenHost}:$Port data=$DataDir"
Write-Host "Local URL: http://127.0.0.1:$Port/"
if ($AllowAllIPs) {
    $lanIPs = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike '127.*' -and $_.AddressState -eq 'Preferred' } |
        Select-Object -ExpandProperty IPAddress -Unique)
    foreach ($ip in $lanIPs) { Write-Host "LAN URL:   http://${ip}:$Port/" }
    Write-Host ''
    Write-Host 'WARNING: this is plain HTTP. Use only on a trusted LAN; for production put HTTPS in front.'
    Write-Host "If other computers cannot connect, allow the port in Windows Firewall (admin PowerShell):"
    Write-Host "  New-NetFirewallRule -DisplayName 'Qiyun Dev $Port' -Direction Inbound -Action Allow -Protocol TCP -LocalPort $Port -Profile Private"
    Write-Host 'RDP launch remains local-only: remote browsers must install the thin connector (or use Guacamole).'
}
if ($Mode -eq 'central') {
    Write-Host "Login: $AdminUser (first start uses the password you entered)"
} else {
    Write-Host 'Login: disabled (loopback single-user mode)'
}
Write-Host 'Frontend edits: run  go run ./cmd/build  then refresh the browser.'
Write-Host 'Go edits: press Ctrl+C and rerun this script.'
Write-Host ''

go run -buildvcs=false ./cmd/qiyun -addr ("${listenHost}:" + $Port) -data $DataDir -web $root