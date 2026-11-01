@echo off
rem Control panel: players, kick/ban, announcements, game mode
cd /d "%~dp0"
set "PY=python"
where py >nul 2>nul && set "PY=py -3"
%PY% tools\admin.py
pause
