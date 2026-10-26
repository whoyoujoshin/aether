# Swap or roll back peer-1's aetherd on DardenPC.
# Dry-run unless -Apply. Run from an elevated PowerShell: the scheduled
# task "Aether Peer-1" starts the process with highest privileges.
#
# The voting-power rule matches cmd/chainwatch SafeToStop and
# scripts/ops/lib.sh: the others must still hold strictly more than 2/3.
# Rollback (-Rollback) runs only while the height is still below
# -ActivationHeight. See docs/OPS-RUNBOOK.md.
#
#   .\cutover-peer1.ps1 -Binary C:\aether-data\aetherd-NEW.exe -Sha256 <hex> -ActivationHeight 460800
#   .\cutover-peer1.ps1 -Apply -Binary ... -Sha256 ... -ActivationHeight 460800
#   .\cutover-peer1.ps1 -Rollback -ActivationHeight 460800 -HeightRpc https://<another node's RPC>
#   .\cutover-peer1.ps1 -Apply -Rollback -ActivationHeight 460800 -HeightRpc https://<another node's RPC>
#
# While peer-1 holds a third or more of the voting power, stopping it
# halts blocks until it is back, so the swap is refused unless -AcceptHalt
# is passed. Every other check still applies.

[CmdletBinding()]
param(
  [switch]$Apply,
  [switch]$Rollback,
  # Swap even though stopping peer-1 leaves the others at 2/3 or less:
  # blocks stop until it is back (seconds, if the new binary starts).
  [switch]$AcceptHalt,
  # Other nodes' RPCs to read the height from when peer-1's own doesn't
  # answer (a new binary that won't start). The highest height wins.
  [string[]]$HeightRpc = @(),
  [string]$Binary = "",
  [string]$Sha256 = "",
  [string]$DestExe = "",
  [long]$ActivationHeight = 0,
  [string]$BackupSuffix = ".pre-cutover",
  [string]$HomeDir = "C:\aether-peer1",
  [string]$Rpc = "http://127.0.0.1:26667",
  [int]$Port = 26667,
  [string]$TaskName = "Aether Peer-1",
  [string]$EnsureScript = "C:\aether-data\peer1-ensure.ps1",
  [int]$MinPeers = 1,
  [string]$ChainId = "",
  [int]$WaitSeconds = 300
)

$ErrorActionPreference = "Stop"

function Test-SafeToStop([long]$Total, [long]$Power) {
  if ($Total -le 0 -or $Power -lt 0 -or $Power -gt $Total) { return $false }
  $rem = [bigint]$Total - [bigint]$Power
  return ($rem * 3) -gt ([bigint]$Total * 2)
}

function Test-Quorum([long]$Signed, [long]$Total) {
  if ($Signed -le 0 -or $Total -le 0) { return $false }
  return (([bigint]$Signed * 3) -gt ([bigint]$Total * 2))
}

