@echo off
setlocal
REM Thin wrapper: all packaging logic lives in build_release.ps1 (single source).
REM Usage: build_release.bat [version] [fast]
REM   build_release.bat 1.3.6        release build with version bump
REM   build_release.bat fast         quick local build, version from wails.json
REM   build_release.bat 1.3.6 fast   quick local build with version bump
REM NOTE: keep this file pure ASCII -- cmd parses .bat via OEM codepage,
REM non-ASCII bytes break parsing. User-facing Chinese lives in the .ps1.

set "VERSION=%~1"
set "FAST=%~2"
if /i "%~1"=="fast" (set "VERSION=" & set "FAST=fast")

if "%VERSION%"=="" (
    set /p VERSION="Enter release version (empty = use version in wails.json): "
)

set "ARGS="
if not "%VERSION%"=="" set "ARGS=-Version %VERSION%"
if /i "%FAST%"=="fast" set "ARGS=%ARGS% -Fast"

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build_release.ps1" %ARGS%
set "RC=%ERRORLEVEL%"

if not "%RC%"=="0" echo [ERROR] Build failed with code %RC%.
if "%~1"=="" pause
exit /b %RC%
