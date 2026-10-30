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
1 uaeth directory listings.

1. Joshua recovers it on his workstation into an encrypted keyring from the
   mnemonic in `created.json` (`wallet import services --keyring-backend
   file --keyring-dir <dir>`, which asks for the mnemonic and a new
   passphrase), and checks the address is
   `aether1e6r6g8mat6aqax9l29wwmrfhp52hjzrpwevqqq9tcj0lm8egdhjs9q44xl`.
2. On the seed: `$P/aetherd keys delete services --keyring-backend test
   --keyring-dir /root/aether-services-key -y`, then `shred -u
   /root/aether-services-key/created.json`. Keep the `address` file if
   there is one, since scripts read it.
3. Listing a service from then on is `paywall list --url <service url>
   --key services --keyring-backend file --keyring-dir <dir>`, run from
   the workstation (`docs/SELL.md`). `announce.sh` on the seed can no
   longer list.

## 6. Relayer keys (sync3, DardenPC)

`outbound` reads a file keyring's passphrase from
`AETHER_KEYRING_PASSPHRASE_FILE` too (`--keyring-backend file`). These are
secp256k1 / eth_secp256k1 keys, so the chains' own CLIs move them.

- **sync3**: export the Osmosis relayer key and import it into a file
  keyring with `osmosisd keys export` and `osmosisd keys import`, typing
  the passphrases. Make a credential the same way, and put the same two
  lines in a drop-in for `aether-outbound-osmosis`.
- **DardenPC**: import the Injective relayer key (`inj1c2rd…`) into a file
  keyring under `C:\aether-data` with `injectived keys import`. Put the
  passphrase in a file only SYSTEM and Administrators can read
  (`icacls <file> /inheritance:r /grant:r SYSTEM:R Administrators:R`).
  Set `AETHER_KEYRING_PASSPHRASE_FILE` for the task that runs
  `outbound.exe`, and add `--keyring-backend file`. Windows has no
  credential store like systemd's, so this one protects copies of the
  keyring but not of the passphrase file. Keep its balance small.

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
