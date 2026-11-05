@echo off
setlocal EnableExtensions EnableDelayedExpansion
cd /d "%~dp0"

set "OPENSSL_VER=1.0.2u"
set "OPENSSL_SHA256=ecd0c6ffb493dd06707d38b14bb4d8c2288bb7033735606569d8f90f89669d16"
set "OPENSSL_ARCHIVE=third_party\cache\openssl-%OPENSSL_VER%.tar.gz"
set "OPENSSL_SRC=third_party\openssl-%OPENSSL_VER%"
set "OPENSSL_URL=https://www.openssl.org/source/old/1.0.2/openssl-%OPENSSL_VER%.tar.gz"

 echo ============================================================
 echo NFS The Run - Legacy SSLv3 Gateway - x86 build
 echo ============================================================
 echo.

where cl >nul 2>nul
if not errorlevel 1 goto :have_msvc
set "VSWHERE=%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe"
if not exist "%VSWHERE%" (
    echo [ERROR] Visual Studio vswhere.exe was not found.
    echo Install Visual Studio Build Tools with Desktop development with C++.
    pause
    exit /b 1
)
for /f "usebackq tokens=*" %%i in (`"%VSWHERE%" -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath`) do set "VSROOT=%%i"
if not defined VSROOT (
    echo [ERROR] Visual Studio C++ x86 tools were not found.
    pause
    exit /b 1
)
call "%VSROOT%\VC\Auxiliary\Build\vcvars32.bat"
if errorlevel 1 (
    echo [ERROR] vcvars32.bat failed.
    pause
    exit /b 1
)

:have_msvc
where perl >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Perl was not found in PATH.
    echo.
    echo OpenSSL 1.0.2u's Windows build requires Perl.
    echo Strawberry Perl is the easiest option. Install it, reopen this prompt,
    echo then run build_x86.bat again.
    pause
    exit /b 1
)

where nmake >nul 2>nul
if errorlevel 1 (
    echo [ERROR] nmake.exe was not found after Visual Studio setup.
    pause
    exit /b 1
)

if not exist third_party\cache mkdir third_party\cache
if not exist build mkdir build
if not exist obj mkdir obj

if not exist "%OPENSSL_ARCHIVE%" (
    echo [1/6] Downloading official OpenSSL %OPENSSL_VER% source...
    powershell -NoProfile -ExecutionPolicy Bypass -Command ^
      "$ProgressPreference='SilentlyContinue'; Invoke-WebRequest -UseBasicParsing '%OPENSSL_URL%' -OutFile '%OPENSSL_ARCHIVE%'"
    if errorlevel 1 goto :download_fail
) else (
    echo [1/6] OpenSSL archive already present.
)

 echo [2/6] Verifying SHA-256...
for /f "tokens=*" %%h in ('powershell -NoProfile -Command "(Get-FileHash -Algorithm SHA256 '%OPENSSL_ARCHIVE%').Hash.ToLower()"') do set "GOT_SHA=%%h"
if /I not "!GOT_SHA!"=="%OPENSSL_SHA256%" (
    echo [ERROR] OpenSSL archive checksum mismatch.
    echo Expected: %OPENSSL_SHA256%
    echo Got:      !GOT_SHA!
    del /q "%OPENSSL_ARCHIVE%" >nul 2>nul
    pause
    exit /b 1
)

if not exist "%OPENSSL_SRC%\ms\do_ms.bat" (
    echo [3/6] Extracting OpenSSL %OPENSSL_VER%...
    if exist "%OPENSSL_SRC%" rmdir /s /q "%OPENSSL_SRC%"
    tar -xzf "%OPENSSL_ARCHIVE%" -C third_party
    if errorlevel 1 (
        echo [ERROR] tar extraction failed. Windows 10/Server 2019+ includes tar.exe.
        pause
        exit /b 1
    )
) else (
    echo [3/6] OpenSSL source already extracted.
)

