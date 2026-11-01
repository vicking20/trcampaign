@echo off
rem Starts the server (it also starts the game's dedicated server). Optional: StartServer.bat "D:\Games\NFS The Run"
cd /d "%~dp0"
set "PY=python"
where py >nul 2>nul && set "PY=py -3"
%PY% --version >nul 2>nul || ( echo Python 3 is needed: https://www.python.org/downloads/ & pause & exit /b 1 )
%PY% tools\host.py run %*
pause
