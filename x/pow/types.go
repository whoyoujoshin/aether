package pow

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	ModuleName = "pow"
	StoreKey   = ModuleName
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
			InitialDifficulty: 1 << 20,
			MinDifficulty:     1 << 10,
			MaxDifficulty:     1 << 40,
			Difficulty:        1 << 20,
			BlockReward:       5_000_000, // 5,000,000 aeth (whole-unit denom, no sub-unit scaling)
			TailEmission:      false,
			EpochLength:       1440, // ~24h at 60s target blocks
			TopKSize:          21,   // BFT-performance sweet spot; see design doc §4
			BondCooldown: 100, // arbitrary placeholder for testing; production value needs real analysis
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
)