@echo off
REM Start fusion-console (and the bundled fusion-gateway when present), then
REM open the default browser with one-time auto-login. Windows.
setlocal EnableDelayedExpansion
cd /d "%~dp0"

set "HERE=%CD%"
set "CONSOLE_BIN=%HERE%\fusion-console.exe"
set "GATEWAY_BIN=%HERE%\fusion-gateway.exe"
if not exist "%CONSOLE_BIN%" set "CONSOLE_BIN=%USERPROFILE%\.fusion-console\bin\fusion-console.exe"
if not exist "%GATEWAY_BIN%" set "GATEWAY_BIN=%USERPROFILE%\.fusion-console\bin\fusion-gateway.exe"

if not exist "%USERPROFILE%\.fusion-console\log" mkdir "%USERPROFILE%\.fusion-console\log"

REM 1. Gateway: start only when a binary and a config exist and :11432 is free.
set "GW_CFG=%~1"
if "%GW_CFG%"=="" set "GW_CFG=%FUSION_GATEWAY_CONFIG%"
if "%GW_CFG%"=="" if exist "%HERE%\config.yaml" set "GW_CFG=%HERE%\config.yaml"
if "%GW_CFG%"=="" if exist "%USERPROFILE%\.fusion-gateway\config.yaml" set "GW_CFG=%USERPROFILE%\.fusion-gateway\config.yaml"

netstat -ano | findstr /r ":11432 .*LISTENING" >nul 2>&1
if !errorlevel! equ 0 (
    echo gateway: already running on :11432
) else if exist "%GATEWAY_BIN%" if not "%GW_CFG%"=="" if exist "%GW_CFG%" (
    start "" /b "%GATEWAY_BIN%" -config "%GW_CFG%" > "%USERPROFILE%\.fusion-console\log\gateway.log" 2>&1
    echo gateway: started (config %GW_CFG%, log %%USERPROFILE%%\.fusion-console\log\gateway.log^)
) else (
    echo gateway: skipped (binary or config.yaml not found^)
)

REM 2. Console: start if :11480 is free; --open launches the default browser.
netstat -ano | findstr /r ":11480 .*LISTENING" >nul 2>&1
if !errorlevel! equ 0 (
    echo console: already running on :11480 - open http://127.0.0.1:11480
) else if exist "%CONSOLE_BIN%" (
    start "" /b "%CONSOLE_BIN%" --open > "%USERPROFILE%\.fusion-console\log\console.log" 2>&1
    echo console: started - browser opening at http://127.0.0.1:11480 (auto-login^)
) else (
    echo console: fusion-console.exe not found
)
endlocal
