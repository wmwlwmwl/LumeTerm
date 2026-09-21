param(
    # 指定版本号（可带 v 前缀），会同步写入 wails.json 等文件；不指定则用 wails.json 当前版本
    [string]$Version,
    # 快速模式：跳过 -clean 和 UPX，产物体积偏大，仅用于本地验证打包流程
    [switch]$Fast
)

if ($Version) {
    $version = $Version.Trim() -replace '^[vV]', ''
    if ($version -notmatch '^\d+\.\d+\.\d+$') {
        Write-Error "版本号格式不正确：$Version（应为 1.2.3）"
        exit 1
    }
} else {
    $version = (Get-Content "wails.json" | ConvertFrom-Json).info.productVersion
}

if ($Fast) {
    Write-Host "Start LumeTerm fast packaging: V$version (skip clean/upx)" -ForegroundColor Cyan
} else {
    Write-Host "Start LumeTerm packaging process: V$version" -ForegroundColor Cyan
}

$basePath = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$nsisPath = "$basePath\Packaging_Tools\nsis\nsis-3.08"
$goPath = "$basePath\Source_Codes\Lumin-Source\go\bin"

# 正式模式需要 UPX 压缩便携版，提前检查，避免编译完才发现缺工具
if (-not $Fast -and -not (Get-Command upx -ErrorAction SilentlyContinue)) {
    Write-Error "未找到 upx，请先安装或将其加入 PATH（或使用 -Fast 跳过）"
    exit 1
}

# Sync version to wails.json, config.ts, package.json, package-lock.json
node -e "const fs=require('fs');const v=process.argv[1];const w=JSON.parse(fs.readFileSync('wails.json','utf8'));w.info.productVersion=v;fs.writeFileSync('wails.json',JSON.stringify(w,null,2)+'\n');let c=fs.readFileSync('frontend/src/config.ts','utf8');c=c.replace(/APP_VERSION\s*=\s*'[^']*'/,'APP_VERSION = '+String.fromCharCode(39)+v+String.fromCharCode(39));fs.writeFileSync('frontend/src/config.ts',c);const p=JSON.parse(fs.readFileSync('frontend/package.json','utf8'));p.version=v;fs.writeFileSync('frontend/package.json',JSON.stringify(p,null,2)+'\n');const pl=JSON.parse(fs.readFileSync('frontend/package-lock.json','utf8'));pl.version=v;if(pl.packages&&pl.packages[''])pl.packages[''].version=v;fs.writeFileSync('frontend/package-lock.json',JSON.stringify(pl,null,2)+'\n');console.log('Synced version '+v+' to wails.json, config.ts, package.json, package-lock.json')" "$version"
if ($LASTEXITCODE -ne 0) { Write-Error "版本号同步失败"; exit 1 }

# Inject required paths
$env:PATH = "$goPath;$nsisPath;$env:USERPROFILE\go\bin;" + $env:PATH

# 正式模式不加 -upx：安装包内放未压缩 exe，由 NSIS LZMA solid 压缩，体积更小；
# 便携版在构建后单独 UPX
$buildArgs = @('build', '-trimpath', '-nsis', '-ldflags', '-s -w')
if (-not $Fast) { $buildArgs += '-clean' }
Write-Host "`n[1/2] Building with Wails (portable + installer)..." -ForegroundColor Yellow
wails @buildArgs
if ($LASTEXITCODE -ne 0) { Write-Error "Build failed"; exit 1 }

Write-Host "`n[2/2] Renaming output files..." -ForegroundColor Yellow
$portableDest = "build\bin\LumeTerm-$version-windows-amd64-portable.exe"
$setupDest = "build\bin\LumeTerm-$version-windows-amd64-installer.exe"

if ($Fast) {
    Move-Item -Path "build\bin\LumeTerm.exe" -Destination $portableDest -Force
} else {
    Copy-Item -Path "build\bin\LumeTerm.exe" -Destination $portableDest -Force
    upx --best $portableDest | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Error "UPX compression failed"; exit 1 }
    Remove-Item -Path "build\bin\LumeTerm.exe" -Force
}
Move-Item -Path "build\bin\LumeTerm-amd64-installer.exe" -Destination $setupDest -Force

Write-Host "`n==============================================" -ForegroundColor Cyan
Write-Host "  SUCCESS!" -ForegroundColor Green
Write-Host "  Portable:  $portableDest" -ForegroundColor Green
Write-Host "  Installer: $setupDest" -ForegroundColor Green
Write-Host "==============================================" -ForegroundColor Cyan
