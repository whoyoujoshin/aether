# v0.2.18-testnet on the testnet: tipping, the price query, every tool paying the minimum

This release changes no consensus rule on `aether-testnet-1`, so there is
no activation height and nothing halts. A node on 0.2.17 and a node on
0.2.18 agree on every block; validators can swap in any order, one at a
time, whenever it suits.

What a node gets from the swap:

- **Tipping** (`app/tipping.go`, #207). The node keeps waiting
  transactions in the SDK's priority mempool and, when it proposes, takes
  them in order of fee per gas over its minimum price, a lone mining find
  first. Only the proposer's choice changes; blocks aren't checked against
  it.
- **The price query** (#214). The node answers `cosmos.base.node.v1beta1`
  Config, which carries its `minimum-gas-prices`. Every tool now asks its
  node for the price and pays it (`--gas-price auto`); a node that doesn't
  answer (0.2.17) is taken to ask nothing, which the testnet's nodes ask
  today.

The services get the fee rule: agentmcp, the test wallet, the paywalls,
the facilitator, the faucet and the `wallet` CLI pay the node's price,
and `get_transaction_status` reports a transaction's fee apart from its
payment. At the testnet's zero price they pay nothing, as now.

## 1. Build once, on a workstation (never on a validator), with Go 1.26.10

From the tag:

```bash
git fetch origin --tags && git checkout v0.2.18-testnet
go version                             # go1.26.10
go test ./app ./wallet/... ./paywall/... ./cmd/agentmcp/ ./cmd/faucet/
bash scripts/prebuild.sh               # dist/prebuilt-<commit>/, with SHA256SUMS
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/aetherd-$(git rev-parse --short HEAD).exe ./cmd/aetherd
sha256sum dist/prebuilt-*/aetherd dist/aetherd-*.exe
```

(`go test ./wallet/` has three tests that need a running devnet with a
faucet and fail without one, on main too: `TestClient_GetBalance_…`,
`TestClient_GetAccountInfo_…`, `TestWallet_BuildSignAndBroadcast_…`.)

Copy the Linux `aetherd` to sync3, sync4 and the seed, and stage the
`.exe` on the seed for Joshua with its sha256.

## 2. Swap the validators, one at a time

Order: **sync3, sync4, seed** (the seed after Joshua's OK), peer-1 any
time in between. Only while the other three are signing.

**a. Check four validators signed the latest block:**

```bash
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[] | select(.block_id_flag==2) | .validator_address'
```

**b. Swap the binary:**

```bash
BIN=$(systemctl show -p ExecStart aetherd | grep -o '/[^ ;]*aetherd[^ ;]*' | head -1)
sha256sum "$BIN"                       # the build it runs now
sudo cp "$BIN" "$BIN.pre-v0218"
sudo install -m 0755 <prebuilt folder>/aetherd "$BIN.new" && sudo mv "$BIN.new" "$BIN"
sudo systemctl restart aetherd         # if it hangs: sudo systemctl kill -s KILL aetherd && sudo systemctl start aetherd
sha256sum /proc/$(systemctl show -p MainPID --value aetherd)/exe   # matches step 1
```

No data backup is needed: nothing on disk changes, and the old binary
runs on the same data at any time.

**c. Check it's back, signing, and answering the price query:**

```bash
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c "$MYADDR"   # 1
curl -s 'localhost:26657/abci_query?path="/cosmos.base.node.v1beta1.Service/Config"' | jq -r .result.response.code   # 0
```

`app.toml`'s `[mempool] max-txs` stays as it is: at `-1` (what `aetherd
init` writes) aetherd holds 5,000 waiting transactions, the same as
CometBFT's own mempool. The seed's `GOMEMLIMIT` and `iavl-cache-size` stay.

## 3. peer-1 (Joshua, on DardenPC, elevated PowerShell)

```powershell
Get-FileHash C:\aether-data\aetherd-<commit>.exe     # must match Gitty's sum
Copy-Item C:\aether-data\peer1-ensure.ps1 C:\aether-data\peer1-ensure.ps1.pre-v0218
```

1. In `C:\aether-data\peer1-ensure.ps1`, change the binary to
   `aetherd-<commit>.exe`. Keep the old `.exe`.
2. Stop the process on port 26667 and start the task:
   ```powershell
   Get-NetTCPConnection -LocalPort 26667 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
   Start-ScheduledTask -TaskName "Aether Peer-1"
   ```
3. Check it:
   ```powershell
   $p = (Get-NetTCPConnection -LocalPort 26667 -State Listen | Select-Object -First 1).OwningProcess
   Get-FileHash (Get-Process -Id $p).Path                     # Gitty's sum
   (Invoke-RestMethod http://127.0.0.1:26667/status).result.sync_info.catching_up   # False
   ```

Every `aetherd` command on DardenPC takes `--home C:\aether-peer1`. Keep
the VPN off.

## 4. The seed's services, from the same prebuilt folder

```bash
cd /root/aether-src && git fetch origin --tags && git checkout v0.2.18-testnet
PREBUILT=<prebuilt folder> bash scripts/agentservices/install.sh     # paywalls, agentservices, facilitator
PREBUILT=<prebuilt folder> KEYRING_BACKEND=file bash scripts/agentservices/sandbox.sh   # the test wallet
REPO=/root/aether-src PREBUILT=<prebuilt folder> bash scripts/deploy-explorer.sh
sudo cp /root/agentmcp /root/agentmcp.bak-pre-v0218 && sudo install -m 0755 <prebuilt folder>/agentmcp /root/agentmcp && sudo systemctl restart aether-agentmcp
```

No unit changes are needed: the paywalls, the test wallet and agentmcp
default to `--gas-price auto`, and the faucet's own `--gas-price` (in
`aether-faucet.service.d/gas-price.conf`) is still read as before. The
faucet is in the prebuilt folder too:

```bash
sudo cp /root/faucet /root/faucet.bak-pre-v0218 && sudo install -m 0755 <prebuilt folder>/faucet /root/faucet && sudo systemctl restart aether-faucet
```

(Check the unit's binary path first: `systemctl show -p ExecStart aether-faucet`.)

Checks: `/svc/hello` answers 402, `/mcp` lists its tools,
`get_transaction_status` on a recent payment shows `to` as the payee (not
the fee collector), and a test wallet buys `hello`.

## 5. Later, a separate step: raise the testnet's price to 0.0001uaeth

Only on Joshua's go, after steps 2 to 4. This rehearses aether-1's price
(`docs/MAINNET.md` §4) on a live chain.

- **First tell users.** Anything that sends from an older build pays no
  fee and is refused once the node it talks to asks a price (`insufficient
  fees`): `aether-chain-client` 0.2.17 or older (the Cursor listing pins
  0.2.11), Aether Pay 0.2.17 or older, and anyone's own scripts. 0.2.18
  pays it.
- **Then, node by node** (the same order and checks as step 2), set
  `minimum-gas-prices = "0.0001uaeth"` in `app.toml` (backup
  `app.toml.pre-price`) and restart. Each node decides its own mempool,
  so a transaction sent to a node still at zero can land in a block
  another node proposes; the price is fully on once all four ask it.
- **The faucet's override:** if `gas-price.conf` names a fixed
  `--gas-price`, make it `0.0001uaeth` (or drop it for `auto`).
- **Checks:** a `wallet send` with `--gas-price 0uaeth` is refused
  (`required: 40uaeth`), one with the defaults lands paying 40 uaeth, a
  test wallet buys `hello`, and the faucet still drips.
- **Undo:** put `minimum-gas-prices` back to `0uaeth` and restart, node by
  node.

Native mining submissions need no fee at any price, so the miners keep
running unchanged. Helicase's relays are proposer-included and pay none.

## Rollback

Put `.pre-v0218` back and restart, node by node (peer-1: the old `.exe`
in `peer1-ensure.ps1`). Nothing on disk needs undoing.
