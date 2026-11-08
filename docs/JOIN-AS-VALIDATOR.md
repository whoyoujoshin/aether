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
(**`v0.2.20-testnet` or later**: earlier releases don't carry the testnet's
rules from block 712,080), download **`aether-linux-amd64.tar.gz`** and
unpack it:

```bash
cd ~
TAG=v0.2.20-testnet          # or whatever the latest release is called
curl -LO https://github.com/whoyoujoshin/aether/releases/download/$TAG/aether-linux-amd64.tar.gz
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
```

Edit `~/.aether/config/config.toml`:

```toml
seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
external_address = "<your server's public IP>:26656"
timeout_commit = "5s"
```

**`timeout_commit` must be `"5s"`** on every validator. One validator with a
longer value makes every block wait for it.

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

The node downloads and checks every block since the start of the testnet.
That takes hours to a day or two. Check progress with:

```bash
curl -s localhost:26657/status | jq '.result.sync_info | {latest_block_height, catching_up}'
```

Compare `latest_block_height` with the explorer
(<https://explorer.157-245-252-221.sslip.io>). You're caught up when
`catching_up` is `false`.

**A few stops on the way are normal.** At blocks 109,000, 122,000, 161,000
and 511,920 the testnet added new parts, and a node replaying the history
stops there with a message that ends `restart this node`. If the process
exited, systemd starts it again by itself; if the log stops moving instead,
run `sudo systemctl restart aetherd`. Either way it carries on from the same
block.

If it stops with `wrong Block.Header.AppHash` or anything else, that's a
bug worth reporting ([BUG-HUNT.md](BUG-HUNT.md)): open an issue with the
block number and the last 50 log lines.

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
$TAG = "v0.2.20-testnet"     # or whatever the latest release is called
curl.exe -LO "https://github.com/whoyoujoshin/aether/releases/download/$TAG/aether-windows-amd64.tar.gz"
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
notepad C:\aether-node\config\config.toml
```

In Notepad, find these lines and change them to read (then save):

```toml
external_address = "<your public IP>:26656"
seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
timeout_commit = "5s"
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

Done when `catching_up` is `False`. As in step 4, the node stops at
109,000, 122,000, 161,000 and 511,920 with `restart this node`. The script
starts it again by itself when it exits; if `node.log` stops moving
instead, run `Get-Process aetherd | Stop-Process` and the script restarts
it within seconds.

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
TAG=v0.2.20-testnet                  # or whatever the latest release is called
FILE=aether-macos-arm64.tar.gz       # or aether-macos-intel.tar.gz
curl -LO https://github.com/whoyoujoshin/aether/releases/download/$TAG/$FILE
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
open -e ~/.aether/config/config.toml
```

In TextEdit, find these lines and change them to read (then save):

```toml
external_address = "<your public IP>:26656"
seeds = "dfa6aae4b7bfd5b0eb1e22fabbae3e83a475b938@157.245.252.221:26656"
timeout_commit = "5s"
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

Done when `catching_up` is `false`. As in step 4, the node stops at
109,000, 122,000, 161,000 and 511,920 with `restart this node`; launchd
starts it again when it exits. If the log stops moving instead, run
`sudo launchctl kickstart -k system/io.aether.node`.

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
