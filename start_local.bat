@echo off
setlocal

echo ==================================================
echo  DRS local development environment
echo ==================================================

REM The backend refuses to start without its secrets, so make sure a .env exists.
if not exist "%~dp0backend\.env" (
    echo Creating backend\.env from .env.example ...
    copy /Y "%~dp0backend\.env.example" "%~dp0backend\.env" >nul
)

echo.
echo Checking PostgreSQL on 127.0.0.1:5432 ...
powershell -NoProfile -Command "if (-not (Test-NetConnection -ComputerName 127.0.0.1 -Port 5432 -InformationLevel Quiet -WarningAction SilentlyContinue)) { Write-Host '  NOT REACHABLE.' -ForegroundColor Red; Write-Host '  Start one with:' -ForegroundColor Yellow; Write-Host '    docker run -d --name drs-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16-alpine' -ForegroundColor Yellow; exit 1 } else { Write-Host '  reachable' -ForegroundColor Green }"
if errorlevel 1 (
    echo.
    echo Start PostgreSQL and run this script again.
    pause
    exit /b 1
)

echo.
echo 1. Backend (port 8080) - applies migrations on start
start "DRS Backend" cmd /k "cd /d %~dp0backend && go run ./cmd/server"

timeout /t 3 >nul

if not exist "%~dp0frontend\node_modules" (
    REM No parentheses in this message: cmd parses the whole if-block before running it,
    REM so an unescaped ')' here closes the block early and the rest of the line is left
    REM dangling as a command -- "... was unexpected at this time."
    echo Installing frontend dependencies via npm install ...
    pushd "%~dp0frontend"
    call npm install
    popd
)

echo 2. Portal (port 3000)
start "DRS Portal" cmd /k "cd /d %~dp0frontend && npm run dev"

timeout /t 3 >nul

start http://localhost:3000

echo.
echo ==================================================
echo  Portal:   http://localhost:3000
echo  Login:    see DEFAULT_SUPERADMIN_* in backend\.env
echo.
echo  Next: build and enroll the agent
echo    powershell -File agents\windows\build.ps1
echo    agents\windows\build\drs-agent.exe enroll -server http://localhost:8080 -token DRS-XXXXXX
echo    agents\windows\build\drs-agent.exe
echo ==================================================
endlocal
