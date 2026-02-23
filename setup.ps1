# setup.ps1 — Enregistre MajWindowsJs dans le Planificateur de tâches Windows
# Doit être exécuté en tant qu'administrateur

param (
    [switch]$Uninstall
)

$taskName = "MajWindowsJs"
$batPath  = Join-Path $PSScriptRoot "WindowsMAJ.bat"

if ($Uninstall) {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    Write-Host "Tâche '$taskName' supprimée du Planificateur de tâches."
    exit 0
}

$action    = New-ScheduledTaskAction -Execute $batPath
$trigger   = New-ScheduledTaskTrigger -AtStartup
$settings  = New-ScheduledTaskSettingsSet -ExecutionTimeLimit 0 -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest

Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

Write-Host "Tâche '$taskName' enregistrée — le script démarrera automatiquement à chaque démarrage de Windows."
