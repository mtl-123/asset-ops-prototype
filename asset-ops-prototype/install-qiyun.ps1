[CmdletBinding()]
param(
    [string]$AppDir = $PSScriptRoot,
    [string]$DesktopDir = [Environment]::GetFolderPath('Desktop'),
    [string]$StartupDir = [Environment]::GetFolderPath('Startup'),
    [switch]$NoStart,
    [switch]$SetupRdpSigning
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($AppDir)) { $AppDir = $PSScriptRoot }
if ([string]::IsNullOrWhiteSpace($AppDir)) { $AppDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$AppDir = (Resolve-Path -LiteralPath $AppDir).Path
$qiyun = Join-Path $AppDir 'qiyun.exe'
$launcher = Join-Path $AppDir 'qiyun-launcher.exe'
if (-not (Test-Path -LiteralPath $qiyun)) { throw "qiyun.exe not found in $AppDir. Run build-qiyun.ps1 first." }
if (-not (Test-Path -LiteralPath $launcher)) { throw "qiyun-launcher.exe not found in $AppDir. Run build-qiyun.ps1 first." }
New-Item -ItemType Directory -Force -Path (Join-Path $AppDir 'logs') | Out-Null
New-Item -ItemType Directory -Force -Path $DesktopDir | Out-Null
New-Item -ItemType Directory -Force -Path $StartupDir | Out-Null

$desktopShortcut = Join-Path $DesktopDir 'Qiyun Asset Ops.lnk'
$startupShortcut = Join-Path $StartupDir 'Qiyun Asset Ops (background).lnk'
$shell = New-Object -ComObject WScript.Shell

function New-Shortcut {
    param([string]$Path, [string]$Arguments, [string]$Description)
    $shortcut = $shell.CreateShortcut($Path)
    $shortcut.TargetPath = $launcher
    $shortcut.Arguments = $Arguments
    $shortcut.WorkingDirectory = $AppDir
    $shortcut.IconLocation = "$qiyun,0"
    $shortcut.Description = $Description
    $shortcut.Save()
    Write-Host "Created $Path"
}

New-Shortcut -Path $desktopShortcut -Arguments '' -Description 'Open Qiyun asset operations'
New-Shortcut -Path $startupShortcut -Arguments '-watch -start-only' -Description 'Start the local Qiyun service at sign-in'

if (-not $NoStart) {
    Start-Process -FilePath $launcher -ArgumentList '-watch', '-start-only' -WindowStyle Hidden
    $ready = $false
    for ($i = 0; $i -lt 60; $i++) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8765/healthz' -TimeoutSec 2
            if ($response.StatusCode -eq 200) { $ready = $true; break }
        }
        catch { }
        Start-Sleep -Milliseconds 500
    }
    if ($ready) {
        Write-Host 'Qiyun service is ready: http://127.0.0.1:8765/#/hosts'
    }
    else {
        Write-Warning 'Qiyun did not become ready. See logs\launcher.log and logs\qiyun.log.'
    }
    if ($SetupRdpSigning -and $ready) {
        $setup = Join-Path $AppDir 'data\rdp\setup-rdp-signing.ps1'
        if (Test-Path -LiteralPath $setup) {
            Write-Host 'Configuring trusted RDP publisher. Approve the certificate and UAC prompts if shown.'
            & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $setup -Mode Install
            if ($LASTEXITCODE -ne 0) { Write-Warning 'RDP signing setup did not complete. Open the app and use the signing button.' }
        }
        else {
            Write-Warning 'RDP setup script is not extracted yet. Open the app and use the signing button.'
        }
    }
}

Write-Host ''
Write-Host 'Done. Double-click the desktop shortcut "Qiyun Asset Ops" to use the app.'
Write-Host "Desktop shortcut: $desktopShortcut"
Write-Host "Sign-in shortcut: $startupShortcut"