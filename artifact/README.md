\# Aether (AETH)



\*\*Aether\*\* is a next-generation Layer 1 blockchain designed for real-world usage: fast, secure, quantum-resistant, and fair-launched.



\### Key Features (Locked)

\- \*\*Block Time\*\*: 60 seconds

\- \*\*Consensus\*\*: Scrypt PoW + AuxPoW (merged mining with LTC/DOGE)

\- \*\*Tokenomics\*\*: Tail emission (\~0.75% long-term inflation), 15% treasury from issuance

\- \*\*Privacy\*\*: Optional shielded transactions

\- \*\*Interoperability\*\*: Cosmos SDK + IBC ready

\- \*\*Fair Launch\*\*: No pre-mine, no VC allocation



\---



\### Quick Start (Local Development)



```powershell

\\# 1. Clone the repo

git clone https://github.com/whoyoujoshin/aether.git

cd aether



\\# 2. Build

go build ./cmd/aetherd



\\# 3. Initialize node

.\\\\aetherd.exe init testnode --chain-id aether-test-1



\\# 4. Start local testnet

.\\\\aetherd.exe start


