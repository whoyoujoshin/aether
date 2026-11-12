# Become a validator on the Aether testnet, step by step

This takes you from nothing to a validator seat on `aether-testnet-1`, the
public testnet. Nothing on it has value. You need a Linux server and an
hour of attention, then time while your node catches up.

**How seats work, in one paragraph.** Nobody hands out seats and nothing
is staked. You run two things: a **node**, which signs blocks, and a
**miner**, which proves work. A miner with at least 1% of the network's
work over the last three epochs (an epoch is 1,440 blocks, about 2.2 hours)
gets a seat for the node it registered. Your power follows your share of
the work, but moves at most 10 points of the total each epoch, so a
newcomer starts with at most 10% and climbs from there. The full rule is in [SEAT-RULE.md](SEAT-RULE.md).

**On Windows or a Mac?** Steps 0 to 10 below are for a Linux server. The
same steps for a Windows PC are in [On Windows](#on-windows), and for a
Mac in [On a Mac](#on-a-mac), further down.

Want to run one on the mainnet rehearsal instead? That's `aether-rc-1`,
by application: see [RC-1.md](RC-1.md) and [OPERATOR.md](OPERATOR.md).

---

## Step 0. Get a server

| | Enough for the testnet |
|---|---|
| CPU | 4 cores (more if the miner runs on the same box) |
| Memory | 8 GB |
| Disk | 80 GB SSD (the chain is about 6 GB and growing) |
| System | Ubuntu 22.04 or 24.04, 64-bit |
| Network | a public IP, port **26656/tcp open** inbound |

A cloud server (about $40 a month) is better than a home PC: a validator
that drops off its network keeps the others waiting.

Log in to it and install two small tools:

```bash
sudo apt-get update && sudo apt-get install -y curl jq
```

## Step 1. Download the programs

From the [latest release](https://github.com/whoyoujoshin/aether/releases/latest)
(**`v0.2.21-testnet` or later**: earlier releases don't carry the testnet's
rules from block 712,080 or can't state-sync a new node), download **`aether-linux-amd64.tar.gz`** and
unpack it:

```bash
cd ~
TAG=v0.2.21-testnet          # or whatever the latest release is called
curl -LO https://github.com/whoyoujoshin/aether/releases/download/$TAG/aether-linux-amd64.tar.gz
curl -LO https://github.com/whoyoujoshin/aether/releases/download/$TAG/SHA256SUMS   # from v0.2.22-testnet on
sha256sum -c SHA256SUMS --ignore-missing     # aether-linux-amd64.tar.gz: OK
mkdir -p aether-bin && tar -xzf aether-linux-amd64.tar.gz -C aether-bin
sudo install -m 0755 aether-bin/aetherd aether-bin/powminer aether-bin/validatorkeygen aether-bin/minerwatch /usr/local/bin/
which aetherd powminer validatorkeygen     # all three found
```

You'll use four of the programs in it:

| Program | What it does |
|---|---|
| `aetherd` | the node |
| `powminer` | the miner |
| `validatorkeygen` | ties your node's signing key to your miner |
| `minerwatch` | optional alerts about your seat |

## Step 2. Set up the node

```bash
aetherd init <pick-a-name> --chain-id aether-testnet-1
curl -L -o ~/.aether/config/genesis.json \
  https://raw.githubusercontent.com/whoyoujoshin/aether/main/testnet/genesis.json
sha256sum ~/.aether/config/genesis.json   # b6f36f01783ed02a4a353ac825deb07c68a5683cdaca92b879e759c0cb988785
```

That is the genesis file the validators run, byte for byte.

Set three lines in `~/.aether/config/config.toml`: `seeds` and
`external_address` (under `[p2p]`) and `timeout_commit` (under
`[consensus]`). Put your server's public IP in the first line:

```bash
IP=<your server's public IP>
sed -i \
  -e 's#^seeds = .*#seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"#' \
  -e "s#^external_address = .*#external_address = \"$IP:26656\"#" \
  -e 's#^timeout_commit = .*#timeout_commit = "5s"#' \
  ~/.aether/config/config.toml
grep -E '^(seeds|external_address|timeout_commit) ' ~/.aether/config/config.toml   # the three lines, set
```

**`timeout_commit` must be `"5s"`** on every validator. One validator with a
longer value makes every block wait for it.

**Start from a recent snapshot (state sync).** A new node can't replay the
testnet from block 1 with today's `aetherd`: the early blocks were made by
older builds. Instead it downloads the state at a recent height from a node
that serves snapshots, checks it against the chain, and carries on from
there. Set it up before the node's first start:

```bash
RPC=https://rpc.157-245-252-221.sslip.io
TRUST=$(( $(curl -s $RPC/block | jq -r .result.block.header.height) - 1000 ))
HASH=$(curl -s "$RPC/block?height=$TRUST" | jq -r .result.block_id.hash)
echo "trusting block $TRUST, hash $HASH"
sed -i -e '/^\[statesync\]/,/^\[/{' \
  -e 's/^enable = .*/enable = true/' \
  -e "s#^rpc_servers = .*#rpc_servers = \"$RPC:443,$RPC:443\"#" \
  -e "s/^trust_height = .*/trust_height = $TRUST/" \
  -e "s/^trust_hash = .*/trust_hash = \"$HASH\"/" \
  -e '}' ~/.aether/config/config.toml
sed -i 's#^persistent_peers = .*#persistent_peers = "5ddca5a28f3a9dcd20b21f57c87ed4a2f289d922@64.23.217.145:26656"#' ~/.aether/config/config.toml
```

`persistent_peers` names sync3, a testnet node that serves snapshots, so
yours finds one straight away. State sync checks the snapshot against
the chain through the `rpc_servers` before trusting it. The setting needs
two, and the seed's is the only public RPC for now, so it is named twice:
the check then trusts the seed alone, with no second node to compare it
with. A second public RPC will be listed here once there is one. State sync needs **`v0.2.21-testnet` or later**: earlier
builds can't restore a snapshot on a new node.

## Step 3. Run it as a service

```bash
sudo tee /etc/systemd/system/aetherd.service >/dev/null <<EOF
[Unit]
Description=Aether node
After=network-online.target

[Service]
User=$USER
ExecStart=/usr/local/bin/aetherd start --home $HOME/.aether
Restart=always
RestartSec=5
LimitNOFILE=65535
Environment=GOMEMLIMIT=5000MiB

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now aetherd
journalctl -u aetherd -f          # Ctrl-C stops watching, not the node
```

## Step 4. Wait for it to catch up

Within a minute or two the node finds a snapshot and restores it. The
log shows, in order:

```
Offering snapshot to ABCI app        height=...
Snapshot accepted, restoring
Applied snapshot chunk to ABCI app   chunk=0 total=1
Verified ABCI app                    appHash=...
Snapshot restored
Time to switch to consensus reactor! height=...
```

Today's snapshots are a single chunk. The last line means the restore is
done and the node is following new blocks. Check progress with:

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
```

Compare `latest_block_height` with the explorer
(<https://explorer.157-245-252-221.sslip.io>). You're caught up when
`catching_up` is `false`, a few minutes after the restore (on 2026-11-12 a
new node restored height 716,600 and was caught up about a minute later).

**If it says `no available snapshots` or keeps looking:** check that
`persistent_peers` is set as in step 2 and that the node can reach the
internet. **If the restore fails** (`state sync aborted`, or an error
naming a store): you're on a build older than `v0.2.21-testnet`, or
something new; stop the node (`sudo systemctl stop aetherd`), open an
issue with the last 50 log lines, and don't delete anything.

Once caught up, the node runs normally; state sync only ever runs on an
empty node, so later restarts don't repeat it.

**Don't start the next steps until it has caught up.** A validator that
gets a seat before its node is current misses blocks.

## Step 5. Make your miner's key

```bash
aetherd keys add miner --keyring-backend file
```

It asks for a passphrase to encrypt the key, then prints your miner's
address (`aether1...`) and a recovery phrase. **Write the phrase down on
paper and keep it off the server.** Anyone with it controls the account.

Save the passphrase where the miner can read it at start-up, readable only
by you:

```bash
read -rs -p "Keyring passphrase: " P && printf '%s' "$P" > ~/.aether/keyring-pass && chmod 600 ~/.aether/keyring-pass; unset P; echo
MINER=$(aetherd keys show miner -a --keyring-backend file)
echo $MINER
```

## Step 6. Open the account with the faucet

A brand-new address doesn't exist on the chain until something is sent to
it. One faucet drip is enough:

```bash
curl -s -X POST https://faucet.157-245-252-221.sslip.io/request \
  -H 'Content-Type: application/json' -d "{\"address\":\"$MINER\"}"
```

Check it arrived (wait for the next block, about 6 seconds):

```bash
aetherd q bank balances $MINER
```

## Step 7. Start mining

```bash
sudo tee /etc/systemd/system/powminer.service >/dev/null <<EOF
[Unit]
Description=Aether miner
After=aetherd.service

[Service]
User=$USER
Environment=AETHER_KEYRING_PASSPHRASE_FILE=$HOME/.aether/keyring-pass
ExecStart=/usr/local/bin/powminer --miner $MINER --from miner --keyring-backend file \
  --chain-id aether-testnet-1 --rpc http://127.0.0.1:26657 --grpc localhost:9090 \
  --auto-submit --loop --fees 0uaeth
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now powminer
journalctl -u powminer -f
```

Each find is a "Found valid nonce" line followed by an accepted
transaction. Mining needs no fee, and each accepted find pays you a
reward. **Make the miner start at boot** (the `enable` above does): a
validator whose miner stops loses its seat at the next epochs. On a server
whose cores the node also needs, add `--duty 0.6` to hash 60% of the time.

## Step 8. Register your node's key to your miner

This tells the chain which node your miner's work is for. It reads the
node's own signing key (`~/.aether/config/priv_validator_key.json`), so
run it on the node's server:

```bash
validatorkeygen --miner $MINER --home ~/.aether \
  --chain-id aether-testnet-1 --keyring-backend file --fees 200uaeth
```

It prints a command starting `aetherd tx pow register-validator-pubkey`.
Copy it, add `--gas 400000` at the end, and run it. It asks for your
keyring passphrase. The fee comes out of your first reward or the faucet
drip.

Check it worked:

```bash
aetherd q pow seat-work $MINER
```

**Back up `~/.aether/config/priv_validator_key.json`** (encrypted, off the
server). **Never run it in two places at once:** two nodes signing with one
key is double-signing, which bans the validator and burns its escrow.
When you move a node, stop the old one first.

## Step 9. Get your seat

At the end of each epoch (blocks ending in …1,439, every 1,440 blocks) the
chain picks the validators. You're in if your miner has at least 1% of the
work over the last three epochs. Watch where you stand:

```bash
aetherd q pow seat-work $MINER        # your work, the 1% bar, whether you meet it
aetherd q pow active-validators       # the current set
```

Or look yourself up on the explorer's Validators page. When you're seated,
your node signs blocks:

```bash
MYADDR=$(curl -s localhost:26657/status | jq -r .result.validator_info.address)
curl -s localhost:26657/status | jq .result.validator_info.voting_power    # above 0
curl -s localhost:26657/block | jq -r '.result.block.last_commit.signatures[].validator_address' | grep -c $MYADDR   # 1
```

## Step 10. Stay in

- **Keep the node and the miner running.** Power follows the last three
  epochs' work, so a stopped miner fades out over a few epochs.
- **Keep signing.** A validator that misses most of the last 1,440 blocks
  can be removed until the next epoch.
- **Rewards:** while you validate, rewards first fill a 1,000 AETH bond
  that stays locked as long as you're active; rewards beyond it are paid
  to your account.
- **Upgrades** come with a block height and a new release. Swap the binary
  before that height: `sudo systemctl stop aetherd`, install the new
  `aetherd`, `sudo systemctl start aetherd`. Watch the
  [releases](https://github.com/whoyoujoshin/aether/releases) page.
- **Alerts (optional):** `minerwatch --address $MINER` reports your work,
  selection and removal, and can post to a webhook (`minerwatch -h`).

## On Windows

The same ten steps for a Windows 10 or 11 PC, in **PowerShell run as
Administrator** (Start menu → type "PowerShell" → right-click → Run as
administrator). Read the Linux steps' explanations above; this section
only gives the Windows commands.

A home PC can validate, but it has to stay on and online. The testnet's
own Windows validator was knocked out by a VPN, by a weak 2.4 GHz Wi-Fi
link, and by its miner not starting again after a reboot, so:

- use a cable or 5 GHz Wi-Fi, and **no VPN**;
- never let it sleep;
- keep the node's folder **out of OneDrive** (a synced folder corrupts the
  chain data). This guide uses `C:\aether` and `C:\aether-node`, which
  OneDrive doesn't touch.

### W0. Keep the PC awake, open the port

```powershell
powercfg /change standby-timeout-ac 0
powercfg /change hibernate-timeout-ac 0
New-NetFirewallRule -DisplayName "Aether P2P" -Direction Inbound -Protocol TCP -LocalPort 26656 -Action Allow
```

On a home network, also forward **TCP port 26656** on your router to this
PC (your router's admin page, "port forwarding"). Find your public IP at
<https://ifconfig.me>.

### W1. Download the programs

```powershell
mkdir C:\aether; cd C:\aether
$TAG = "v0.2.21-testnet"     # or whatever the latest release is called
curl.exe -LO "https://github.com/whoyoujoshin/aether/releases/download/$TAG/aether-windows-amd64.tar.gz"
# From v0.2.22-testnet on, check the download: the two hashes must match.
curl.exe -LO "https://github.com/whoyoujoshin/aether/releases/download/$TAG/SHA256SUMS"
Select-String aether-windows-amd64.tar.gz SHA256SUMS; (Get-FileHash aether-windows-amd64.tar.gz).Hash.ToLower()
tar -xzf aether-windows-amd64.tar.gz
dir *.exe     # aetherd.exe, powminer.exe, validatorkeygen.exe, minerwatch.exe, ...
```

### W2. Set up the node

The node lives in `C:\aether-node`. **Every `aetherd` command that touches
the node takes `--home C:\aether-node`**; without it, aetherd looks in
your user folder instead.

```powershell
C:\aether\aetherd.exe init <pick-a-name> --chain-id aether-testnet-1 --home C:\aether-node
curl.exe -L -o C:\aether-node\config\genesis.json https://raw.githubusercontent.com/whoyoujoshin/aether/main/testnet/genesis.json
(Get-FileHash C:\aether-node\config\genesis.json).Hash.ToLower()   # b6f36f01783ed02a4a353ac825deb07c68a5683cdaca92b879e759c0cb988785
notepad C:\aether-node\config\config.toml
```

Get a recent block to trust for state sync (see step 2):

```powershell
$RPC = "https://rpc.157-245-252-221.sslip.io"
$trust = [int](Invoke-RestMethod "$RPC/block").result.block.header.height - 1000
$hash = (Invoke-RestMethod "$RPC/block?height=$trust").result.block_id.hash
"trust_height = $trust"; "trust_hash = `"$hash`""
```

In Notepad, find these lines and change them to read (then save; Ctrl+F
finds each). `external_address`, `seeds` and `persistent_peers` are under
`[p2p]`, `timeout_commit` under `[consensus]`, and the last four under
`[statesync]`, further down; use the two values PowerShell just printed.
`rpc_servers` names the seed twice because it is the only public RPC for
now (see step 2):

```toml
external_address = "<your public IP>:26656"
seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
persistent_peers = "5ddca5a28f3a9dcd20b21f57c87ed4a2f289d922@64.23.217.145:26656"
timeout_commit = "5s"

[statesync]
enable = true
rpc_servers = "https://rpc.157-245-252-221.sslip.io:443,https://rpc.157-245-252-221.sslip.io:443"
trust_height = <the number printed>
trust_hash = "<the hash printed>"
```

### W3. Run it in the background, at every start-up

Windows has no systemd, so a small script keeps the node running and a
scheduled task starts that script at boot. Make the script:

```powershell
@'
# Keeps the Aether node running; started by the "Aether Node" task.
while ($true) {
  if ((Test-Path C:\aether\node.log) -and (Get-Item C:\aether\node.log).Length -gt 200MB) {
    Move-Item -Force C:\aether\node.log C:\aether\node.log.old
  }
  cmd /c "C:\aether\aetherd.exe start --home C:\aether-node >> C:\aether\node.log 2>&1"
  Start-Sleep -Seconds 5
}
'@ | Set-Content -Encoding ascii C:\aether\run-node.ps1
```

Register the task and start it:

```powershell
$action    = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File C:\aether\run-node.ps1"
$trigger   = New-ScheduledTaskTrigger -AtStartup
$settings  = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType S4U -RunLevel Highest
Register-ScheduledTask -TaskName "Aether Node" -Action $action -Trigger $trigger -Settings $settings -Principal $principal
Start-ScheduledTask -TaskName "Aether Node"
Get-Content C:\aether\node.log -Wait -Tail 20      # Ctrl-C stops watching, not the node
```

To stop the node (before an upgrade, say): `Stop-ScheduledTask -TaskName
"Aether Node"`, then `Get-Process aetherd | Stop-Process`. **Never start
`aetherd.exe` by hand while the task runs it:** two copies of one validator
is double-signing.

### W4. Wait for it to catch up

```powershell
(Invoke-RestMethod http://127.0.0.1:26657/status).result.sync_info | Select-Object latest_block_height, catching_up
```

Done when `catching_up` is `False`, usually a few minutes after the
start. As in step 4, the node first finds and restores a snapshot; the
restore is done when `node.log` shows `Snapshot restored`, then `Time to
switch to consensus reactor!`. If the restore fails, stop the task
(`Stop-ScheduledTask -TaskName "Aether Node"`, then `Get-Process aetherd |
Stop-Process`), open an issue with the end of `node.log`, and don't delete
anything.

### W5. Make your miner's key

The miner key lives in your user folder (`%USERPROFILE%\.aether`), where
`powminer` looks for it, so **no `--home` here**:

```powershell
C:\aether\aetherd.exe keys add miner --keyring-backend file
```

Write the recovery phrase on paper. Then save the passphrase (8 characters
or more) where the miner can read it, readable only by you:

```powershell
$s = Read-Host "Keyring passphrase" -AsSecureString
[IO.File]::WriteAllText("C:\aether\keyring-pass.txt", [Net.NetworkCredential]::new("", $s).Password)
icacls C:\aether\keyring-pass.txt /inheritance:r /grant:r "$($env:USERNAME):(R)" | Out-Null
$MINER = C:\aether\aetherd.exe keys show miner -a --keyring-backend file
$MINER
```

### W6. Open the account with the faucet

```powershell
Invoke-RestMethod -Method Post -Uri https://faucet.157-245-252-221.sslip.io/request -ContentType "application/json" -Body (@{address=$MINER} | ConvertTo-Json)
C:\aether\aetherd.exe q bank balances $MINER
```

### W7. Start mining, at every start-up

Same pattern as the node: a script, and a task that runs it at boot.

```powershell
@"
# Keeps the Aether miner running; started by the "Aether Miner" task.
`$env:USERPROFILE = "$env:USERPROFILE"
`$env:AETHER_KEYRING_PASSPHRASE_FILE = "C:\aether\keyring-pass.txt"
while (`$true) {
  cmd /c "C:\aether\powminer.exe --miner $MINER --from miner --keyring-backend file --chain-id aether-testnet-1 --rpc http://127.0.0.1:26657 --grpc localhost:9090 --auto-submit --loop --fees 0uaeth >> C:\aether\miner.log 2>&1"
  Start-Sleep -Seconds 10
}
"@ | Set-Content -Encoding ascii C:\aether\run-miner.ps1

$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File C:\aether\run-miner.ps1"
Register-ScheduledTask -TaskName "Aether Miner" -Action $action -Trigger $trigger -Settings $settings -Principal $principal
Start-ScheduledTask -TaskName "Aether Miner"
Get-Content C:\aether\miner.log -Wait -Tail 20
```

(`$trigger`, `$settings` and `$principal` are the ones from W3; if you
closed that window, run those three lines again first.) Add `--duty 0.6`
after `--loop` if the PC slows down while mining.

### W8. Register your node's key to your miner

```powershell
C:\aether\validatorkeygen.exe --miner $MINER --home C:\aether-node --chain-id aether-testnet-1 --keyring-backend file --fees 200uaeth
```

Copy the `aetherd tx pow register-validator-pubkey …` line it prints, put
`C:\aether\` in front of `aetherd`, add `--gas 400000` at the end, and run
it. It asks for your keyring passphrase. Then check:

```powershell
C:\aether\aetherd.exe q pow seat-work $MINER
```

**Back up `C:\aether-node\config\priv_validator_key.json`** (encrypted,
off the PC). Never run it on two machines at once.

### W9. Get your seat

As in step 9. To see whether your node is signing:

```powershell
$st = Invoke-RestMethod http://127.0.0.1:26657/status
$st.result.validator_info.voting_power      # above 0 once seated
((Invoke-RestMethod http://127.0.0.1:26657/block).result.block.last_commit.signatures | Where-Object validator_address -eq $st.result.validator_info.address).Count   # 1
```

### W10. Stay in

As in step 10. To upgrade: stop both tasks (`Stop-ScheduledTask`, then
`Get-Process aetherd, powminer | Stop-Process`), replace the `.exe` files
in `C:\aether` with the new release's, and start both tasks again. Check
now and then that `miner.log` still shows finds: a miner that stopped is
the commonest way to lose a seat.

## On a Mac

The same ten steps for a MacBook (or any Mac) on macOS 13 or later, in the
**Terminal** app (Applications → Utilities → Terminal). Read the Linux
steps' explanations above; this section only gives the Mac commands.

A laptop can validate, but only while it stays awake, plugged in and
online, so:

- keep it **plugged in**, with the lid open (a closed lid sleeps it);
- use a cable or 5 GHz Wi-Fi, and **no VPN**;
- keep `~/.aether` where it is: not in Desktop or Documents, which iCloud
  may sync (a synced folder corrupts the chain data);
- mining heats a laptop: start with `--duty 0.5` (step M7).

### M0. Keep the Mac awake, open the port

```bash
sudo pmset -c sleep 0 disksleep 0 displaysleep 10    # never sleep on the charger
```

On a home network, forward **TCP port 26656** on your router to this Mac
(your router's admin page, "port forwarding"), and find your public IP at
<https://ifconfig.me>. If the macOS firewall is on (System Settings →
Network → Firewall), let the node in after step M1:

```bash
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add /usr/local/bin/aetherd
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp /usr/local/bin/aetherd
```

### M1. Download the programs

Pick the file for your Mac: Apple menu → About This Mac → **Chip: Apple M…**
means `aether-macos-arm64.tar.gz`; **Processor: Intel** means
`aether-macos-intel.tar.gz`.

```bash
cd ~
TAG=v0.2.21-testnet                  # or whatever the latest release is called
FILE=aether-macos-arm64.tar.gz       # or aether-macos-intel.tar.gz
curl -LO https://github.com/whoyoujoshin/aether/releases/download/$TAG/$FILE
curl -LO https://github.com/whoyoujoshin/aether/releases/download/$TAG/SHA256SUMS   # from v0.2.22-testnet on
shasum -a 256 -c SHA256SUMS --ignore-missing  # <your file>: OK
mkdir -p aether-bin && tar -xzf $FILE -C aether-bin
sudo mkdir -p /usr/local/bin
sudo install -m 0755 aether-bin/aetherd aether-bin/powminer aether-bin/validatorkeygen aether-bin/minerwatch /usr/local/bin/
which aetherd powminer validatorkeygen     # all three found
```

Downloading with `curl` as above keeps macOS from blocking the programs.
If you downloaded the file in a browser instead and macOS says the program
"cannot be opened", clear the browser's quarantine mark:
`xattr -dr com.apple.quarantine ~/aether-bin`, then install again.

The commands below use `jq`, which macOS 15 includes. On an older macOS,
install it with [Homebrew](https://brew.sh): `brew install jq`.

### M2. Set up the node

```bash
aetherd init <pick-a-name> --chain-id aether-testnet-1
curl -L -o ~/.aether/config/genesis.json \
  https://raw.githubusercontent.com/whoyoujoshin/aether/main/testnet/genesis.json
shasum -a 256 ~/.aether/config/genesis.json   # b6f36f01783ed02a4a353ac825deb07c68a5683cdaca92b879e759c0cb988785
```

Set `seeds` and `external_address` (under `[p2p]`) and `timeout_commit`
(under `[consensus]`), with your public IP in the first line. macOS's
`sed` needs `-i ''`:

```bash
IP=<your public IP>
sed -i '' \
  -e 's#^seeds = .*#seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"#' \
  -e "s#^external_address = .*#external_address = \"$IP:26656\"#" \
  -e 's#^timeout_commit = .*#timeout_commit = "5s"#' \
  ~/.aether/config/config.toml
grep -E '^(seeds|external_address|timeout_commit) ' ~/.aether/config/config.toml   # the three lines, set
```

Then set up state sync (see step 2):

```bash
RPC=https://rpc.157-245-252-221.sslip.io
TRUST=$(( $(curl -s $RPC/block | jq -r .result.block.header.height) - 1000 ))
HASH=$(curl -s "$RPC/block?height=$TRUST" | jq -r .result.block_id.hash)
echo "trusting block $TRUST, hash $HASH"
sed -i '' -e '/^\[statesync\]/,/^\[/{' \
  -e 's/^enable = .*/enable = true/' \
  -e "s#^rpc_servers = .*#rpc_servers = \"$RPC:443,$RPC:443\"#" \
  -e "s/^trust_height = .*/trust_height = $TRUST/" \
  -e "s/^trust_hash = .*/trust_hash = \"$HASH\"/" \
  -e '}' ~/.aether/config/config.toml
sed -i '' 's#^persistent_peers = .*#persistent_peers = "5ddca5a28f3a9dcd20b21f57c87ed4a2f289d922@64.23.217.145:26656"#' ~/.aether/config/config.toml
```

### M3. Run it in the background, at every start-up

macOS starts background programs with `launchd`. This file tells it to
start the node at boot, as you, and to start it again whenever it stops:

```bash
sudo tee /Library/LaunchDaemons/io.aether.node.plist >/dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>io.aether.node</string>
  <key>UserName</key><string>$USER</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/bin/aetherd</string><string>start</string><string>--home</string><string>$HOME/.aether</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>HOME</key><string>$HOME</string>
    <key>GOMEMLIMIT</key><string>4000MiB</string>
  </dict>
  <key>SoftResourceLimits</key><dict><key>NumberOfFiles</key><integer>65535</integer></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>$HOME/aether-node.log</string>
  <key>StandardErrorPath</key><string>$HOME/aether-node.log</string>
</dict></plist>
EOF
sudo chown root:wheel /Library/LaunchDaemons/io.aether.node.plist
sudo chmod 644 /Library/LaunchDaemons/io.aether.node.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/io.aether.node.plist
tail -f ~/aether-node.log            # Ctrl-C stops watching, not the node
```

Keep the log from filling the disk (macOS trims it at 200 MB, keeping
three old copies):

```bash
echo "$HOME/aether-node.log $USER:staff 644 3 200000 * J" | sudo tee /etc/newsyslog.d/aether.conf
```

To stop the node (before an upgrade, say): `sudo launchctl bootout
system/io.aether.node`; to start it again, the `bootstrap` line above.
**Never run `aetherd start` by hand while launchd runs it:** two copies of
one validator is double-signing.

### M4. Wait for it to catch up

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
```

Done when `catching_up` is `false`, usually a few minutes after the
start. As in step 4, the node first finds and restores a snapshot; the
restore is done when the log shows `Snapshot restored`, then `Time to
switch to consensus reactor!`. If the restore fails, stop the node (`sudo
launchctl bootout system/io.aether.node`), open an issue with the end of
`~/aether-node.log`, and don't delete anything.

### M5. Make your miner's key

```bash
aetherd keys add miner --keyring-backend file
```

Write the recovery phrase on paper. Then save the passphrase (8 characters
or more) where the miner can read it, readable only by you:

```bash
printf "Keyring passphrase: "; read -rs P; echo
printf '%s' "$P" > ~/.aether/keyring-pass && chmod 600 ~/.aether/keyring-pass; unset P
MINER=$(aetherd keys show miner -a --keyring-backend file)
echo $MINER
```

### M6. Open the account with the faucet

```bash
curl -s -X POST https://faucet.157-245-252-221.sslip.io/request \
  -H 'Content-Type: application/json' -d "{\"address\":\"$MINER\"}"
aetherd q bank balances $MINER
```

### M7. Start mining, at every start-up

```bash
sudo tee /Library/LaunchDaemons/io.aether.miner.plist >/dev/null <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>io.aether.miner</string>
  <key>UserName</key><string>$USER</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/bin/powminer</string>
    <string>--miner</string><string>$MINER</string>
    <string>--from</string><string>miner</string>
    <string>--keyring-backend</string><string>file</string>
    <string>--chain-id</string><string>aether-testnet-1</string>
    <string>--rpc</string><string>http://127.0.0.1:26657</string>
    <string>--grpc</string><string>localhost:9090</string>
    <string>--auto-submit</string><string>--loop</string>
    <string>--fees</string><string>0uaeth</string>
    <string>--duty</string><string>0.5</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>HOME</key><string>$HOME</string>
    <key>AETHER_KEYRING_PASSPHRASE_FILE</key><string>$HOME/.aether/keyring-pass</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>$HOME/aether-miner.log</string>
  <key>StandardErrorPath</key><string>$HOME/aether-miner.log</string>
</dict></plist>
EOF
sudo chown root:wheel /Library/LaunchDaemons/io.aether.miner.plist
sudo chmod 644 /Library/LaunchDaemons/io.aether.miner.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/io.aether.miner.plist
echo "$HOME/aether-miner.log $USER:staff 644 3 50000 * J" | sudo tee -a /etc/newsyslog.d/aether.conf
tail -f ~/aether-miner.log
```

`--duty 0.5` hashes half the time, so the laptop stays cooler. Raise it
(up to `1`) on a desktop Mac.

### M8. Register your node's key to your miner

```bash
validatorkeygen --miner $MINER --home ~/.aether \
  --chain-id aether-testnet-1 --keyring-backend file --fees 200uaeth
```

Copy the `aetherd tx pow register-validator-pubkey …` line it prints, add
`--gas 400000` at the end, and run it. It asks for your keyring
passphrase. Then check:

```bash
aetherd q pow seat-work $MINER
```

**Back up `~/.aether/config/priv_validator_key.json`** (encrypted, off the
Mac). Never run it on two machines at once.

### M9. Get your seat

As in step 9; the same `curl … | jq` checks work on a Mac.

### M10. Stay in

As in step 10. To upgrade: `sudo launchctl bootout system/io.aether.miner`
and `sudo launchctl bootout system/io.aether.node`, install the new
programs as in M1, then `bootstrap` the node and the miner again (M3, M7).
Check now and then that `~/aether-miner.log` still shows finds.

## Quick reference

| What | Where |
|---|---|
| Chain ID | `aether-testnet-1` |
| Seed | `dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656` |
| Genesis | [`testnet/genesis.json`](../testnet/genesis.json) |
| Explorer | <https://explorer.157-245-252-221.sslip.io> |
| Faucet | `POST https://faucet.157-245-252-221.sslip.io/request` |
| Public RPC | <https://rpc.157-245-252-221.sslip.io> |
| Security reports | GitHub → Security → Report a vulnerability ([SECURITY.md](../SECURITY.md)) |
