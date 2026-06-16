# Aether (AETH)

**The Quantum-Resistant, Fair-Launched, Day-to-Day Cryptocurrency**

Aether is a next-generation Layer 1 blockchain designed for real-world usability, sustainable security, and future-proof cryptography.

## Vision
- Fixed 60-second blocks for fast confirmations
- Hybrid Scrypt PoW + AuxPoW (merged mining with LTC/DOGE)
- Perpetual low tail emission (~0.75% long-term)
- Transparent on-chain treasury
- Native IBC interoperability
- Optional privacy (shielded transactions)
- Post-quantum signatures

## Quick Start

### Local Testnet
```bash
# Clone repo
git clone https://github.com/whoyoujoshin/aether.git
cd aether

# Build
make install

# Initialize node
aetherd init mynode --chain-id aether-testnet-1

# Copy genesis (once available)
cp genesis.json ~/.aether/config/genesis.json

# Start node
aetherd start
```

### Mining (Testnet)
See `docs/mining.md` for instructions (Scrypt + AuxPoW support).

## Parameters (Locked)

See full [Design Document](Aether_DesignDocument.md) for details.

- Block time: 60 seconds
- Long-term inflation: ~0.75%
- Initial reward: 5 AETH/block (declining over 8 years)
- Tail reward: 0.18 AETH/block
- Treasury: 15% issuance + 25% fees

## Tech Stack
- Cosmos SDK (Go)
- Custom Scrypt PoW consensus
- IBC enabled
- Post-quantum signatures

## Roadmap
See full roadmap in the Design Document.

## Contributing
Contributions welcome! See `CONTRIBUTING.md`

## License
MIT
