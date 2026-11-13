@echo off
REM Start fusion-console (and the bundled fusion-gateway when present), then
REM open the default browser with one-time auto-login. Windows.
REM The window stays open: the console runs in the foreground here and its
REM logs stream into this window, so failures are always visible.
setlocal EnableDelayedExpansion
cd /d "%~dp0"

set "HERE=%CD%"
set "CONSOLE_BIN=%HERE%\fusion-console.exe"
set "GATEWAY_BIN=%HERE%\fusion-gateway.exe"
if not exist "%CONSOLE_BIN%" set "CONSOLE_BIN=%USERPROFILE%\.fusion-console\bin\fusion-console.exe"
if not exist "%GATEWAY_BIN%" set "GATEWAY_BIN=%USERPROFILE%\.fusion-console\bin\fusion-gateway.exe"

set "LOGDIR=%USERPROFILE%\.fusion-console\log"
if not exist "%LOGDIR%" mkdir "%LOGDIR%"

echo [startup] fusion-console launcher - package dir %HERE%

REM 1. One-time: replace the packaged bootstrap JWT secret with a random one.
if exist "%HERE%\config.yaml" (
    findstr /c:"FUSION-CONSOLE-BOOTSTRAP-SECRET-0000" "%HERE%\config.yaml" >nul 2>&1
    if not errorlevel 1 (
        powershell -NoProfile -Command "$s=-join((1..64)|%%{'{0:x}' -f (Get-Random -Max 16)}); (Get-Content -LiteralPath '%HERE%\config.yaml') -replace 'FUSION-CONSOLE-BOOTSTRAP-SECRET-00000000000000000000000000000000', $s | Set-Content -LiteralPath '%HERE%\config.yaml'"
        echo [startup] gateway jwt secret generated in config.yaml
    )
)

REM 2. Gateway: start only when a binary and a config exist and :11432 is free.
set "GW_CFG=%~1"
if "%GW_CFG%"=="" set "GW_CFG=%FUSION_GATEWAY_CONFIG%"
if "%GW_CFG%"=="" if exist "%HERE%\config.yaml" set "GW_CFG=%HERE%\config.yaml"
if "%GW_CFG%"=="" if exist "%USERPROFILE%\.fusion-gateway\config.yaml" set "GW_CFG=%USERPROFILE%\.fusion-gateway\config.yaml"

netstat -ano | findstr /r ":11432 .*LISTENING" >nul 2>&1
if !errorlevel! equ 0 (
    echo [startup] gateway already running on :11432
    goto console
)
if not exist "%GATEWAY_BIN%" (
    echo [startup] gateway skipped: fusion-gateway.exe not found
    goto console
)
if "%GW_CFG%"=="" (
    echo [startup] gateway skipped: no config.yaml found
    goto console
)
echo [startup] starting fusion-gateway on :11432 - config %GW_CFG%
start "" /b "%GATEWAY_BIN%" -config "%GW_CFG%" > "%LOGDIR%\gateway.log" 2>&1
timeout /t 2 /nobreak >nul
netstat -ano | findstr /r ":11432 .*LISTENING" >nul 2>&1
if errorlevel 1 (
    echo [startup] WARNING: gateway did not open :11432 - check %LOGDIR%\gateway.log
) else (
    echo [startup] gateway is up on :11432
)

:console
REM 3. Console in the foreground; -open launches the default browser.
netstat -ano | findstr /r ":11480 .*LISTENING" >nul 2>&1
if !errorlevel! equ 0 (
    echo [startup] console already running - open http://127.0.0.1:11480
    pause
    goto end
)
if not exist "%CONSOLE_BIN%" (
    echo [startup] ERROR: fusion-console.exe not found
    pause
    goto end
)
echo [startup] starting fusion-console on http://127.0.0.1:11480 - browser opens automatically
echo [startup] (console logs stream below; keep this window open - closing it stops the console^)
"%CONSOLE_BIN%" -open
echo [startup] console exited unexpectedly
pause

:end
endlocal
