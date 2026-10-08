# Keeps peer-1's validator node running on DardenPC. Run by the scheduled
# task "Aether Peer-1" (at boot and every 5 minutes, no time limit). Copy
# to C:\aether-data\peer1-ensure.ps1. To upgrade: put the new binary beside
# the old as aetherd-<commit>.exe and change $exe; see docs/INJECTIVE-TESTNET.md.
$ErrorActionPreference = "Stop"
$homeDir = "C:\aether-peer1"
$exe = "C:\aether-data\aetherd-12b2d15.exe"
$log = "C:\aether-data\peer1-ensure.log"
$nodeLog = "C:\aether-data\peer1-node.log"
function Write-Log($msg) {
  $line = "{0} {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $msg
  Add-Content -Path $log -Value $line
}
# peer-1 counts as running if anything holds its RPC port, or any aetherd
# build of any name runs on its home. Never start a second copy.
if (Get-NetTCPConnection -LocalPort 26667 -State Listen -ErrorAction SilentlyContinue) {
  Write-Log "already running (port 26667 in use)"
  exit 0
}
$procs = @(Get-CimInstance Win32_Process | Where-Object {
  $_.Name -like "aetherd*.exe" -and $_.CommandLine -and ($_.CommandLine -match [regex]::Escape("--home C:\aether-peer1") -or $_.CommandLine -match [regex]::Escape("--home=C:\aether-peer1"))
})
if ($procs.Count -gt 0) {
  Write-Log ("already running pid=" + (($procs | ForEach-Object { $_.ProcessId }) -join ",") + " name=" + (($procs | ForEach-Object { $_.Name }) -join ","))
  exit 0
}
Write-Log "starting: $exe start --home $homeDir"
$p = Start-Process -FilePath $exe -ArgumentList @("start", "--home", $homeDir) -PassThru -Wait -WindowStyle Hidden -RedirectStandardOutput $nodeLog -RedirectStandardError "$nodeLog.err"
$code = $p.ExitCode
Write-Log "exited code=$code"
if ($null -eq $code) { exit 1 }
exit $code
