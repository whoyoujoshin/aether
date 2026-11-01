# Service keys off the unencrypted keyring

**For:** Gitty (Joshua keeps the passphrases and mnemonics). **Linear:**
AET-26. **Why:** every key the seed's services use sits in `keyring-backend
test`, which stores private keys unencrypted on disk, on a validator. A copy
of `/root` (a backup, a snapshot, a data copy for a new node) carries them
all. `docs/MAINNET.md` §3: mainnet money needs an encrypted keyring on a
machine that is not a validator.

This runbook does the first half on the testnet now: keys that only
receive go off the server, and keys that must sign unattended move into an
encrypted keyring. The second half, moving the signing services to a
server that isn't a validator, comes with the query node
(`docs/QUERY-NODE.md`): the same steps apply there.

## What it protects, and what it doesn't

- **Protects:** copies of the disk. The keyring files are encrypted with a
  passphrase. The passphrase is a systemd encrypted credential under
  `/etc/credstore.encrypted/`, sealed to this server's own credential key
  (`/var/lib/systemd/credential.secret`, or the TPM if there is one).
  systemd decrypts it only into the running service's private credentials
  directory. A copied keyring directory, or a copied credential file, is
  useless on another machine.
- **Doesn't protect:** root on the running server. Root can decrypt the
  credential or read a running service's memory. The defence there is
  small balances in hot keys (top them up, don't hoard), and keys that
  only receive not being on the server at all.

## The keys

| Key | Where now | Signs unattended? | Goes to |
|---|---|---|---|
| `faucet-v3` (faucet) | `/root/.aether`, test | yes | `/root/.aether` file keyring, credential `aether-home-keyring` |
| `faucet-v2` (powminer; the seed's consensus key is registered to it) | `/root/.aether`, test | yes | the same file keyring and credential |
| `sandbox-funder` (test wallet) | `/root/aether-sandbox-key`, test | yes | its file keyring, credential `aether-sandbox-keyring` (`sandbox.sh` with `KEYRING_BACKEND=file`) |
| `services` (the six services' payee) | `/root/aether-services-key`, test, mnemonic in `created.json` | no: paywalls need only `--pay-to`; it signs only the directory listings | off the server: a workstation's encrypted keyring |
| `decoy` (benchmark decoys' payee) | `/root/aether-decoy-key`, test | no (`SWEEP=yes` signs) | stays: testnet benchmark only, a few AETH |
| relayer keys: Osmosis on sync3, Injective on DardenPC (`outbound`) | each chain's own keyring, test | yes | a file keyring there (step 6) |

Helicase relays unsigned, so it has no key.

The `aetherd` CLI's `keys export`/`keys import` can't move ML-DSA keys: its
armor decodes private keys with the legacy amino codec, which doesn't know
them. `wallet copy-key` copies the key's 32-byte seed straight into the
other keyring instead, and checks the address is unchanged. The `wallet`
binary is in `scripts/prebuild.sh`'s output.

## 0. Before you start

- Build `main` on a workstation (`scripts/prebuild.sh`) and copy
  `dist/prebuilt-<commit>` to the seed. It carries the `wallet`, and the
  faucet, agentmcp and powminer that read a file keyring's passphrase from
  `AETHER_KEYRING_PASSPHRASE_FILE`.
- `systemd --version` must be 250 or later (Ubuntu 24.04 has 255).
- Joshua picks the passphrases: two, at least 12 characters, one per
  credential. They go in his password manager before anything else.

## 1. The credentials

On the seed, once per credential. Type the passphrase; it never touches the
disk in the clear:

```bash
install -d -m 700 /etc/credstore.encrypted
read -rs PASS && printf '%s' "$PASS" | systemd-creds encrypt --name=keyring - /etc/credstore.encrypted/aether-home-keyring; unset PASS
read -rs PASS && printf '%s' "$PASS" | systemd-creds encrypt --name=keyring - /etc/credstore.encrypted/aether-sandbox-keyring; unset PASS
systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-home-keyring - | wc -c   # the passphrase's length: it decrypts
```

`--name=keyring` must match the `keyring` in the units' `LoadCredentialEncrypted=keyring:…`.

## 2. Copy the keys into the encrypted keyrings

`P=/root/prebuilt-<commit>`. The passphrase goes in a file in `/run`
(memory, not disk) only for the copy:

```bash
umask 077
systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-home-keyring /run/aether-pass
AETHER_KEYRING_PASSPHRASE_FILE=/run/aether-pass $P/wallet copy-key faucet-v3 faucet-v2 \
  --keyring-backend test --keyring-dir /root/.aether --to-dir /root/.aether
