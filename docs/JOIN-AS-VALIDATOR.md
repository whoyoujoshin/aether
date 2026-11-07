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
