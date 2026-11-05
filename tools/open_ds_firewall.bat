@echo off
setlocal
rem Opens the Windows firewall for the race servers (run on the SERVER machine).
rem
rem   open_ds_firewall.bat                       relay ports only (default, RELAY on)
rem   open_ds_firewall.bat [relay_first] [count] default 6100 64 (RELAY_PORT_BASE / RELAY_PORT_COUNT)
rem   open_ds_firewall.bat direct [first] [count] RELAY_ENABLED=false: the race servers' own ports
rem
rem With the relay (default) players only send to the relay ports of
rem nfsrun-server.exe; the race servers talk to it over 127.0.0.1 and need no rule.
net session >nul 2>&1
if errorlevel 1 (
  powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -ArgumentList '%*' -Verb RunAs"
  exit /b
)
if /i "%~1"=="direct" goto direct
set "FIRST=%~1"
if "%FIRST%"=="" set "FIRST=6100"
set "COUNT=%~2"
if "%COUNT%"=="" set "COUNT=64"
set /a LAST=%FIRST%+%COUNT%-1
netsh advfirewall firewall delete rule name="NFSTR race servers (UDP)" >nul 2>&1
netsh advfirewall firewall add rule name="NFSTR race servers (UDP)" dir=in action=allow protocol=UDP localport=%FIRST%-%LAST%
echo.
echo Windows firewall: UDP %FIRST%-%LAST% (relay) open. Open the same UDP ports on the router / hosting firewall.
echo The race servers' own ports do not need to be open.
pause
exit /b

:direct
set "FIRST=%~2"
if "%FIRST%"=="" set "FIRST=25200"
set "COUNT=%~3"
if "%COUNT%"=="" set "COUNT=8"
set /a LAST=%FIRST%+%COUNT%-1
rem race servers also use FIRST+100.. for the Frostbite -listen socket (DS_LISTEN_OFFSET)
set /a LFIRST=%FIRST%+100
set /a LLAST=%LAST%+100
netsh advfirewall firewall delete rule name="NFSTR race servers (UDP)" >nul 2>&1
netsh advfirewall firewall add rule name="NFSTR race servers (UDP)" dir=in action=allow protocol=UDP localport=%FIRST%-%LAST%,%LFIRST%-%LLAST%,6000,9999
echo.
echo Windows firewall: UDP 6000, 9999, %FIRST%-%LAST% and %LFIRST%-%LLAST% open (RELAY_ENABLED=false). Open the same ports on the router / hosting firewall.
pause
