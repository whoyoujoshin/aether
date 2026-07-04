package pow

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	ModuleName = "pow"
	StoreKey   = ModuleName
)

type Params struct {
	TargetBlockTime   int64   `json:"target_block_time" yaml:"target_block_time"` // seconds (default 60)
	InitialDifficulty uint64  `json:"initial_difficulty" yaml:"initial_difficulty"`
	MinDifficulty     uint64  `json:"min_difficulty" yaml:"min_difficulty"`
	MaxDifficulty     uint64  `json:"max_difficulty" yaml:"max_difficulty"`
	BlockReward       sdk.Int `json:"block_reward" yaml:"block_reward"` // base reward in uaeth
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

var (
	KeyParams         = []byte("params")
	KeyLastDifficulty = []byte("last_difficulty")
	KeyLastBlockTime  = []byte("last_block_time")
)
