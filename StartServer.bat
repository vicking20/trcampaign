@echo off
rem NFSTR Online server for Windows (experimental). Double-click. Needs Python 3: https://www.python.org/downloads/ (tick "Add Python to PATH").
rem Optional: StartServer.bat "D:\Games\NFS The Run"  to point it at another game folder.
cd /d "%~dp0"
if exist NFSTR_Server\tools\host.py ( set GAMEDIR=%~dp0.& cd NFSTR_Server ) else ( set GAMEDIR= )
if not "%~1"=="" set GAMEDIR=%~1
set PY=
where py >nul 2>nul && set PY=py -3
if "%PY%"=="" where python >nul 2>nul && set PY=python
if "%PY%"=="" (
  echo Python 3 is required. Install it from https://www.python.org/downloads/ ^(tick "Add Python to PATH"^), then run this again.
  pause
  exit /b 1
)
%PY% tools\host.py run "%GAMEDIR%"
pause
