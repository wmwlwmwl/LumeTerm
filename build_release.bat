@echo off
setlocal
echo ==============================================
echo        LumeTerm Automated Builder
echo ==============================================

if "%~1"=="" (
    set /p VERSION="Enter release version (e.g. 1.0.1): "
) else (
    set "VERSION=%~1"
)

if "%VERSION%"=="" (
    echo [ERROR] Version cannot be empty!
    pause
    exit /b 1
)

REM Strip V/v prefix for source file updates
set "VER_NUM=%VERSION%"
if /i "%VER_NUM:~0,1%"=="V" set "VER_NUM=%VER_NUM:~1%"

echo [1/4] Updating version to %VER_NUM% in source files...
node -e "const fs=require('fs');const v=process.argv[1];const w=JSON.parse(fs.readFileSync('wails.json','utf8'));w.info.productVersion=v;fs.writeFileSync('wails.json',JSON.stringify(w,null,2)+'\n');let c=fs.readFileSync('frontend/src/config.ts','utf8');c=c.replace(/APP_VERSION\s*=\s*'[^']*'/,'APP_VERSION = '+String.fromCharCode(39)+v+String.fromCharCode(39));fs.writeFileSync('frontend/src/config.ts',c);const p=JSON.parse(fs.readFileSync('frontend/package.json','utf8'));p.version=v;fs.writeFileSync('frontend/package.json',JSON.stringify(p,null,2)+'\n');const pl=JSON.parse(fs.readFileSync('frontend/package-lock.json','utf8'));pl.version=v;if(pl.packages&&pl.packages[''])pl.packages[''].version=v;fs.writeFileSync('frontend/package-lock.json',JSON.stringify(pl,null,2)+'\n');console.log('Updated wails.json, config.ts, package.json, package-lock.json')" "%VER_NUM%"
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Failed to update version in source files.
    if "%~1"=="" pause
    exit /b 1
)

echo [2/4] Configuring Go and NSIS environment...
for %%I in ("%~dp0..\..\..") do set "ROOT_DIR=%%~fI"
set "GO_BIN=%ROOT_DIR%\Source_Codes\Lumin-Source\go\bin"
set "NSIS_DIR=%ROOT_DIR%\Packaging_Tools\nsis\nsis-3.08"
set "PATH=%GO_BIN%;%NSIS_DIR%;%USERPROFILE%\go\bin;%PATH%"

echo [3/4] Building setup installer using Wails...
cd /d "%~dp0"
call wails build -clean -upx -nsis -ldflags "-s -w"

set "EXE_PATH="
for %%f in (build\bin\*installer.exe) do (
    set "EXE_PATH=%%f"
    goto :found_exe
)

:found_exe
if "%EXE_PATH%"=="" (
    echo [ERROR] Build failed, setup installer not found.
    if "%~1"=="" pause
    exit /b 1
)

echo [4/4] Renaming output files...
move /y "build\bin\LumeTerm.exe" "build\bin\LumeTerm-V%VER_NUM%-windows-amd64-portable.exe" >nul
move /y "build\bin\LumeTerm-amd64-installer.exe" "build\bin\LumeTerm-V%VER_NUM%-windows-amd64-installer.exe" >nul

echo.
echo ==============================================
echo   SUCCESS!
echo   Installer: %CD%\build\bin\LumeTerm-V%VER_NUM%-windows-amd64-installer.exe
echo   Portable:  %CD%\build\bin\LumeTerm-V%VER_NUM%-windows-amd64-portable.exe
echo ==============================================
if "%~1"=="" pause
