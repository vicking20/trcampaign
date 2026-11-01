@echo off
rem Host a game and play it on this PC. Put this file next to "Need For Speed The Run.exe".
setlocal
cd /d "%~dp0"
set "PY=python"
where py >nul 2>nul && set "PY=py -3"
%PY% --version >nul 2>nul || ( echo Python 3 is needed: https://www.python.org/downloads/ & pause & exit /b 1 )
%PY% Scripts\lsx.py --setup 127.0.0.1 || exit /b 1

echo Stopping anything left over from an earlier run...
call "%~dp0NFSTR_Server\StopServer.bat" nopause >nul 2>nul
cd /d "%~dp0"
taskkill /f /im NFSTR_Server.exe >nul 2>nul
taskkill /f /im "Need For Speed The Run.exe" >nul 2>nul
powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.Name -match '^pythonw?\.exe$|^py\.exe$' -and $_.CommandLine -like '*lsx.py*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }" >nul 2>nul

rem One sign-in helper for both the game server and the game
start "NFSTR Origin helper" /min %PY% -u "%~dp0Scripts\lsx.py"
timeout /t 1 /nobreak >nul
start "NFSTR Online server" cmd /c "%~dp0NFSTR_Server\StartServer.bat"

echo Waiting for the server...
set /a TRIES=0
:wait
timeout /t 2 /nobreak >nul
netstat -ano | findstr /c:":42127" | findstr LISTENING >nul && goto up
set /a TRIES+=1
if %TRIES% lss 45 goto wait
echo The server did not start - see its window.
pause
goto done

:up
call NFSTR_Online_Play.bat shared
:done
echo Stopping the server...
call "%~dp0NFSTR_Server\StopServer.bat" nopause >nul 2>nul
cd /d "%~dp0"
taskkill /f /im NFSTR_Server.exe >nul 2>nul
powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.Name -match '^pythonw?\.exe$|^py\.exe$' -and $_.CommandLine -like '*lsx.py*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }" >nul 2>nul
endlocal
