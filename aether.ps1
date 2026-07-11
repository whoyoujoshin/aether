# aether.ps1 — wrapper for aetherd that always uses the correct --home,
# so commands can never silently point at the wrong (default) location again.
#
# Usage:
#   .\aether.ps1 reset                 # full wipe + fresh init + print new validator pubkey
#   .\aether.ps1 start                 # start the node (with min-gas-prices set)
#   .\aether.ps1 keys add <name>       # any other aetherd subcommand, home is injected automatically
#   .\aether.ps1 tx pow submit ...     # etc — anything after the first word(s) is passed through
#
# Put this file in your project root (C:\aether-data) and run it with .\aether.ps1 <command>

$HomeDir = "C:\Users\jdard\.aether"

function Invoke-Aetherd {
    param([string[]]$Args)
    go run ./cmd/aetherd @Args --home $HomeDir
}

$cmd = $args[0]
$rest = $args[1..($args.Length - 1)]

switch ($cmd) {
    "reset" {
        Write-Host "Wiping $HomeDir ..." -ForegroundColor Yellow
        Remove-Item -Recurse -Force $HomeDir -ErrorAction SilentlyContinue

        Write-Host "Running fresh init ..." -ForegroundColor Yellow
        go run ./cmd/aetherd init mynode --chain-id aether-testnet-1 --overwrite --home $HomeDir

        Write-Host ""
        Write-Host "Validator pubkey (copy the 'key' value into genesis.json's consensus.validators):" -ForegroundColor Cyan
        go run ./cmd/aetherd comet show-validator --home $HomeDir

        Write-Host ""
        Write-Host "Genesis file to edit:" -ForegroundColor Cyan
        Write-Host "  $HomeDir\config\genesis.json"
        Write-Host ""
        Write-Host "After editing genesis.json, run: .\aether.ps1 start" -ForegroundColor Green
    }
    "start" {
        Invoke-Aetherd @("start", "--minimum-gas-prices=0.0001aeth")
    }
    "genesis" {
        # quick shortcut to open the genesis file directly
        notepad "$HomeDir\config\genesis.json"
    }
    default {
        # anything else: pass every argument straight through to aetherd, with --home appended
        Invoke-Aetherd $args
    }
}
