@echo off
setlocal
cd /d "%~dp0"
if exist obj rmdir /s /q obj
if exist build rmdir /s /q build
echo Cleaned gateway outputs. OpenSSL source/cache was kept.