rm -f /run/aether-pass

systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-sandbox-keyring /run/aether-pass
AETHER_KEYRING_PASSPHRASE_FILE=/run/aether-pass $P/wallet copy-key sandbox-funder \
  --keyring-backend test --keyring-dir /root/aether-sandbox-key --to-dir /root/aether-sandbox-key
rm -f /run/aether-pass
```

Each line prints the key's address. Check it against the known one
(`faucet-v3` `aether1l8ws…ppsf`, `faucet-v2` `aether13cfj…`, the funder
`aether16dh7…g88v`). The file keyring sits next to the test one, in
`keyring-file/` of the same directory, so every default path (the faucet's
drip log and agent registry, the powminer's home) stays as it is.

## 3. Switch the services, one at a time

**Faucet.** Add a drop-in, `/etc/systemd/system/aether-faucet.service.d/keyring.conf`:

```ini
[Service]
LoadCredentialEncrypted=keyring:/etc/credstore.encrypted/aether-home-keyring
Environment=AETHER_KEYRING_PASSPHRASE_FILE=%d/keyring
```

In `gas-price.conf`, which holds the faucet's start command, add
`--keyring-backend file` to the `ExecStart=` line (keep `--keyring-dir` as
it is). Back up both files first (`.bak-filekeyring`). Install the new
faucet binary from `$P` the way the last faucet deploy did, then run
`systemctl daemon-reload && systemctl restart aether-faucet`.
`journalctl -u aether-faucet -n 5` shows `listening … from aether1l8ws…`.
Then fund a fresh address through the public faucet page.

**Powminer** (the seed's seat depends on it): the same drop-in on
`aether-powminer`, and `--keyring-backend file` on its `ExecStart`. Do it
right after a boundary, while all four validators sign. Then check
`journalctl -u aether-powminer` shows accepted submissions. A powminer
that can't open its key mines nothing; the seat keeps its power until the
window's work falls, so there is time to put it back.

**Test wallet:**

```bash
cd /root/aether-src && KEYRING_BACKEND=file PREBUILT=$P bash scripts/agentservices/sandbox.sh
```

The unit gains the credential lines and `--keyring-backend file`. Then
create a test wallet (`docs/TEST-WALLET.md`): its grant is a transfer from
the funder, so it proves the funder signs.

## 4. Remove the unencrypted copies

Only after each service in step 3 has signed from the file keyring. Four
parts, in order; each ends with a check, and the next waits for it.

`faucet-v3` and `faucet-v2` have no mnemonic file, so once their test
copies are gone the seed's `keyring-file/` is their only copy, and
`faucet-v2` holds the seed's seat. Part A makes a second copy first. It
is encrypted with the passphrase, so it can sit on a workstation.

**A. Back up the encrypted keyrings (Gitty, on the seed).**

```bash
P=/root/prebuilt-<commit>; B=/root/keyrings-file-$(date +%Y%m%d).tgz
umask 077
tar -C /root -czf $B .aether/keyring-file aether-sandbox-key/keyring-file
# Check the archive opens with the passphrases, from a copy in /run (memory):
mkdir -p /run/kcheck && tar -C /run/kcheck -xzf $B
systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-home-keyring /run/aether-pass
AETHER_KEYRING_PASSPHRASE_FILE=/run/aether-pass $P/wallet list --keyring-backend file --keyring-dir /run/kcheck/.aether
systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-sandbox-keyring /run/aether-pass
AETHER_KEYRING_PASSPHRASE_FILE=/run/aether-pass $P/wallet list --keyring-backend file --keyring-dir /run/kcheck/aether-sandbox-key
rm -rf /run/aether-pass /run/kcheck
sha256sum $B
```

The first list shows `faucet-v3` and `faucet-v2`, the second
`sandbox-funder`, at their known addresses. Send Joshua the archive (any
way: it is encrypted) and its sha256.

**B. Joshua keeps the archive and the mnemonics.**

1. Save the archive in two places, e.g. a folder on his PC and a USB stick
   or a password-manager attachment. `Get-FileHash <file>` in PowerShell
   matches Gitty's sha256.
2. In the DigitalOcean console on the seed, show each mnemonic, copy it
   into its own password-manager entry, then clear the screen:
   ```bash
   cat /root/aether-sandbox-key/funder-mnemonic.txt
   cat /root/aether-services-key/created.json     # the "mnemonic" field; needed again in step 5
   cat /root/aether-decoy-key/decoy-mnemonic.txt  # optional: the benchmark decoy
   clear
   ```
3. Tell Gitty: archive saved (hash matches), mnemonics saved.

**C. Delete the test-keyring copies (Gitty, after Joshua's OK).**

```bash
for k in faucet-v3 faucet-v2; do $P/aetherd keys delete $k --keyring-backend test --keyring-dir /root/.aether -y; done
$P/aetherd keys delete sandbox-funder --keyring-backend test --keyring-dir /root/aether-sandbox-key -y
shred -u /root/aether-sandbox-key/funder-mnemonic.txt
shred -u /root/aether-decoy-key/decoy-mnemonic.txt   # only if Joshua saved it
shred -u $B
```

`created.json` stays until step 5. Other keys in `/root/.aether`'s test
keyring (`ls /root/.aether/keyring-test`) are listed for Joshua to
decide, not deleted here.

**D. Check (Gitty).** The services still sign from the file keyring: one
faucet drip, one new test wallet (its grant is a funder transfer), and
`journalctl -u aether-powminer -n 20` shows accepted submissions. Then
remove the `.bak-filekeyring` backups.

## 5. The payee key leaves the server

The paywalls only need `--pay-to` (the address). The key signs only the
1 uaeth directory listings. It moves to Joshua's PC as an encrypted
keyring with its own passphrase; nothing restarts.

**A. Joshua: a third passphrase**, `aether-services-payee`, in his password
manager (20+ characters, letters, digits and dashes).

**B. Joshua, in the DigitalOcean console on the seed** (after `bind 'set
enable-bracketed-paste off'`). `copy-key` asks for the new passphrase
twice, hidden: paste it each time.

