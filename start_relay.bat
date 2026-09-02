@echo off
setlocal

REM Runs the media relay for local testing.
REM
REM Only needed to test FORCE_TURN_RELAY on one machine. Without it, a local session
REM connects directly over loopback and never exercises the relay path -- which is the
REM path every deployed session will take, so it is worth proving before deploying.
REM
REM The relay reads backend\.env, the same file the API server reads, so the credentials
REM it accepts and the credentials the server hands out cannot drift apart. Set these
REM there first:
REM
REM   TURN_PUBLIC_IP=127.0.0.1
REM   TURN_SHARED_SECRET=<any 32+ random characters>
REM   FORCE_TURN_RELAY=true
REM
REM Then restart the backend and start a session: the viewer badge should read
REM "Relayed (TURN)" rather than "Direct (P2P)".

echo ==================================================
echo  DRS media relay (local)
echo ==================================================

findstr /R /C:"^TURN_SHARED_SECRET=." "%~dp0backend\.env" >nul 2>&1
if errorlevel 1 (
    echo.
    echo  TURN_SHARED_SECRET is not set in backend\.env.
    echo  The relay will not start without it -- a relay with no secret would
    echo  accept anyone's traffic.
    echo.
    echo  Add these three lines to backend\.env, then run this again:
    echo.
    echo    TURN_PUBLIC_IP=127.0.0.1
    echo    TURN_SHARED_SECRET=change-me-to-32-random-characters
    echo    FORCE_TURN_RELAY=true
    echo.
    pause
    exit /b 1
)

cd /d "%~dp0backend"
go run ./cmd/turnserver

endlocal
