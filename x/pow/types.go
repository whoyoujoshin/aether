package pow

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	ModuleName = "pow"
	StoreKey   = ModuleName
	LivenessWindowSize     = 60
	LivenessMissThreshold  = 0.5 // 50% -- more than this within the window triggers removal
)

type Params struct {
	TargetBlockTime   int64 `json:"target_block_time" yaml:"target_block_time"`
	InitialDifficulty int   `json:"initial_difficulty" yaml:"initial_difficulty"`
	MinDifficulty     int   `json:"min_difficulty" yaml:"min_difficulty"`
	MaxDifficulty     int   `json:"max_difficulty" yaml:"max_difficulty"`
	Difficulty        int   `json:"difficulty" yaml:"difficulty"`
	BlockReward       int   `json:"block_reward" yaml:"block_reward"`
	TailEmission      bool  `json:"tail_emission" yaml:"tail_emission"` // For sustainable model post-initial phase
	EpochLength       int64 `json:"epoch_length" yaml:"epoch_length"`   // Blocks per validator-selection epoch; see randomness-beacon design doc §4
	TopKSize          int64 `json:"top_k_size" yaml:"top_k_size"`       // Max number of validators selected per epoch; see randomness-beacon design doc §4
	BondCooldown int64 `json:"bond_cooldown" yaml:"bond_cooldown"` // Blocks an active validator's rewards stay escrowed before automatic release
	RecencyWindowK int64 `json:"recency_window_k" yaml:"recency_window_k"` // Max blocks between a mining header's claimed ancestor and current height
}

type MiningHeader struct {
	Height       uint64         `json:"height"`
	Timestamp    int64          `json:"timestamp"`
	PrevHash     []byte         `json:"prev_hash"`
	MerkleRoot   []byte         `json:"merkle_root"`
	Nonce        uint64         `json:"nonce"`
	Difficulty   uint64         `json:"difficulty"`
	MinerAddress sdk.AccAddress `json:"miner_address"`
}

type GenesisState struct {
	Params Params `json:"params"`
}

func DefaultGenesisState() GenesisState {
	return GenesisState{
		Params: Params{
			TargetBlockTime:   60,
			InitialDifficulty: 285_960, // Retuned for real Scrypt (Litecoin params: N=1024, r=1, p=1),
                             // measured at ~4,767 hashes/sec single-threaded -- averages
                             // ~60s per nonce, matching TargetBlockTime. The old 1<<20
                             // value was tuned against SHA-256's raw speed and meant
                             // something entirely different under Scrypt's memory-hardness.
			MinDifficulty:     1_024,    // ~0.2s average at measured throughput -- an easy floor,
                             // giving AdjustDifficulty real room to move down.
			MaxDifficulty:     100_000_000, // ~5.8 hours average at measured throughput -- generous
                                // headroom for faster hardware / more miners joining later.
			Difficulty:        285_960,     // Starts equal to InitialDifficulty.
			BlockReward:       5_000_000, // 5,000,000 aeth (whole-unit denom, no sub-unit scaling)
			TailEmission:      false,
			EpochLength:       1440, // ~24h at 60s target blocks
			TopKSize:          21,   // BFT-performance sweet spot; see design doc §4
			BondCooldown: 100, // arbitrary placeholder for testing; production value needs real analysis
			RecencyWindowK: 10,
		},
	}
}
var (
	KeyParams        = []byte("params")
	KeyDifficulty    = []byte("difficulty")
	KeyBlockReward   = []byte("block_reward")
	KeyLastBlockTime = []byte("last_block_time")
	KeyMinDifficulty = []byte("min_difficulty")
	KeyMaxDifficulty = []byte("max_difficulty")
	KeyTargetBlockTime = []byte("target_block_time")
	KeyValidatorPubkeyPrefix = []byte("validator_pubkey/")
	KeyEpochLength     = []byte("epoch_length")
	KeyEpochWorkPrefix = []byte("epoch_work/")
	KeyActiveValidatorPrefix = []byte("active_validator/")
	KeyConsensusToMinerPrefix = []byte("consensus_to_miner/")
	KeyBannedPrefix           = []byte("banned/")
	KeyEscrowPrefix       = []byte("escrow/")
	KeyEscrowUnlockPrefix = []byte("escrow_unlock/")
	KeyBondCooldown       = []byte("bond_cooldown")
	KeyPendingRemovalPrefix = []byte("pending_removal/")
	KeyRecentHashPrefix       = []byte("recent_hash/")
	KeyRecentDifficultyPrefix = []byte("recent_difficulty/")
	KeyAcceptedWorkPrefix     = []byte("accepted_work/")
	KeyRecencyWindowK = []byte("recency_window_k")
	KeyValidatorEnteredAtPrefix = []byte("validator_entered_at/")
	KeyLivenessBitmapPrefix = []byte("liveness_bitmap/") // validator addr -> [60]byte (0/1 per slot)
	KeyLivenessIndexPrefix  = []byte("liveness_index/")  // validator addr -> current write index (0-59)
	KeyLivenessMissedPrefix = []byte("liveness_missed/") // validator addr -> current miss count in window
)