if not exist "%OPENSSL_SRC%\out32\ssleay32.lib" (
    echo [4/6] Building static OpenSSL %OPENSSL_VER% with SSLv3 methods...
    pushd "%OPENSSL_SRC%"
    rem Start clean: a failed/old Configure can leave generated headers and makefiles behind.
    if exist ms\nt.mak nmake /nologo -f ms\nt.mak clean >nul 2>nul
    if exist tmp32 rmdir /s /q tmp32
    if exist out32 rmdir /s /q out32
    if exist inc32 rmdir /s /q inc32

    rem no-asm avoids a NASM dependency. Keep ENGINE enabled: OpenSSL 1.0.2's
    rem Win32 makefile still compiles engine sources and they need openssl/engine.h.
    rem enable-weak-ssl-ciphers is REQUIRED here because the 2011 EA client may
    rem offer RC4-SHA / RC4-MD5, disabled by default in late 1.0.2 builds.
    rem Do NOT pass no-ssl3 or no-ssl3-method.
    perl Configure VC-WIN32 no-asm no-shared no-zlib enable-weak-ssl-ciphers
    if errorlevel 1 (popd & goto :openssl_fail)
    call ms\do_ms.bat
    if errorlevel 1 (popd & goto :openssl_fail)

    rem Modern MSVC is much newer than this 2019 source. OpenSSL 1.0.2's generated
    rem nt.mak enables warnings-as-errors (-WX). Strip only that flag so benign
    rem new-compiler warnings do not abort the historical compatibility build.
    powershell -NoProfile -ExecutionPolicy Bypass -Command ^
      "$p='ms\nt.mak'; $s=[IO.File]::ReadAllText($p); $s=$s -replace '(?i)(^|[ \t])-WX([ \t]|$)', '$1$2'; [IO.File]::WriteAllText($p,$s,[Text.Encoding]::ASCII)"
    if errorlevel 1 (popd & goto :openssl_fail)

    rem NOTE: inc32\openssl headers are populated by the nmake build itself in
    rem OpenSSL 1.0.2. Do not validate engine.h/ssl3.h before nmake.
    nmake /nologo -f ms\nt.mak
    if errorlevel 1 (popd & goto :openssl_fail)

    if not exist inc32\openssl\engine.h (
        echo [ERROR] OpenSSL build finished but inc32\openssl\engine.h is missing.
        popd
        goto :openssl_fail
    )
    if not exist inc32\openssl\ssl3.h (
        echo [ERROR] OpenSSL build finished but inc32\openssl\ssl3.h is missing.
        popd
        goto :openssl_fail
    )
    if not exist out32\libeay32.lib (
        echo [ERROR] OpenSSL build finished but out32\libeay32.lib is missing.
        popd
        goto :openssl_fail
    )
    if not exist out32\ssleay32.lib (
        echo [ERROR] OpenSSL build finished but out32\ssleay32.lib is missing.
        popd
        goto :openssl_fail
    )
    popd
) else (
    echo [4/6] OpenSSL static libraries already built.
)

if not exist "%OPENSSL_SRC%\out32\ssleay32.lib" (
    echo [ERROR] ssleay32.lib was not produced.
    pause
    exit /b 1
)
if not exist "%OPENSSL_SRC%\out32\libeay32.lib" (
    echo [ERROR] libeay32.lib was not produced.
    pause
    exit /b 1
)

 echo [5/6] Compiling gateway...
if exist obj\*.obj del /q obj\*.obj >nul 2>nul
cl /nologo /c /TC /O2 /MT /W4 /D_CRT_SECURE_NO_WARNINGS /DWIN32_LEAN_AND_MEAN ^
   /I "%OPENSSL_SRC%\inc32" /Foobj\ ^
   src\main.c src\gateway.c src\config.c src\log.c
if errorlevel 1 goto :compile_fail

 echo [6/6] Linking gateway...
link /nologo /SUBSYSTEM:CONSOLE /MACHINE:X86 /OPT:REF /OPT:ICF ^
  /OUT:build\nfsrun-ssl-gateway.exe ^
  obj\main.obj obj\gateway.obj obj\config.obj obj\log.obj ^
  /LIBPATH:"%OPENSSL_SRC%\out32" ^
  ssleay32.lib libeay32.lib ws2_32.lib advapi32.lib user32.lib gdi32.lib crypt32.lib
if errorlevel 1 goto :link_fail

rem Keep the historical OpenSSL CLI beside the gateway. The first-run setup
rem uses it to generate the Old ProtoSSL-compatible OTG3 certificate.
copy /Y "%OPENSSL_SRC%\out32\openssl.exe" "build\openssl-1.0.2u.exe" >nul
if errorlevel 1 (
    echo [ERROR] Could not copy OpenSSL CLI to build\openssl-1.0.2u.exe
    goto :link_fail
)

 echo.
 echo ============================================================
 echo BUILD COMPLETE
 echo ============================================================
 echo Output: build\nfsrun-ssl-gateway.exe
echo OpenSSL CLI: build\openssl-1.0.2u.exe
 echo.
 echo Integrated package: start_server.bat manages this gateway automatically.
 echo.
exit /b 0

:download_fail
 echo [ERROR] Could not download OpenSSL from %OPENSSL_URL%
 pause
 exit /b 1
:openssl_fail
 echo [ERROR] OpenSSL build failed.
 echo See the first compiler error above. This build keeps ENGINE + SSLv3/RC4 compatibility enabled and removes /WX for modern MSVC.
 pause
 exit /b 1
:compile_fail
 echo [ERROR] Gateway compilation failed.
 pause
 exit /b 1
:link_fail
 echo [ERROR] Gateway link failed.
 pause
 exit /b 1
