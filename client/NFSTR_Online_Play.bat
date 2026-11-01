@echo off
setlocal
cd /d "%~dp0"
if not exist "Need For Speed The Run.exe" ( echo Run this from the game folder. & pause & exit /b 1 )
if not exist "Scripts\lsx.py" ( echo Scripts\lsx.py is missing - extract the whole zip into the game folder. & pause & exit /b 1 )
set "PY=python"
where py >nul 2>nul && set "PY=py -3"
%PY% --version >nul 2>nul || ( echo Python 3 is needed: https://www.python.org/downloads/ & pause & exit /b 1 )

rem "shared" = called by NFSTR_Host_And_Play.bat, which already set things up and runs the helper
if /i "%~1"=="shared" goto launch
%PY% Scripts\lsx.py --setup || exit /b 1
taskkill /f /im "Need For Speed The Run.exe" >nul 2>nul
powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.Name -match '^pythonw?\.exe$|^py\.exe$' -and $_.CommandLine -like '*lsx.py*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }" >nul 2>nul
start "NFSTR Origin helper" /min %PY% -u "%~dp0Scripts\lsx.py"
timeout /t 1 /nobreak >nul

:launch
start "Need for Speed The Run" /wait "%~dp0Need For Speed The Run.exe"
if /i not "%~1"=="shared" powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.Name -match '^pythonw?\.exe$|^py\.exe$' -and $_.CommandLine -like '*lsx.py*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }" >nul 2>nul
endlocal
