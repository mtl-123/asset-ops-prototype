[CmdletBinding()]
param(
    [string]$SourceDir = $PSScriptRoot,
    [string]$TargetDir = 'C:\Program Files\QiyunAssetOps',
    [string]$DataDir = 'C:\ProgramData\QiyunAssetOps\data',
    [string]$PublicUrl = 'https://ops.example.com',
    [string]$Listen = '127.0.0.1:8765',
    [string]$AdminUser = 'admin',
    [string]$AdminPassword = '',
    [string]$GuacamoleUrl = '',
    [string]$GuacamoleUser = '',
    [string]$GuacamolePassword = '',
    [string]$GuacamoleDataSource = '',
    [string]$GuacamoleRdpSecurity = 'nla',
    [switch]$NoStart
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($SourceDir)) { $SourceDir = $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($SourceDir)) { $SourceDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$SourceDir = (Resolve-Path -LiteralPath $SourceDir).Path
if (-not $NoStart) {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run install-central.ps1 from an elevated PowerShell (Run as Administrator).'
    }
}
if (-not (Test-Path -LiteralPath (Join-Path $SourceDir 'qiyun.exe'))) { throw "qiyun.exe not found in $SourceDir" }
if ([string]::IsNullOrWhiteSpace($AdminPassword)) {
    $secure = Read-Host -AsSecureString 'Initial administrator password (at least 8 characters)'
    $AdminPassword = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
}
if ($AdminPassword.Length -lt 8) { throw 'Administrator password must be at least 8 characters.' }
if ([string]::IsNullOrWhiteSpace($PublicUrl) -or $PublicUrl -eq 'https://ops.example.com') {
    Write-Warning 'PublicUrl is still the example https://ops.example.com. Update DNS/HTTPS before production use.'
}
$public = [Uri]$PublicUrl
$origins = $public.Host
if (-not $public.IsDefaultPort) { $origins = $origins + ':' + $public.Port }

New-Item -ItemType Directory -Force -Path $TargetDir, $DataDir | Out-Null
$taskName = 'QiyunAssetOps'
$existing = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
if ($existing) {
    Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
}
Get-Process -Name 'qiyun' -ErrorAction SilentlyContinue | ForEach-Object {
    try {
        if ($_.Path -and $_.Path.StartsWith($TargetDir, [StringComparison]::OrdinalIgnoreCase)) { Stop-Process -Id $_.Id -Force }
    } catch { }
}
$items = @('qiyun.exe', 'index.html', 'vendor', 'assets', 'downloads', 'start-central.ps1', 'uninstall-central.ps1', 'IT-CHECKLIST.md', 'central.config.example.json')
foreach ($item in $items) {
    $source = Join-Path $SourceDir $item
    if (Test-Path -LiteralPath $source) {
        Copy-Item -LiteralPath $source -Destination $TargetDir -Recurse -Force
    }
}

$config = [ordered]@{
    listen                 = $Listen
    data_dir               = $DataDir
    web_root               = $TargetDir
    public_url             = $PublicUrl
    allowed_origins        = $origins
    allowed_cidrs          = ''
    admin_user             = $AdminUser
    admin_password         = $AdminPassword
    metrics_interval       = '60s'
    connector_url          = 'http://127.0.0.1:8766'
    connector_download     = '/downloads/QiyunConnector.zip'
    guacamole_url          = $GuacamoleUrl
    guacamole_user         = $GuacamoleUser
    guacamole_password     = $GuacamolePassword
    guacamole_data_source  = $GuacamoleDataSource
    guacamole_rdp_security = $GuacamoleRdpSecurity
    guacamole_tls_seclevel = ''
    guacamole_domain       = ''
}
$configPath = Join-Path $TargetDir 'central.config.json'
$config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding utf8

if ($NoStart) {
    Write-Host 'Files copied and config written. Task was not registered because -NoStart was used.'
    Write-Host "Config: $configPath"
    Write-Host "Data:   $DataDir"
    return
}

$argument = '-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + (Join-Path $TargetDir 'start-central.ps1') + '"'
$powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$action = New-ScheduledTaskAction -Execute $powershell -Argument $argument
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -User 'SYSTEM' -Force | Out-Null
Start-ScheduledTask -TaskName $taskName

$port = ($Listen -split ':')[-1]
$health = 'http://127.0.0.1:' + $port + '/healthz'
$ready = $false
for ($i = 0; $i -lt 60; $i++) {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Uri $health -TimeoutSec 2
        if ($response.StatusCode -eq 200) { $ready = $true; break }
    } catch { }
    Start-Sleep -Milliseconds 500
}
if (-not $ready) {
    Write-Warning 'Service did not become ready. The temporary admin password remains in central.config.json; inspect logs and retry.'
    Write-Host "Logs: $(Join-Path (Split-Path -Parent $DataDir) 'logs\qiyun.log')"
    exit 1
}
# Bootstrap is complete once health is up. Remove the plaintext bootstrap password;
# later starts use the existing user table instead.
$config.Remove('admin_password')
$config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding utf8
Write-Host ''
Write-Host 'Qiyun central service installed and running.'
Write-Host "URL:        $PublicUrl"
Write-Host "Local:      http://127.0.0.1:$port"
Write-Host "Admin user: $AdminUser"
Write-Host 'Next: configure HTTPS reverse proxy, sign in, change the admin password, create users and asset grants.'
Write-Host 'Connector download: /downloads/QiyunConnector.zip'