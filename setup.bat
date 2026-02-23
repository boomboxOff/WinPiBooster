@echo off
net session >nul 2>&1
if %errorlevel% neq 0 (
    powershell -Command "Start-Process '%~f0' -Verb RunAs"
    exit /b
)

set TASK_NAME=WinPiBooster
set BAT_PATH="%~dp0WinPiBooster.bat"

schtasks /query /tn "%TASK_NAME%" >nul 2>&1
if %errorlevel% equ 0 (
    echo La tache '%TASK_NAME%' existe deja.
    set /p CONFIRM=Voulez-vous la remplacer ? (O/N) :
    if /i not "%CONFIRM%"=="O" (
        echo Annulation.
        pause
        exit /b
    )
    schtasks /delete /tn "%TASK_NAME%" /f
)

powershell -ExecutionPolicy Bypass -Command "$action = New-ScheduledTaskAction -Execute '%BAT_PATH%'; $trigger = New-ScheduledTaskTrigger -AtStartup; $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit 0 -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1); $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest; Register-ScheduledTask -TaskName '%TASK_NAME%' -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null"

if %errorlevel% equ 0 (
    echo Tache '%TASK_NAME%' enregistree avec redemarrage automatique en cas de crash.
    echo Le script demarrera automatiquement a chaque demarrage de Windows.
) else (
    echo Erreur lors de l'enregistrement de la tache.
)
pause