```bash
P=/root/prebuilt-<commit>
umask 077
$P/wallet copy-key services --keyring-backend test --keyring-dir /root/aether-services-key --to-dir /root/payee-out
tar -C /root -czf /root/payee-keyring.tgz payee-out
sha256sum /root/payee-keyring.tgz
```

It prints `services aether1e6r6g8mat6aqax9l29wwmrfhp52hjzrpwevqqq9tcj0lm8egdhjs9q44xl
copied to the file keyring`; any other address, stop.

**C. Gitty sends Joshua `/root/payee-keyring.tgz`** (encrypted, like step
4's archive). Joshua unpacks it to e.g. `C:\aether-keys\payee-out`, checks
`Get-FileHash` against the sha256, and lists it with the release
`wallet.exe` (v0.2.16-testnet or later), typing the passphrase:

```powershell
.\wallet.exe list --keyring-backend file --keyring-dir C:\aether-keys\payee-out
```

It shows `services` at `aether1e6r6g8…q44xl`. Save the archive in a second
place too; the mnemonic from step 4 is the other way back.

**D. Gitty removes it from the seed, after Joshua's OK:**

```bash
$P/aetherd keys delete services --keyring-backend test --keyring-dir /root/aether-services-key -y
shred -u /root/aether-services-key/created.json /root/payee-keyring.tgz
rm -rf /root/payee-out
```

Keep the `address` file if there is one, since scripts read it. Check
one paywall still answers an unpaid request with its 402 naming the
payee.

From then on a service is listed from Joshua's PC: `paywall list --url
<service url> --key services --keyring-backend file --keyring-dir
C:\aether-keys\payee-out` (`docs/SELL.md`). `announce.sh` on the seed can
no longer list.

(Recovering from the mnemonic instead, `wallet import services
--keyring-backend file --keyring-dir <dir>`, needs a `wallet` built after
the fix to its mnemonic prompt: earlier builds read only the first word.)

## 6. Relayer keys (sync3, DardenPC)

Two keys sign relay transactions on the other chains: the Osmosis relayer
key for `aether-outbound-osmosis` on sync3, and the Injective relayer key
(`inj1c2rd…`) for `outbound.exe` on DardenPC. Both are in test keyrings.
`outbound -copy-key-to <dir>` copies its `-cparty-key` into an encrypted
file keyring in `<dir>` (next to the test one when `<dir>` is the
`-cparty-home`), checks the address is unchanged, and exits. It handles
secp256k1 and Injective's eth_secp256k1. `outbound` then runs with
`-keyring-backend file` and reads the passphrase from
`AETHER_KEYRING_PASSPHRASE_FILE`.

**0. Before you start.**
- Gitty builds from `main` on the workstation: `scripts/prebuild.sh` now
  has `outbound` for sync3, plus `GOOS=windows GOARCH=amd64 CGO_ENABLED=0
  go build -o outbound.exe ./cmd/outbound` for DardenPC. Note both sha256s.
- Joshua makes two more passphrases in his password manager:
  `aether-osmosis-relayer` and `aether-injective-relayer`.
- Each relayer restart is just the relayer, not a validator. Packets
  wait while it's down; Injective's client of Aether trusts about 65 h.

**A. sync3 (Joshua types the passphrase, Gitty does the rest).**

1. Joshua, in the DigitalOcean console on sync3 (`bind 'set
   enable-bracketed-paste off'` first), one line at a time:
   ```bash
   install -d -m 700 /etc/credstore.encrypted
   read -rsp 'OSMOSIS relayer passphrase: ' PASS && printf '%s' "$PASS" | systemd-creds encrypt --name=keyring - /etc/credstore.encrypted/aether-osmosis-relayer; unset PASS; echo
   systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-osmosis-relayer - | wc -c   # the passphrase's length
   ```
2. Gitty copies the key and checks the printed `osmo1…` address is the
   relayer's:
   ```bash
   O=/root/aether-outbound-<commit>     # the new build, copied over; sha256 checked
   umask 077
   systemd-creds decrypt --name=keyring /etc/credstore.encrypted/aether-osmosis-relayer /run/aether-pass
   AETHER_KEYRING_PASSPHRASE_FILE=/run/aether-pass $O -copy-key-to /root/.osmosisd-relayer \
     -cparty-home /root/.osmosisd-relayer -cparty-key relayer -cparty-bech32-prefix osmo
   rm -f /run/aether-pass
   ```
3. Switch the unit. Back up the unit (`.bak-filekeyring`), then add
   `/etc/systemd/system/aether-outbound-osmosis.service.d/keyring.conf`:
   ```ini
   [Service]
   LoadCredentialEncrypted=keyring:/etc/credstore.encrypted/aether-osmosis-relayer
   Environment=AETHER_KEYRING_PASSPHRASE_FILE=%d/keyring
   ```
   In the unit's `ExecStart`, point at `$O` and change `--keyring-backend
   test` to `file`. Then `systemctl daemon-reload && systemctl restart
   aether-outbound-osmosis`. `journalctl -u aether-outbound-osmosis -n 5`
   shows `relaying aether -> … over 07-tendermint-5287 as osmo1…`, and its
   next client update (within `-refresh-after`) lands on Osmosis.
   ibcwatch on sync3 shows `07-tendermint-5287` refreshed.

**B. DardenPC (Joshua, elevated PowerShell).**

1. Put the new `outbound.exe` in place, keeping the old one:
   ```powershell
   Get-FileHash $HOME\Downloads\outbound.exe        # Gitty's sha256
   Copy-Item C:\aether-bin\outbound.exe C:\aether-bin\outbound.exe.pre-filekeyring
   ```
   (the copy over it comes in step 4, once the old one has stopped)
2. The passphrase file, readable only by SYSTEM and Administrators:
   ```powershell
   New-Item -ItemType Directory -Force C:\aether-keys | Out-Null
   $s = Read-Host -AsSecureString 'INJECTIVE relayer passphrase'
   [System.Net.NetworkCredential]::new('', $s).Password | Set-Content -NoNewline -Encoding ascii C:\aether-keys\injective-relayer-pass.tmp
   icacls C:\aether-keys\injective-relayer-pass.tmp /inheritance:r /grant:r SYSTEM:R Administrators:R
   ```
   It is `.tmp` until step 4: the task script switches to the file keyring
   as soon as `injective-relayer-pass.txt` exists.
3. Copy the key; the printed address must be `inj1c2rd0g6e9pxfulr3jpy2lvme6a37sqnfk35wlg`:
   ```powershell
   '' | & $HOME\Downloads\outbound.exe -copy-key-to C:\aether-relayer\injective -cparty-home C:\aether-relayer\injective -cparty-key relayer -cparty-bech32-prefix inj -keyring-passphrase-file C:\aether-keys\injective-relayer-pass.tmp
   ```
   The `'' |` matters in an interactive PowerShell: its console counts as a
   terminal, and the SDK would otherwise prompt there instead of using the
   file.

   **On Windows, pass `-keyring-passphrase-file` rather than relying on
   `AETHER_KEYRING_PASSPHRASE_FILE`.** Under the scheduled task, PowerShell
   5.1's `Start-Process` gave the child neither the variable nor a usable
   stdin (`os.Stdin` was nil), so the keyring failed with `invalid argument`
   x3 and `too many failed passphrase attempts`. The flag sets the variable
   inside outbound itself; `scripts/windows/outbound-injective-ensure.ps1`
   passes it with the file backend. Linux/systemd keeps using the variable.
4. Switch over. Copy `scripts/windows/outbound-injective-ensure.ps1` from
   `main` over `C:\aether-data\outbound-injective-ensure.ps1` (back the old
   one up as `.pre-filekeyring`). It uses the file keyring once the
   passphrase file exists. Then:
   ```powershell
   Get-NetTCPConnection -LocalPort 8096 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }
   Copy-Item $HOME\Downloads\outbound.exe C:\aether-bin\outbound.exe -Force
   Rename-Item C:\aether-keys\injective-relayer-pass.tmp injective-relayer-pass.txt
   Start-ScheduledTask 'Aether Outbound Injective'
   Get-Content C:\aether-data\outbound-injective-ensure.log -Tail 2      # "starting: … (file keyring)"
   Get-Content C:\aether-data\outbound-injective.log -Tail 5             # "relaying aether -> injective-888 … as inj1c2rd…"
   ```
   Within the hour (`-refresh-after 1h`) ibcwatch shows `07-tendermint-510`
   refreshed. To go back: rename the `.txt` to `.tmp`, stop it, start the
   task; it runs on the test keyring again.

**C. Back up and remove the test copies (after A and B have relayed).**
Tar each `keyring-file` directory (`/root/.osmosisd-relayer/keyring-file`
on sync3; `C:\aether-relayer\injective\keyring-file` on DardenPC) to
Joshua's backups, as in step 4. Then, with Joshua's OK, delete the
test-keyring copies with any SDK CLI that reads the key type, e.g. on
sync3 `/root/osmosisd keys delete relayer --keyring-backend test --home
/root/.osmosisd-relayer -y`, and on DardenPC
`C:\aether-bin\counterpartyd.exe keys delete relayer --keyring-backend
test --home C:\aether-relayer\injective -y`. `relayer.exe`, used for
handshakes, then needs `-keyring-backend file` too.

Windows has no credential store like systemd's: this protects copies of
the keyring, not of the passphrase file next to it. Keep the Injective
relayer's balance small.

## If something goes wrong

- **A service won't start (`incorrect passphrase`, or `too many failed
  passphrase attempts`):** the credential's passphrase isn't the one the
  keyring was made with. Put the unit's backups back (`.bak-filekeyring`)
  and restart: the test keyring copy is still there until step 4.
- **`AETHER_KEYRING_PASSPHRASE_FILE … readable by others`:** the service
  isn't getting the credential (no `LoadCredentialEncrypted` line, or
  `%d` unexpanded). Check `systemctl show -p Environment <unit>`.
- **The seed's credential key is lost (server rebuilt):** the encrypted
  credentials can't be decrypted elsewhere, by design. Make new ones from
  the passphrases in Joshua's password manager. The keyring directories
  restore from a backup as they are.
