[CmdletBinding()]
param(
    [string]$StartupDir = [Environment]::GetFolderPath('Startup'),
    [switch]$RemoveData
)

$ErrorActionPreference = 'Stop'
$target = Join-Path $env:LOCALAPPDATA 'QiyunConnector'
$shortcutPath = Join-Path $StartupDir 'Qiyun Connector.lnk'
if (Test-Path -LiteralPath $shortcutPath) {
    Remove-Item -LiteralPath $shortcutPath -Force
    Write-Host "Removed $shortcutPath"
}
Get-Process -Name 'qiyun-connector' -ErrorAction SilentlyContinue | ForEach-Object {
    try {
        if ($_.Path -and $_.Path.StartsWith($target, [StringComparison]::OrdinalIgnoreCase)) {
            Stop-Process -Id $_.Id -Force
            Write-Host "Stopped qiyun-connector $($_.Id)"
        }
    }
    catch { }
}
if ($RemoveData -and (Test-Path -LiteralPath $target)) {
    Remove-Item -LiteralPath $target -Recurse -Force
    Write-Host "Removed $target"
}
Write-Host 'Connector uninstall complete. The RDP signing certificate and policy were kept.'