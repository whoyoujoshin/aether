# Registers the scheduled task that runs outbound-injective-ensure.ps1.
# Run once, from an Administrator PowerShell, after copying that script to
# C:\aether-data. It runs as the same account as "Aether Peer-1".
$action   = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File C:\aether-data\outbound-injective-ensure.ps1"
$boot     = New-ScheduledTaskTrigger -AtStartup; $boot.Delay = "PT2M"
$every    = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) -RepetitionInterval (New-TimeSpan -Minutes 5) -RepetitionDuration (New-TimeSpan -Days 9999)
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal = (Get-ScheduledTask 'Aether Peer-1').Principal
Register-ScheduledTask -TaskName 'Aether Outbound Injective' -Action $action -Trigger $boot,$every -Principal $principal -Settings $settings | Out-Null
Get-ScheduledTask 'Aether Outbound Injective' | Format-List TaskName, State
