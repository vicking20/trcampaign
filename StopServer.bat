@echo off
cd /d "%~dp0"
if exist NFSTR_Server\tools\host.py cd NFSTR_Server
set PY=py -3
where py >nul 2>nul || set PY=python
%PY% tools\host.py stop
pause
