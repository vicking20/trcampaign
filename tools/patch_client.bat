@echo off
setlocal
cd /d "%~dp0"
if "%~1"=="" (
    echo Usage: patch_client.bat "C:\path\Need For Speed The Run.exe" [base-domain]
    echo Example: patch_client.bat "C:\Games\NFS The Run\Need For Speed The Run.exe" myserver.example
    pause
    exit /b 2
)
if "%~2"=="" (
    python "%~dp0patch_client.py" "%~1"
) else (
    python "%~dp0patch_client.py" "%~1" --domain "%~2"
)
if errorlevel 9009 (
    if "%~2"=="" (
        py -3 "%~dp0patch_client.py" "%~1"
    ) else (
        py -3 "%~dp0patch_client.py" "%~1" --domain "%~2"
    )
)
set "RC=%ERRORLEVEL%"
if not "%RC%"=="0" pause
exit /b %RC%
