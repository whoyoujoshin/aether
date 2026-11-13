package pow

// A consensus key belongs for good to the first miner that registered it.
//
// The key → miner index (GetMinerByConsensusAddr) is what ProcessMisbehavior
// reads to decide whom CometBFT's evidence is against, and it reads it when
// the evidence lands, not as it was when the key signed. Before
// ConsensusKeyOwnershipHeight a key was refused only while another miner
// still held it, so a validator that double-signed with key K could move
// to a new key, register K to a throwaway miner account of its own (it
// holds K's private key, so it can sign the proof of possession), and the
// evidence, landing later, banned the throwaway and burned its empty
// escrow. The miner that double-signed kept its escrow and its account.
//
// From the height, registering a key that another miner registered before
// is refused, even after that miner has moved to a new key, so the index
// keeps naming whoever held the key and the penalty finds them. The miner
// that first registered it may register it again. A genesis validator's
// key (indexed to its own address) may still be bound to a miner account,
// as before.

// ConsensusKeyOwnershipHeight switches this on: block 1 on fresh chains;
// on the testnet 812,880, with x/accountauth's SessionKeyCapHeight (an
// app test keeps them equal), so one validator swap carries both.
const ConsensusKeyOwnershipHeight int64 = 812_880
