package pow

import (
	"cosmossdk.io/math"
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
			BlockReward: 5_000_000, // 5,000,000 uaeth = 5.00 AETH -- matches Year 1 of the locked
                        // decay schedule (see tail-emission-decision.md), though this
                        // flat genesis default will be superseded once DistributeBlockReward
                        // is wired to compute the real height-based decay curve rather than
                        // reading this single static value.
			TailEmission:      false,
			EpochLength:       1440, // ~24h at 60s target blocks
			TopKSize:          21,   // BFT-performance sweet spot; see design doc §4
			BondCooldown: 100, // arbitrary placeholder for testing; production value needs real analysis
			RecencyWindowK: 60, // widened from the original 10 after live testing showed
                    // real Scrypt mining introduces genuine multi-minute
                    // submission variance -- 10 was too tight and rejected
                    // honest, valid submissions. See liveness-detection-decision.md.
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
	KeyLastAcceptedSubmissionHeight = []byte("last_accepted_submission_height")
)

// Locked block-reward decay schedule (see tail-emission-decision.md).
// These are permanent protocol constants, not genesis-tunable
// parameters -- Aether's monetary policy is meant to be as fixed and
// predictable as Bitcoin's halving schedule, not something an operator
// can casually reconfigure per-deployment.
const (
	InitialBlockReward    int64 = 5_000_000 // 5.00 AETH, in uaeth
	BlockRewardDecayYears int64 = 8
	TailBlockReward       int64 = 200_000 // 0.20 AETH, in uaeth, permanent from year 9 onward
	SecondsPerYear        int64 = 31_557_600 // 365.25 days -- a fixed constant, not computed from wall-clock time
)

// BlockRewardDecayFactor is ~34% annual decay (66% retained per year),
// confirmed by direct calculation to land at ~0.18 AETH after 8 years
// from a 5.00 AETH start -- NOT the originally-stated "18%/year",
// which was mathematically verified to be incorrect (it would only
// reach ~1.02 AETH after 8 years). Uses math.LegacyDec (deterministic,
// arbitrary-precision fixed-point), never float64 -- floating-point
// arithmetic is not guaranteed to produce identical results across
// different systems, which would risk a consensus fork.
var BlockRewardDecayFactor = math.LegacyMustNewDecFromStr("0.66")

// KeyBootstrapPowerCorrected guards a genuine, one-time, live
// correction (see Keeper.CorrectBootstrapPower) for a real,
// previously-shipped gap: the genesis bootstrap validator's real
// CometBFT-visible voting power was never actually reduced from its
// large genesis placeholder down to the standard flat power every
// other Top-K-selected validator receives.
var KeyBootstrapPowerCorrected = []byte("bootstrap_power_corrected")

// BootstrapPowerCorrectionHeight is the real, historical block height
// on aether-testnet-1 at which Keeper.CorrectBootstrapPower actually
// fired on the live seed node (157.245.252.221) -- confirmed by an
// independent fresh-sync AppHash bisection (Gitty), since the original
// deploy (Aug 29, 2026) predates this height gate existing.
//
// A first attempt at this constant (71100) was wrong -- it came from a
// journalctl timestamp lookup that did not actually correspond to the
// correction height and was never cross-checked against AppHash. Trust
// the AppHash-level bisection over log-timestamp inference: it directly
// confirmed AppHash agreement with the seed through height 40866, then
// divergence when validating block 40867's header. Per CometBFT's
// deferred-execution convention, block H's header carries the AppHash
// resulting from *committing block H-1*, not block H itself (the
// header is finalized before that block's own Commit runs) -- so a
// mismatch surfacing on block 40867's header means the state after
// committing block 40866 already differed, i.e. the diverging
// execution was block 40866's EndBlock, not 40867's.
//
// This MUST be a fixed height, not a store-flag check ("has my own
// local state ever run this before"): a node replaying chain history
// from genesis reaches its own "first EndBlock ever" at height 1, not
// at the real historical height the live network was at when this
// first deployed. A store-flag gate makes a fresh node apply the
// ValidatorUpdate at a different height than a node that was running
// continuously, so the two compute different state and AppHash
// diverges permanently starting at block 2. Height-gating (the same
// pattern real chain upgrades use) makes every node -- fresh or
// continuously-running -- apply the correction at the identical real
// height, which is what determinism requires.
const BootstrapPowerCorrectionHeight int64 = 40866