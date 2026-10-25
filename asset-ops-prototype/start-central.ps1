[CmdletBinding()]
param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot 'central.config.json')
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
    $scriptDir = $PSScriptRoot
    if ([string]::IsNullOrWhiteSpace($scriptDir)) { $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
    $ConfigPath = Join-Path $scriptDir 'central.config.json'
}
if (-not (Test-Path -LiteralPath $ConfigPath)) { throw "central config not found: $ConfigPath" }
$config = Get-Content -LiteralPath $ConfigPath -Raw | ConvertFrom-Json
$root = Split-Path -Parent (Resolve-Path -LiteralPath $ConfigPath).Path
$exe = Join-Path $root 'qiyun.exe'
if (-not (Test-Path -LiteralPath $exe)) { throw "qiyun.exe not found in $root" }
$dataDir = [string]$config.data_dir
if ([string]::IsNullOrWhiteSpace($dataDir)) { $dataDir = Join-Path (Split-Path -Parent $root) 'QiyunAssetOps\data' }
$webRoot = [string]$config.web_root
if ([string]::IsNullOrWhiteSpace($webRoot)) { $webRoot = $root }
$logDir = Join-Path (Split-Path -Parent $dataDir) 'logs'
New-Item -ItemType Directory -Force -Path $dataDir, $logDir | Out-Null
$logPath = Join-Path $logDir 'qiyun.log'
$launcherLog = Join-Path $logDir 'launcher.log'
if ((Test-Path -LiteralPath $logPath) -and (Get-Item -LiteralPath $logPath).Length -gt 10MB) {
    Move-Item -LiteralPath $logPath -Destination ($logPath + '.1') -Force
}

$env:QIYUN_AUTH_MODE = 'on'
$env:QIYUN_ADMIN_USER = if ([string]::IsNullOrWhiteSpace([string]$config.admin_user)) { 'admin' } else { [string]$config.admin_user }
if ($config.PSObject.Properties.Name -contains 'admin_password' -and -not [string]::IsNullOrWhiteSpace([string]$config.admin_password)) {
    $env:QIYUN_ADMIN_PASSWORD = [string]$config.admin_password
} else {
    Remove-Item Env:QIYUN_ADMIN_PASSWORD -ErrorAction SilentlyContinue
}
$origins = [string]$config.allowed_origins
if ([string]::IsNullOrWhiteSpace($origins) -and -not [string]::IsNullOrWhiteSpace([string]$config.public_url)) {
    $public = [Uri]([string]$config.public_url)
    $origins = $public.Host
    if (-not $public.IsDefaultPort) { $origins = $origins + ':' + $public.Port }
}
$env:QIYUN_ALLOWED_ORIGINS = $origins
if (-not [string]::IsNullOrWhiteSpace([string]$config.allowed_cidrs)) { $env:QIYUN_ALLOWED_CIDRS = [string]$config.allowed_cidrs }
$env:QIYUN_CONNECTOR_URL = if ([string]::IsNullOrWhiteSpace([string]$config.connector_url)) { 'http://127.0.0.1:8766' } else { [string]$config.connector_url }
$env:QIYUN_CONNECTOR_DOWNLOAD = if ([string]::IsNullOrWhiteSpace([string]$config.connector_download)) { '/downloads/QiyunConnector.zip' } else { [string]$config.connector_download }

if (-not [string]::IsNullOrWhiteSpace([string]$config.guacamole_url)) {
    $env:QIYUN_GUACAMOLE_URL = [string]$config.guacamole_url
    $env:QIYUN_GUACAMOLE_USER = [string]$config.guacamole_user
    $env:QIYUN_GUACAMOLE_PASSWORD = [string]$config.guacamole_password
    if (-not [string]::IsNullOrWhiteSpace([string]$config.guacamole_data_source)) { $env:QIYUN_GUACAMOLE_DATA_SOURCE = [string]$config.guacamole_data_source }
    if (-not [string]::IsNullOrWhiteSpace([string]$config.guacamole_rdp_security)) { $env:QIYUN_GUACAMOLE_RDP_SECURITY = [string]$config.guacamole_rdp_security }
    if (-not [string]::IsNullOrWhiteSpace([string]$config.guacamole_tls_seclevel)) { $env:QIYUN_GUACAMOLE_TLS_SECLEVEL = [string]$config.guacamole_tls_seclevel }
    if (-not [string]::IsNullOrWhiteSpace([string]$config.guacamole_domain)) { $env:QIYUN_GUACAMOLE_DOMAIN = [string]$config.guacamole_domain }
}
$metrics = if ([string]::IsNullOrWhiteSpace([string]$config.metrics_interval)) { '60s' } else { [string]$config.metrics_interval }
$listen = if ([string]::IsNullOrWhiteSpace([string]$config.listen)) { '127.0.0.1:8765' } else { [string]$config.listen }

"$(Get-Date -Format o) starting qiyun listen=$listen data=$dataDir web=$webRoot" | Out-File -LiteralPath $launcherLog -Append -Encoding utf8
$arguments = '-addr "{0}" -data "{1}" -web "{2}" -metrics-interval "{3}"' -f $listen, $dataDir, $webRoot, $metrics
$process = Start-Process -FilePath $exe -ArgumentList $arguments -PassThru -Wait -WindowStyle Hidden `
    -RedirectStandardOutput (Join-Path $logDir 'qiyun.out.log') `
    -RedirectStandardError $logPath
"$(Get-Date -Format o) qiyun exited with code $($process.ExitCode)" | Out-File -LiteralPath $launcherLog -Append -Encoding utf8
exit $process.ExitCode