@echo off
cd /d "%~dp0"
title cliVoiceToClipboard

if not exist "bin\whisper-server.exe" (
    echo [v2c] First time setup detected.
    echo [v2c] Downloading whisper-server and model weights...
    powershell -ExecutionPolicy Bypass -File .\setup.ps1
    if errorlevel 1 (
        echo [v2c] Setup failed. Please check your internet connection.
        pause
        exit /b 1
    )
)

echo.
echo ========================================================
echo   cliVoiceToClipboard is starting!
echo   Default Push-to-Talk Key: F12
echo   Hold F12 to talk, release to copy to clipboard!
echo ========================================================
echo.

v2c.exe --backend cuda --hotkey F12
if errorlevel 1 (
    echo.
    echo [v2c] CUDA launch failed or no NVIDIA GPU detected.
    echo [v2c] Attempting CPU fallback mode...
    echo.
    v2c.exe --backend cpu --threads 6 --hotkey F12
)

pause