function Get-Sha256Hex([string]$Path) {
  return (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLower()
}

function Get-Validators([string]$Base) {
  $all = @()
  $page = 1
  do {
    $body = Invoke-RestMethod -Uri "$Base/validators?per_page=100&page=$page" -TimeoutSec 20
    if ($body.result.validators) { $all += @($body.result.validators) }
    $total = [int]$body.result.total
    $page++
  } while ($all.Count -lt $total -and $page -le 20)
  return $all
}

function Invoke-Step([string]$Description, [scriptblock]$Action) {
  if (-not $Apply) {
    Write-Host "dry-run: $Description"
    return
  }
  & $Action
}

if ($ActivationHeight -le 0) {
  throw "Pass -ActivationHeight (the height where the new binary's rules begin). Rollback is refused at and after that height."
}
if (-not $Rollback -and (-not $Binary -or -not $Sha256)) {
  throw "Pass -Binary and -Sha256, or -Rollback."
}

$Rpc = $Rpc.TrimEnd("/")
Write-Host $(if ($Apply) { "apply: peer-1 will be restarted" } else { "dry-run: preflight runs for real; peer-1 will not be restarted. Pass -Apply to perform it." })

function Get-Height([string]$Base) {
  try {
    return [long](Invoke-RestMethod -Uri "$($Base.TrimEnd('/'))/status" -TimeoutSec 20).result.sync_info.latest_block_height
  } catch {
    return $null
  }
}

if ($Rollback) {
  # peer-1's own RPC is down when its new binary won't start, which is
  # when a rollback is wanted, so the other nodes' heights count too. The
  # highest one never allows a rollback the chain is already past.
  $height = $null
  foreach ($u in @($Rpc) + $HeightRpc) {
    $h = Get-Height $u
    if ($null -eq $h) { Write-Host "$u gives no height"; continue }
    Write-Host "$u reports height $h"
    if ($null -eq $height -or $h -gt $height) { $height = $h }
  }
  if ($null -eq $height) {
    throw "refusing rollback: no RPC answered with a height. Pass -HeightRpc with another node's RPC."
  }
  $bak = "$EnsureScript$BackupSuffix"
  if (-not (Test-Path $bak)) { throw "no backup at $bak" }
  if ($height -ge $ActivationHeight) {
    throw "refusing rollback: height $height, activation $ActivationHeight. Roll back only while the height is still below the activation height."
  }
  Write-Host "rollback will restore $bak over $EnsureScript and restart the task"
  Invoke-Step "restore $bak" { Copy-Item -Force $bak $EnsureScript }
  Invoke-Step "stop listener on $Port and start task $TaskName" {
    Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force }
    Start-ScheduledTask -TaskName $TaskName
  }
  if (-not $Apply) { Write-Host "dry-run: no change made"; return }
  Write-Host "peer-1 rolled back to the ensure-script backup"
  return
}

$status = Invoke-RestMethod -Uri "$Rpc/status" -TimeoutSec 20
$height = [long]$status.result.sync_info.latest_block_height
# JSON false must stay false. A non-empty string would be $true under [bool].
$catching = ("$($status.result.sync_info.catching_up)" -eq "True")
$network = [string]$status.result.node_info.network
$mine = ([string]$status.result.validator_info.address).ToUpper()
$net = Invoke-RestMethod -Uri "$Rpc/net_info" -TimeoutSec 20
$peers = [int]$net.result.n_peers
$block = Invoke-RestMethod -Uri "$Rpc/block" -TimeoutSec 20
$vals = @(Get-Validators $Rpc)

$total = [long]0
$power = $null
foreach ($v in $vals) {
  $vp = [long]$v.voting_power
  $total += $vp
  if (([string]$v.address).ToUpper() -eq $mine) { $power = $vp }
}
$signed = [long]0
$signedMine = $false
$sigs = @($block.result.block.last_commit.signatures)
foreach ($sig in $sigs) {
  if ([int]$sig.block_id_flag -ne 2) { continue }
  $addr = ([string]$sig.validator_address).ToUpper()
  if ($addr -eq $mine) { $signedMine = $true }
  foreach ($v in $vals) {
    if (([string]$v.address).ToUpper() -eq $addr) { $signed += [long]$v.voting_power }
  }
}

Write-Host "height $height catching_up=$catching peers=$peers chain=$network validator=$mine power=$power/$total signed=$signed/$total"

if ($ChainId -and $network -ne $ChainId) { throw "chain id $network, want $ChainId" }

if ($catching) { throw "node reports catching_up; wait until it is synced" }
if ($peers -lt $MinPeers) { throw "peers $peers, want at least $MinPeers" }
if ($null -eq $power) { throw "$mine is not in the validator set" }
if (-not (Test-SafeToStop $total $power)) {
  if (-not $AcceptHalt) {
    throw "UNSAFE to stop $mine (power $power of $total). The others would hold 2/3 or less and the chain would halt until peer-1 is back. Pass -AcceptHalt to swap it anyway."
  }
  Write-Host "WARNING: $mine holds $power of $total. Blocks stop until peer-1 is back (-AcceptHalt)."
}
if (-not (Test-Quorum $signed $total)) {
  throw "the latest block has $signed of $total voting power signed, which is not more than 2/3"
}
if (-not $signedMine) { throw "$mine did not sign the latest block" }

