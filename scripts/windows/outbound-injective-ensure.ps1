# Keeps outbound.exe relaying Aether -> Injective testnet on DardenPC.
# Run by the scheduled task "Aether Outbound Injective" (2 minutes after
# boot and every 5 minutes, no time limit); see docs/INJECTIVE-TESTNET.md.
$ErrorActionPreference = "Stop"
$exe = "C:\aether-bin\outbound.exe"
$log = "C:\aether-data\outbound-injective-ensure.log"
$outLog = "C:\aether-data\outbound-injective.log"
function Write-Log($msg) {
  $line = "{0} {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $msg
  Add-Content -Path $log -Value $line
}
# Running if anything holds its health port, or any outbound*.exe relays
# over Injective's client of Aether. Never start a second copy: two would
# race each other's Injective account sequence.
if (Get-NetTCPConnection -LocalPort 8096 -State Listen -ErrorAction SilentlyContinue) { exit 0 }
$procs = @(Get-CimInstance Win32_Process | Where-Object {
  $_.Name -like "outbound*.exe" -and $_.CommandLine -and $_.CommandLine -match "07-tendermint-510"
})
if ($procs.Count -gt 0) {
  Write-Log ("already running pid=" + (($procs | ForEach-Object { $_.ProcessId }) -join ","))
  exit 0
}
# Keep the previous run's log, so a crash's last lines survive the restart.
if (Test-Path $outLog) { Move-Item $outLog "$outLog.prev" -Force }
$argList = @(
  "-aether-rpc", "http://127.0.0.1:26667",
  "-cparty-rpc", "https://testnet.sentry.tm.injective.network:443",
  "-cparty-grpc", "testnet.sentry.chain.grpc.injective.network:443",
  "-cparty-chain-id", "injective-888", "-cparty-bech32-prefix", "inj",
  "-cparty-key", "relayer", "-cparty-home", "C:\aether-relayer\injective",
  "-keyring-backend", "test",
  "-cparty-gas-prices", "160000000inj",
  "-client-id", "07-tendermint-510",
  "-refresh-after", "1h", "-listen", "127.0.0.1:8096"
)
Write-Log "starting: $exe"
$p = Start-Process -FilePath $exe -ArgumentList $argList -PassThru -Wait -WindowStyle Hidden -RedirectStandardError $outLog -RedirectStandardOutput "$outLog.stdout"
Write-Log "exited code=$($p.ExitCode)"
if ($null -eq $p.ExitCode) { exit 1 }
exit $p.ExitCode
