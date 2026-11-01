@echo off
cd /d "%~dp0"
set "PY=python"
where py >nul 2>nul && set "PY=py -3"
%PY% tools\host.py stop
if /i not "%~1"=="nopause" pause
