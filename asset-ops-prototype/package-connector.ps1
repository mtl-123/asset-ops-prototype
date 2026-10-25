[CmdletBinding()]
param(
    [switch]$SkipBuild,
    [string]$CentralUrl = '',
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'
$scriptDir = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($scriptDir)) { $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
if ([string]::IsNullOrWhiteSpace($OutputDir)) { $OutputDir = Join-Path $scriptDir 'dist' }
$root = Split-Path -Parent $scriptDir
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $root '.gocache' }
if (-not $env:GOTMPDIR) { $env:GOTMPDIR = Join-Path $root '.gotmp' }
Push-Location $scriptDir
try {
    if (-not $SkipBuild) {
        go build -buildvcs=false -ldflags '-H=windowsgui' -o qiyun-connector.exe ./cmd/connector
        if ($LASTEXITCODE -ne 0) { throw 'qiyun-connector.exe build failed.' }
    }
    if (-not (Test-Path -LiteralPath (Join-Path $scriptDir 'qiyun-connector.exe'))) {
        throw 'qiyun-connector.exe is missing. Run without -SkipBuild first.'
    }
    $stage = Join-Path $OutputDir 'QiyunConnector'
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    Copy-Item -LiteralPath (Join-Path $scriptDir 'qiyun-connector.exe') -Destination $stage
    Copy-Item -LiteralPath (Join-Path $scriptDir 'install-connector.ps1') -Destination $stage
    Copy-Item -LiteralPath (Join-Path $scriptDir 'uninstall-connector.ps1') -Destination $stage

    $urlLine = ''
    if (-not [string]::IsNullOrWhiteSpace($CentralUrl)) { $urlLine = ' -CentralUrl "' + $CentralUrl.Trim().TrimEnd('/') + '"' }
    $cmd = "@echo off`r`npowershell.exe -NoProfile -ExecutionPolicy Bypass -File `"%~dp0install-connector.ps1`"$urlLine`r`npause`r`n"
    [IO.File]::WriteAllText((Join-Path $stage 'install-connector.cmd'), $cmd, [Text.Encoding]::ASCII)

    $readme = @'
栖云 Windows 连接器 - 安装说明

用途：让中心网页在你自己的 Windows 电脑上启动 mstsc，而不是在服务器上弹桌面。

1. 解压整个文件夹到本机任意目录（不要只复制 exe）。
2. 双击 install-connector.cmd。
   - 如果安装器没有内置中心地址，会提示输入，例如 https://ops.example.com。
   - 安装不需要管理员权限；首次 RDP 签名时会出现一次 UAC。
3. 安装后连接器会随 Windows 登录自动启动，监听 127.0.0.1:8766。
4. 打开中心网页并登录，在 Windows 资产上点“远程”：
   - 页面检测到连接器后，点“首次启用签名”，按提示允许证书和 UAC；
   - 然后点“连接远程桌面”，mstsc 会在本机打开。
5. 卸载：运行安装目录下的 uninstall-connector.ps1。
   日志：%LOCALAPPDATA%\QiyunConnector\logs\connector.log。

注意：
- 连接器只监听回环地址，不接受局域网直接访问。
- 中心地址必须是安装时配置的地址；票据一次性、90 秒有效。
- 密码由中心经 HTTPS 只发给本机连接器，不进入浏览器。
'@
    [IO.File]::WriteAllText((Join-Path $stage 'README-安装.txt'), $readme, [Text.UTF8Encoding]::new($true))

    $zip = Join-Path $OutputDir 'QiyunConnector.zip'
    if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip -Force }
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal

    $downloads = Join-Path $scriptDir 'downloads'
    New-Item -ItemType Directory -Force -Path $downloads | Out-Null
    Copy-Item -LiteralPath $zip -Destination (Join-Path $downloads 'QiyunConnector.zip') -Force
    Write-Host "Connector package: $zip"
    Write-Host "Served copy:      $(Join-Path $downloads 'QiyunConnector.zip')"
    Get-Item -LiteralPath $zip | Select-Object FullName, Length, LastWriteTime
}
finally {
    Pop-Location
}