@echo off
setlocal
cd /d "%~dp0"
echo ============================================================
echo  NFS The Run - re-enable the Origin SDK startup in the game exe
echo  (5 bytes, verified, backup is made). See docs\ORIGIN_LSX.md
echo ============================================================
set "EXE=%~1"
if "%EXE%"=="" set /p EXE=Path of Need For Speed The Run.exe: 
if defined EXE set "EXE=%EXE:"=%"
if "%EXE%"=="" exit /b 1
python "%~dp0patch_origin_startup.py" "%EXE%"
if errorlevel 9009 py -3 "%~dp0patch_origin_startup.py" "%EXE%"
pause
