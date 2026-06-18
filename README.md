# Aether (AETH) - Local Testnet

Aether is a Scrypt-based, fair-launched Layer 1 blockchain with 60-second blocks, AuxPoW merged mining, and sustainable tokenomics.

## Current Features
- 60s block time
- Basic transaction sending
- Query balance and supply
- Mining simulation
- Local testnet node

## Quick Start

```powershell
# Build
go build ./cmd/aetherd

# Initialize node
.\aetherd.exe init

# Start the testnet (in one window)
.\aetherd.exe start

# In another window - send test transaction
.\aetherd.exe tx send aether1sender aether1receiver 100

# Check balance
.\aetherd.exe query balance aether1receiver

# Mining simulation
.\aetherd.exe mine