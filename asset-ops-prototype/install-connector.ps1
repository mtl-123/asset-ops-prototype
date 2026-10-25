[CmdletBinding()]
param(
    [string]$CentralUrl = '',
    [string]$AppDir = $PSScriptRoot,
    [string]$StartupDir = [Environment]::GetFolderPath('Startup'),
    [switch]$NoStart
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($AppDir)) { $AppDir = $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($AppDir)) { $AppDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$AppDir = (Resolve-Path -LiteralPath $AppDir).Path
$source = Join-Path $AppDir 'qiyun-connector.exe'
if (-not (Test-Path -LiteralPath $source)) { throw "qiyun-connector.exe not found in $AppDir" }
if ([string]::IsNullOrWhiteSpace($CentralUrl)) {
    $CentralUrl = Read-Host 'Central server URL (for example https://ops.example.com)'
}
$CentralUrl = $CentralUrl.Trim().TrimEnd('/')
$uri = $null
if (-not [Uri]::TryCreate($CentralUrl, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -notin @('http', 'https')) {
    throw "Central URL must start with http:// or https://"
}

$target = Join-Path $env:LOCALAPPDATA 'QiyunConnector'
New-Item -ItemType Directory -Force -Path $target, (Join-Path $target 'logs') | Out-Null
Get-Process -Name 'qiyun-connector' -ErrorAction SilentlyContinue | ForEach-Object {
    try {
        if ($_.Path -and $_.Path.StartsWith($target, [StringComparison]::OrdinalIgnoreCase)) {
            Stop-Process -Id $_.Id -Force
        }
    }
    catch { }
}
Copy-Item -LiteralPath $source -Destination $target -Force
$uninstall = Join-Path $AppDir 'uninstall-connector.ps1'
if (Test-Path -LiteralPath $uninstall) { Copy-Item -LiteralPath $uninstall -Destination $target -Force }
$exe = Join-Path $target 'qiyun-connector.exe'

New-Item -ItemType Directory -Force -Path $StartupDir | Out-Null
$shortcutPath = Join-Path $StartupDir 'Qiyun Connector.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $exe
$shortcut.Arguments = '-central "' + $CentralUrl + '"'
$shortcut.WorkingDirectory = $target
$shortcut.IconLocation = "$exe,0"
$shortcut.Description = 'Qiyun local RDP connector'
$shortcut.Save()
Write-Host "Created $shortcutPath"

if (-not $NoStart) {
    Start-Process -FilePath $exe -ArgumentList '-central', $CentralUrl -WindowStyle Hidden
    $ready = $false
    for ($i = 0; $i -lt 40; $i++) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8766/healthz' -TimeoutSec 2
            if ($response.StatusCode -eq 200) { $ready = $true; break }
        }
        catch { }
        Start-Sleep -Milliseconds 500
    }
    if ($ready) { Write-Host 'Qiyun connector is ready on http://127.0.0.1:8766' }
    else { Write-Warning 'Connector did not become ready. Check %LOCALAPPDATA%\QiyunConnector\logs\connector.log.' }
}

Write-Host ''
Write-Host "Installed. Central server: $CentralUrl"
Write-Host 'Open the central web console, then click Connect on a Windows asset.'
Write-Host 'The first connection asks the connector to enable trusted RDP signing (UAC).'