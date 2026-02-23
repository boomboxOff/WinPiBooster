@echo off
net session >nul 2>&1
if %errorlevel% neq 0 (
    powershell -Command "Start-Process '%~f0' -Verb RunAs"
    exit /b
)

set EXE="%~dp0WinPiBooster.exe"

sc query WinPiBooster >nul 2>&1
if %errorlevel% equ 0 (
    echo Le service 'WinPiBooster' existe deja.
    set /p CONFIRM=Voulez-vous le reinstaller ? (O/N) :
    if /i not "%CONFIRM%"=="O" (
        echo Annulation.
        pause
        exit /b
    )
    %EXE% stop >nul 2>&1
    %EXE% remove
)

%EXE% install
if %errorlevel% equ 0 (
    %EXE% start
    echo.
    echo Service 'WinPiBooster' installe et demarre automatiquement au demarrage de Windows.
    echo Pour le desinstaller : WinPiBooster.exe stop ^& WinPiBooster.exe remove
) else (
    echo Erreur lors de l'installation du service.
)
pause
