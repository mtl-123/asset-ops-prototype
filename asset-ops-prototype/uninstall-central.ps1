[CmdletBinding()]
param(
    [string]$TargetDir = 'C:\Program Files\QiyunAssetOps',
    [string]$DataDir = 'C:\ProgramData\QiyunAssetOps\data',
    [switch]$RemoveFiles,
    [switch]$RemoveData
)

$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run uninstall-central.ps1 from an elevated PowerShell.'
}
$taskName = 'QiyunAssetOps'
$task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
if ($task) {
    Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
    Write-Host "Removed scheduled task $taskName"
}
Get-Process -Name 'qiyun' -ErrorAction SilentlyContinue | ForEach-Object {
    try {
        if ($_.Path -and $_.Path.StartsWith($TargetDir, [StringComparison]::OrdinalIgnoreCase)) {
            Stop-Process -Id $_.Id -Force
            Write-Host "Stopped qiyun $($_.Id)"
        }
    } catch { }
}
if ($RemoveFiles -and (Test-Path -LiteralPath $TargetDir)) {
    Remove-Item -LiteralPath $TargetDir -Recurse -Force
    Write-Host "Removed $TargetDir"
}
if ($RemoveData -and (Test-Path -LiteralPath $DataDir)) {
    Remove-Item -LiteralPath $DataDir -Recurse -Force
    Write-Host "Removed $DataDir (asset database and master key)"
}
Write-Host 'Central uninstall complete. Data is kept unless -RemoveData was specified.'
Write-Host 'The reverse proxy, DNS entry and connector signing certificate/policy were not touched.'