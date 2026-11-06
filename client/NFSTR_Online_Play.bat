@echo off
setlocal
cd /d "%~dp0"
if not exist "Need For Speed The Run.exe" ( echo Run this from the game folder. & pause & exit /b 1 )
if not exist "Scripts\nfstr-origin.exe" ( echo Scripts\nfstr-origin.exe is missing - extract the whole zip into the game folder. & pause & exit /b 1 )

rem "shared" = called by NFSTR_Host_And_Play.bat, which already set things up and runs the helper
if /i "%~1"=="shared" goto launch
Scripts\nfstr-origin.exe --setup || exit /b 1
taskkill /f /im "Need For Speed The Run.exe" >nul 2>nul
taskkill /f /im nfstr-origin.exe >nul 2>nul
timeout /t 1 /nobreak >nul
netstat -ano | findstr /c:"127.0.0.1:3216" | findstr LISTENING >nul && ( echo Port 3216 is taken by another program, probably the EA app or Origin. Close it - also from the tray - and run this again. & pause & exit /b 1 )
pushd "%~dp0Scripts"
start "NFSTR Origin helper" /min nfstr-origin.exe
popd
timeout /t 1 /nobreak >nul

:launch
start "Need for Speed The Run" /wait "%~dp0Need For Speed The Run.exe" -NfsOnline.AllowSinglePlayerOnline true
if /i not "%~1"=="shared" taskkill /f /im nfstr-origin.exe >nul 2>nul
endlocal