$got = Get-Sha256Hex $Binary
if ($got -ne $Sha256.ToLower()) { throw "checksum $got does not match $Sha256" }

if (-not $DestExe) {
  $DestExe = Join-Path (Split-Path -Parent $EnsureScript) (Split-Path -Leaf $Binary)
}
$ensureBak = "$EnsureScript$BackupSuffix"
Write-Host "binary $Binary -> $DestExe ; ensure script $EnsureScript (backup $ensureBak)"

Invoke-Step "copy binary to $DestExe" {
  Copy-Item -Force $Binary $DestExe
  $copied = Get-Sha256Hex $DestExe
  if ($copied -ne $Sha256.ToLower()) { throw "copied checksum $copied does not match" }
}
Invoke-Step "backup $EnsureScript to $ensureBak (kept if it already exists)" {
  if (-not (Test-Path $ensureBak)) { Copy-Item $EnsureScript $ensureBak }
}
Invoke-Step "point `$exe in $EnsureScript at $DestExe" {
  $text = [IO.File]::ReadAllText($EnsureScript)
  $pattern = '(?m)^\$exe\s*=\s*[''"].*[''"]'
  if ($text -notmatch $pattern) { throw "$EnsureScript has no `$exe = assignment" }
  # $$ is a literal $ in a regex replacement.
  $line = ('$exe = "{0}"' -f $DestExe).Replace('$', '$$')
  $updated = ([regex]::new($pattern)).Replace($text, $line, 1)
  $utf8 = New-Object System.Text.UTF8Encoding $false
  [IO.File]::WriteAllText($EnsureScript, $updated, $utf8)
}
Invoke-Step "stop the process on port $Port and start scheduled task $TaskName" {
  Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force }
  Start-ScheduledTask -TaskName $TaskName
}

if (-not $Apply) {
  Write-Host "dry-run: would wait up to ${WaitSeconds}s for catching_up=false and $mine to sign"
  return
}

$deadline = (Get-Date).AddSeconds($WaitSeconds)
$healthy = $false
while ((Get-Date) -lt $deadline) {
  try {
    $st = Invoke-RestMethod -Uri "$Rpc/status" -TimeoutSec 20
    $h2 = [long]$st.result.sync_info.latest_block_height
    $up = ("$($st.result.sync_info.catching_up)" -ne "True")
    $blk = Invoke-RestMethod -Uri "$Rpc/block" -TimeoutSec 20
    $hit = $false
    foreach ($sig in @($blk.result.block.last_commit.signatures)) {
      if ([int]$sig.block_id_flag -eq 2 -and ([string]$sig.validator_address).ToUpper() -eq $mine) { $hit = $true }
    }
    $proc = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    $running = ""
    if ($proc) { $running = (Get-Process -Id $proc.OwningProcess -ErrorAction SilentlyContinue).Path }
    $hashOk = $false
    if ($running) { $hashOk = (Get-Sha256Hex $running) -eq $Sha256.ToLower() }
    if ($up -and $h2 -gt $height -and $hit -and $hashOk) { $healthy = $true; break }
  } catch {
    Write-Host "waiting: $($_.Exception.Message)"
  }
  Start-Sleep -Seconds 5
}
if (-not $healthy) {
  if ($height -lt $ActivationHeight -and (Test-Path $ensureBak)) {
    Write-Host "peer-1 did not return to signing below activation $ActivationHeight; restoring $ensureBak"
    Copy-Item -Force $ensureBak $EnsureScript
    Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force }
    Start-ScheduledTask -TaskName $TaskName
  } else {
    Write-Host "peer-1 did not return to signing. Height $height, activation $ActivationHeight. The new ensure script was left in place because rollback is refused at or after activation."
  }
  throw "peer-1 did not sign within ${WaitSeconds}s"
}
Write-Host "peer-1 is signing on $DestExe"
