# Privacy policy

Effective 2026-10-08.

This covers the Aether wallet for AI agents (`agentmcp`, including the
`aether-wallet.mcpb` bundle for Claude Desktop), the desktop wallet, and
the public testnet services this project runs: the explorer, the faucet,
the public read-only MCP endpoint and the public RPC and gRPC nodes.

In short: there are no accounts and no analytics, keys stay on your
machine, and nothing is sold or shared. What you put on the blockchain is
public and permanent.

## The wallet on your machine

- **Keys stay with you.** The agent's key and recovery phrase are kept in
  its keyring folder on your machine (`~/.aether-agent` by default). They
  are never sent to us or anyone else. On first run the recovery phrase is
  also printed once to the wallet's log, which your MCP client may keep on
  your machine.
- **Local records.** Spending totals, purchase receipts, prepaid balances
  and approval requests are kept in that same folder, on your machine.
- **What leaves your machine:**
  - transactions and queries, to the node you configure (by default the
    public testnet node this project runs);
  - your agent's address, to the faucet, when you ask it for test funds;
  - requests and payments, to the paid services your agent chooses to
    call (each service's own policy applies to what it receives);
  - alerts, to a webhook, only if you set `--notify-webhook`.
- **No telemetry.** The wallet sends no analytics, crash reports or usage
  data.

The desktop wallet works the same way: keys and records stay on your
machine. It checks this repository's GitHub releases for updates, so
GitHub sees that request.

## Public read-only MCP endpoint

It holds no key and cannot send, sign or spend. It answers lookups of
addresses, transaction hashes and the on-chain service directory, and
doesn't keep a record of what you looked up. The web server in front of it
may log your IP address, the time and the request path, as web servers do.

## Faucet

- Each payment it makes is recorded: the address funded, the amount, the
  transaction hash, the time, how the request arrived (browser, agent key
  or API), and the agent key's ID and name if one was used. These records
  are published at the faucet's `/drips` and `/stats`.
- It uses your IP address to enforce its per-caller limits. The address is
  held in memory for that, and written to the faucet's server log next to
  the address it funded.

## Explorer

No accounts, no cookies, no analytics. Your browser keeps display
preferences (such as which chain the helix shows) in its local storage, on
your device. The web server may log your IP address, the time and the
request path.

## The blockchain

Everything sent on chain is public and permanent: addresses, amounts,
memos, service listings and ratings. No one, including this project, can
delete it. Don't put personal information in memos or listings.

## Server logs

Server logs are kept only to run and protect these services, and are
removed as the servers' system logs rotate. They are not sold, shared, or
used for advertising or profiling.

## Changes and contact

Changes to this policy are commits to this file. Questions: open an issue
at https://github.com/whoyoujoshin/aether/issues.
