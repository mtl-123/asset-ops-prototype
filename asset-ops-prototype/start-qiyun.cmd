@echo off
setlocal
cd /d "%~dp0"
if not exist "qiyun.exe" (
  echo ERROR: qiyun.exe is missing. Build the application first.
  pause
  exit /b 1
)
echo Checking port 8765. An existing server will NOT be stopped.
powershell.exe -NoProfile -NonInteractive -Command "$ErrorActionPreference='Stop'; $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,8765); try { $listener.Start() } catch { Write-Host 'Port 8765 is unavailable. No new instance was started.'; Write-Host 'Check the existing application or server console before retrying.'; exit 1 } finally { try { $listener.Stop() } catch { } }"
if errorlevel 1 (
  pause
  exit /b 1
)
echo Starting Qiyun at http://127.0.0.1:8765/#/hosts
echo Run this file from your normal Windows desktop or terminal.
echo Keep this window open; press Ctrl+C to stop.
"%~dp0qiyun.exe" -addr 127.0.0.1:8765 -data "%~dp0data" -web "%~dp0."
set "qiyun_exit=%errorlevel%"
echo Qiyun exited with code %qiyun_exit%. Review the error above.
pause
exit /b %qiyun_exit%
