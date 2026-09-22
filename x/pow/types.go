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
	KeyPendingKeyRevocationPrefix = []byte("pending_key_revocation/") // miner addr -> old consensus pubkey bytes
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

// SubmissionCapActivationHeight is the real height at which the
// one-accepted-submission-per-block-height cap (see SubmitPoW's
// dispatcher in msg_server.go) actually started running on the live
// seed -- confirmed via journalctl around the real deploy restart
// (stopped after committing 51006, new process's first commit was
// 51007) and cross-checked by height/timestamp regression from two
// independently-verified anchors (40866 and 71174), which put the
// deploy at ~51019, twelve blocks off.
//
// This is the SAME bug class as BootstrapPowerCorrectionHeight, found
// independently: the cap's Get/Set pair was added with no activation
// gate at all, so a fresh node replaying pre-deploy history pays gas
// for a check that didn't exist yet when the seed originally processed
// those blocks, producing a different (higher) gas_used on any tx that
// happens to hit the gas limit -- confirmed concretely on a real
// height-40914 MsgSubmitPoW: seed gas_used 200223, fresh replay
// (unpatched) 201316, causing a LastResultsHash mismatch even though
// AppHash still matched (both nodes reject the same tx, just disagree
// on how much gas the rejection cost).
//
// Unlike BootstrapPowerCorrectionHeight (a single one-time event),
// this is a permanent behavior active from this height onward -- so
// it's gated with >=, not ==, and needs no persisted flag: before this
// height the check and its tracking write are both skipped entirely
// (matching the seed's real pre-deploy history, where the tracking key
// never existed yet), and from this height on every node runs it
// identically, which is exactly what the live seed has done since.
const SubmissionCapActivationHeight int64 = 51007

// BanEnforcementActivationHeight and RotationRevocationActivationHeight
// gate two real, live-flagged fixes (Gitty, Section 3 items 3 and 1):
// a permanently-banned miner was never actually rejected by SubmitPoW
// (IsBanned was only ever checked in Top-K qualification filtering, so
// a banned miner kept minting the full real block reward forever), and
// RegisterValidatorPubkey never revoked an old consensus key's real
// CometBFT voting power on rotation (the only revocation path builds
// its update from whatever pubkey is CURRENTLY registered, never the
// one that actually held power).
//
// A live-history audit (tx_search across every MsgSubmitPoW and
// MsgRegisterValidatorPubkey ever submitted, cross-checked against
// ban-status for every miner address that ever appeared) confirmed
// both paths are clean: no miner has ever been banned at all, and no
// miner has ever registered a second consensus pubkey while active.
// Unlike BootstrapPowerCorrectionHeight and SubmissionCapActivationHeight,
// this means neither fix actually needs a height gate to replay
// history correctly -- the buggy behavior was never historically
// exercised, so a fresh replay computes identically with or without
// the gate.
//
// Gated anyway, on principle, matching the same family as the other
// two: an unconditional accept/reject change to SubmitPoW,
// RegisterValidatorPubkey, or Top-K removal is exactly the class of
// change that has twice now silently broken fresh-node replay on this
// chain. A future re-audit finding either check wrong would be exactly
// as costly as the first two times; gating costs nothing when the
// history is clean and is the only thing that costs nothing when it
// turns out not to be.
//
// Originally set to 77000, on the assumption the whole fleet would
// upgrade to this binary before the chain reached that height. That
// assumption broke: seed and sync3 never upgraded, the live chain
// crossed 77000 running the old pre-gate binary, and when peer-1
// solo-upgraded to this code at tip ~79808 it diverged immediately
// (LastResultsHash mismatch) -- because replaying/continuing past
// 77000 with the NEW gate logic disagrees with what the network's
// actual historical blocks were finalized with (the OLD, ungated
// logic). Gitty confirmed and rolled peer-1 back to the pre-gate
// binary.
//
// This means 77000 is now a burned height: it can never be used by
// this code again, on any node, for the same reason a fresh replay
// through that window would compute differently than the live chain
// did. Re-set to 90000 -- comfortably ahead of the tip at the time of
// this fix (~79959) -- and, critically, this new height must not take
// effect on ANY node until the full fleet (seed, sync3, sync4, peer-1)
// swaps to this binary together, confirms matching AppHash, and only
// then is allowed to cross 90000. Confirm/adjust this against the
// seed's actual height immediately before the coordinated cutover --
// if the fleet won't be ready with real margin before 90000, bump it
// further rather than repeat this exact mistake a third time.
const (
	BanEnforcementActivationHeight     int64 = 90000
	RotationRevocationActivationHeight int64 = 90000
)