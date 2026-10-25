[CmdletBinding()]
param(
    [string]$AppDir = $PSScriptRoot,
    [string]$DesktopDir = [Environment]::GetFolderPath('Desktop'),
    [string]$StartupDir = [Environment]::GetFolderPath('Startup'),
    [switch]$RemoveData
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($AppDir)) { $AppDir = $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($AppDir)) { $AppDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$AppDir = (Resolve-Path -LiteralPath $AppDir).Path
$targets = @(
    (Join-Path $DesktopDir 'Qiyun Asset Ops.lnk'),
    (Join-Path $StartupDir 'Qiyun Asset Ops (background).lnk')
)
foreach ($path in $targets) {
    if (Test-Path -LiteralPath $path) {
        Remove-Item -LiteralPath $path -Force
        Write-Host "Removed $path"
    }
}

Get-Process -Name 'qiyun', 'qiyun-launcher' -ErrorAction SilentlyContinue | ForEach-Object {
    try {
        if ($_.Path -and $_.Path.StartsWith($AppDir, [StringComparison]::OrdinalIgnoreCase)) {
            Stop-Process -Id $_.Id -Force
            Write-Host "Stopped $($_.ProcessName) $($_.Id)"
        }
    }
    catch { }
}

if ($RemoveData) {
    $data = Join-Path $AppDir 'data'
    if (Test-Path -LiteralPath $data) {
        Remove-Item -LiteralPath $data -Recurse -Force
        Write-Host "Removed $data"
    }
}

Write-Host 'Uninstall complete. Logs and the application directory were kept.'
Write-Host 'Use -RemoveData to delete the encrypted asset database as well.'