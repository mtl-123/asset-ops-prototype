[CmdletBinding()]
param(
    [switch]$SkipBuild,
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'
$scriptDir = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($scriptDir)) { $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
if ([string]::IsNullOrWhiteSpace($OutputDir)) { $OutputDir = Join-Path $scriptDir 'dist' }

if (-not $SkipBuild) {
    & (Join-Path $scriptDir 'build-qiyun.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
}

$required = @('qiyun.exe', 'qiyun-launcher.exe', 'index.html', 'install-qiyun.ps1', 'uninstall-qiyun.ps1', 'start-qiyun.cmd')
foreach ($name in $required) {
    if (-not (Test-Path -LiteralPath (Join-Path $scriptDir $name))) {
        throw "Missing $name. Run build-qiyun.ps1 first."
    }
}

$stage = Join-Path $OutputDir 'QiyunAssetOps'
if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path $stage | Out-Null

foreach ($name in $required) {
    Copy-Item -LiteralPath (Join-Path $scriptDir $name) -Destination $stage
}
New-Item -ItemType Directory -Force -Path (Join-Path $stage 'vendor') | Out-Null
Copy-Item -LiteralPath (Join-Path $scriptDir 'vendor\xterm') -Destination (Join-Path $stage 'vendor\xterm') -Recurse
Copy-Item -LiteralPath (Join-Path $scriptDir 'assets') -Destination (Join-Path $stage 'assets') -Recurse
$agentDownload = Join-Path $scriptDir 'downloads\qiyun-agent-linux-amd64.zip'
if (Test-Path -LiteralPath $agentDownload) {
    New-Item -ItemType Directory -Force -Path (Join-Path $stage 'downloads') | Out-Null
    Copy-Item -LiteralPath $agentDownload -Destination (Join-Path $stage 'downloads')
}

$setupCmd = @'
@echo off
setlocal
set "TARGET=%LOCALAPPDATA%\QiyunAssetOps"
echo Installing Qiyun Asset Ops to "%TARGET%"
if exist "%TARGET%\qiyun.exe" (
  echo An existing installation was found. Close Qiyun before upgrading.
)
robocopy "%~dp0" "%TARGET%" /E /NFL /NDL /NJH /NJS /NP >nul
if errorlevel 8 (
  echo Copy failed. Close Qiyun and run setup.cmd again.
  pause
  exit /b 1
)
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%TARGET%\install-qiyun.ps1" -AppDir "%TARGET%"
if errorlevel 1 (
  echo Installation failed. Review the messages above.
  pause
  exit /b 1
)
start "" "http://127.0.0.1:8765/#/hosts"
echo.
echo Installed. Use the desktop shortcut "Qiyun Asset Ops".
echo Keep the Qiyun background service running; it starts automatically at sign-in.
pause
'@
[IO.File]::WriteAllText((Join-Path $stage 'setup.cmd'), $setupCmd, [Text.Encoding]::ASCII)

$readme = @'
栖云资产运维 - Windows 安装说明

1. 将整个文件夹解压到本机任意目录（不要只复制 setup.cmd）。
2. 双击 setup.cmd。
   - 安装不需要管理员权限。
   - 首次使用远程桌面签名时，Windows 会请求一次 UAC/证书确认。
3. 安装完成后：
   - 桌面出现 “Qiyun Asset Ops”，双击即可打开；
   - 登录 Windows 时会自动启动后台服务；
   - 不需要再运行任何命令。
4. 卸载：
   - 运行安装目录（%LOCALAPPDATA%\QiyunAssetOps）下的 uninstall-qiyun.ps1；
   - 如需同时删除资产库，加 -RemoveData。
5. 数据位置：
   - 资产库与凭据在安装目录的 data 子目录；
   - 日志在 logs\launcher.log 和 logs\qiyun.log。
6. 注意：
   - 这是每台 Windows 电脑本地运行服务的架构，资产库不会自动与其他电脑同步；
   - 如果多台电脑要共享同一套资产，需要中心资产服务或 Web RDP 网关，不能只复制快捷方式。
'@
[IO.File]::WriteAllText((Join-Path $stage 'README-安装.txt'), $readme, [Text.UTF8Encoding]::new($true))

$zip = Join-Path $OutputDir 'QiyunAssetOps.zip'
if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal

Write-Host "Package directory: $stage"
Write-Host "Package archive:   $zip"
Get-Item -LiteralPath $zip | Select-Object FullName, Length, LastWriteTime