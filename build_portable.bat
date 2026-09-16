@echo off
chcp 65001 >nul
rem Build the TDebug Windows portable package: exe + config + readme, zipped.
setlocal
cd /d "%~dp0"

set STAGE=dist\tdebug-portable
set GOPROXY=https://goproxy.cn,direct

if exist "%STAGE%" rmdir /s /q "%STAGE%"
mkdir "%STAGE%"

echo [1/4] Building web frontend ...
if not exist web\dist (
    pushd web
    call npm install
    if errorlevel 1 (echo NPM INSTALL FAILED & popd & exit /b 1)
    call npm run build
    if errorlevel 1 (echo WEB BUILD FAILED & popd & exit /b 1)
    popd
) else (
    echo   web\dist exists, skip
)

echo [2/4] Building tdebug.exe ...
call go build -trimpath -o tdebug.exe .
if errorlevel 1 (
    echo BUILD FAILED
    exit /b 1
)

echo [3/4] Copying config.json / README.md ...
rem 优先打包本地 config.json;没有则用脱敏示例当模板
set SRC_CFG=config.json
if not exist "%SRC_CFG%" set SRC_CFG=config.example.json
echo   config source: %SRC_CFG%
copy /y "%SRC_CFG%" "%STAGE%\config.json" >nul
if errorlevel 1 (
    echo COPY CONFIG FAILED
    exit /b 1
)
copy /y README.md "%STAGE%\" >nul
copy /y tdebug.exe "%STAGE%\" >nul

echo [4/4] Packing zip ...
python -c "import zipfile, os; root='dist/tdebug-portable'; z=zipfile.ZipFile('dist/tdebug-portable.zip','w',zipfile.ZIP_DEFLATED); [z.write(os.path.join(root,n),'tdebug-portable/'+n) for n in os.listdir(root)]; z.close()" >nul 2>nul
if errorlevel 1 (
    echo   python unavailable, falling back to PowerShell ...
    powershell -NoProfile -Command "Compress-Archive -Path '%STAGE%' -DestinationPath 'dist\tdebug-portable.zip' -Force" >nul 2>nul
    if errorlevel 1 (
        echo ZIP FAILED
        exit /b 1
    )
)

echo Done: dist\tdebug-portable.zip
dir /b dist
endlocal
