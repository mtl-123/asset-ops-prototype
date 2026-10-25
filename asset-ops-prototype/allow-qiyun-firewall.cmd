@echo off
setlocal
net session >nul 2>&1
if not "%errorlevel%"=="0" (
  echo Requesting administrator privileges...
  powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
  exit /b
)
echo Adding Windows Firewall rule for Qiyun...
netsh advfirewall firewall delete rule name="Qiyun Dev 8770" >nul 2>&1
netsh advfirewall firewall add rule name="Qiyun Dev 8770" dir=in action=allow protocol=TCP localport=8770 profile=any
if errorlevel 1 (
  echo Failed to add rule.
  pause
  exit /b 1
)
echo.
echo Done. Open http://10.0.0.12:8770/ from another computer on the same LAN.
pause
