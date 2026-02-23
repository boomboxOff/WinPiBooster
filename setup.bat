@echo off
net session >nul 2>&1
if %errorlevel% neq 0 (
    powershell -Command "Start-Process '%~f0' -Verb RunAs"
    exit /b
)

set TASK_NAME=MajWindowsJs
set BAT_PATH=%~dp0WindowsMAJ.bat

schtasks /query /tn "%TASK_NAME%" >nul 2>&1
if %errorlevel% equ 0 (
    echo La tache '%TASK_NAME%' existe deja. Suppression...
    schtasks /delete /tn "%TASK_NAME%" /f
)

schtasks /create /tn "%TASK_NAME%" /tr "\"%BAT_PATH%\"" /sc onstart /ru SYSTEM /rl HIGHEST /f
if %errorlevel% equ 0 (
    echo Tache '%TASK_NAME%' enregistree. Le script demarrera automatiquement a chaque demarrage de Windows.
) else (
    echo Erreur lors de l'enregistrement de la tache.
)
pause
