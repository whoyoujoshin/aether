Write-Host "Resetting Aether node data..."
Remove-Item -Recurse -Force "$env:USERPROFILE\.aether\data" -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path "$env:USERPROFILE\.aether\data" | Out-Null
'{"height":"0","round":0,"step":0}' | Set-Content "$env:USERPROFILE\.aether\data\priv_validator_state.json"
Write-Host "Done. Run: go run ./cmd/aetherd start --minimum-gas-prices=`"0.0001aeth`""
