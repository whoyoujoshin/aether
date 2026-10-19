package pow_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/pow"
)

var seatEpoch = pow.SeatRuleActivationHeight / pow.DefaultGenesisState().Params.EpochLength

func seatWork(k pow.Keeper, ctx sdk.Context, epoch int64, addr sdk.AccAddress) math.Int {
	for _, e := range k.IterateSeatWork(ctx, epoch) {
		if e.Addr.Equals(addr) {
			return e.Work
		}
	}
	return math.ZeroInt()
}

// A native submission adds the difficulty it had to meet to its miner's
// seat work, from the activation height only.
func TestSeatRule_NativeSubmissionCreditsItsDifficulty(t *testing.T) {
	for _, tc := range []struct {
		height int64
		want   int64
	}{{pow.SeatRuleActivationHeight - 1, 0}, {pow.SeatRuleActivationHeight, 2}} {
		k, ctx, _ := setupKeeper(t)
		srv := pow.NewMsgServerImpl(k)
		hash := []byte("block-hash-native-seat-work")
		ctx = setupRecentBlock(k, ctx, tc.height-1, hash, 2)
		ctx = ctx.WithBlockHeight(tc.height).WithBlockTime(time.Unix(1_900_000_000, 0))
		k.SetBlockReward(ctx, math.NewInt(5_000_000))
		miner, addr := validMinerAddr(t)

		var err error
		for nonce := uint64(0); nonce < 200; nonce++ {
			msg := newNativeSubmitMsg(addr, uint64(tc.height-1), ctx.BlockTime().Unix(), hash, []byte("merkle"), nonce, 2)
			if _, err = srv.SubmitPoW(ctx, msg); err == nil {
				break
			}
		}
		require.NoError(t, err)
		require.Equal(t, uint64(1), k.GetMiningWork(ctx, k.CurrentEpoch(ctx), miner), "the submission count is kept as before")
		require.Equal(t, math.NewInt(tc.want), seatWork(k, ctx, k.CurrentEpoch(ctx), miner), "height %d", tc.height)
	}
}

// An AuxPoW submission adds the AuxPoW difficulty to the reward address
// it commits to, not to whoever relayed it.
func TestSeatRule_AuxPoWCreditsTheRewardAddress(t *testing.T) {
	c := newAuxPowCase(t)
	c.templateHeight = pow.SeatRuleActivationHeight - 1
	c.ctx = setupRecentBlock(c.k, c.ctx, c.templateHeight, c.blockHash, auxTestDifficulty).WithBlockHeight(pow.SeatRuleActivationHeight)
	c.k.SetAuxDifficulty(c.ctx, math.NewInt(auxTestDifficulty))

	require.NoError(t, c.submit(c.proof(t, c.pool)))
	epoch := c.k.CurrentEpoch(c.ctx)
	require.Equal(t, math.NewInt(auxTestDifficulty), seatWork(c.k, c.ctx, epoch, c.pool))
	require.True(t, seatWork(c.k, c.ctx, epoch, c.relayer).IsZero())
	require.Zero(t, c.k.GetMiningWork(c.ctx, epoch, c.pool), "AuxPoW still adds nothing to the submission count")
}

// The first epoch under the rule: powers follow the window's work, the
// validators seated under the old rule count as holding
// ValidatorVotingPower, the one that stopped mining fades rather than
// vanishing, and the oldest epoch's work is pruned.
func TestSeatRule_EpochTransition(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.SeatRuleActivationHeight + 1439)
	a, b, gone := sdk.AccAddress("seat_rule_validator_a"), sdk.AccAddress("seat_rule_validator_b"), sdk.AccAddress("seat_rule_stopped____")
	for _, v := range []sdk.AccAddress{a, b, gone} {
		k.SetValidatorPubkey(ctx, v, genValidatorPubkey(t))
	}
	// Seated under the old rule, equal power.
	k.SetActiveValidator(ctx, a)
	k.SetActiveValidator(ctx, gone)

	old := seatEpoch - pow.SeatWorkWindowEpochs + 1
	k.AddSeatWork(ctx, old, a, math.NewInt(40))
	k.AddSeatWork(ctx, seatEpoch, a, math.NewInt(20))
	k.AddSeatWork(ctx, seatEpoch, b, math.NewInt(40))
	k.AddSeatWork(ctx, seatEpoch+1, b, math.NewInt(1_000)) // outside the window

	updates := k.ComputeValidatorUpdates(ctx, seatEpoch)
	require.Len(t, updates, 3)

	// Target: a 60%, b 40%, gone 0, from 50/0/50: 50% would move, so
	// each moves a fifth of the way.
	powers := map[string]int64{}
	for _, v := range []sdk.AccAddress{a, b, gone} {
		require.True(t, k.IsActiveValidator(ctx, v))
		p, ok := k.GetValidatorPower(ctx, v)
		require.True(t, ok)
		powers[string(v)] = p
	}
	require.Equal(t, int64(52_000_000), powers[string(a)])
	require.Equal(t, int64(8_000_000), powers[string(b)])
	require.Equal(t, int64(40_000_000), powers[string(gone)])

	require.Empty(t, k.IterateSeatWork(ctx, old), "the oldest epoch isn't in the next window")
	require.NotEmpty(t, k.IterateSeatWork(ctx, seatEpoch))

	// No seat work at all: the set stays as it is.
	require.Nil(t, k.ComputeValidatorUpdates(ctx, seatEpoch+10))
	require.True(t, k.IsActiveValidator(ctx, gone))
}

// Below the activation height selection is the old rule, whatever seat
// work is recorded.
func TestSeatRule_OldRuleBelowTheHeight(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(pow.SeatRuleActivationHeight - 1)
	miner := sdk.AccAddress("old_rule_miner_______")
	k.SetValidatorPubkey(ctx, miner, genValidatorPubkey(t))
	k.AddMiningWork(ctx, seatEpoch-1, miner, 1)
	k.AddSeatWork(ctx, seatEpoch-1, miner, math.NewInt(1_000))

	updates := k.ComputeValidatorUpdates(ctx, seatEpoch-1)
	require.Len(t, updates, 1)
	require.Equal(t, int64(pow.ValidatorVotingPower), updates[0].Power)
	_, ok := k.GetValidatorPower(ctx, miner)
	require.False(t, ok)
}

// A validator's rewards fill its escrow up to the bond floor, and the rest
// is paid out, from the activation height; below it, all of it stays in
// escrow. The bond's unlock height moves forward either way.
func TestSeatRule_BondFloor(t *testing.T) {
	floor := math.NewInt(pow.ValidatorBondFloor)
	reward := math.NewInt(5_000_000)
	cut := reward.MulRaw(15).QuoRaw(100)
	// The miner's part, plus the validators' 13/15 of the cut: v is the
	// only validator.
	minerPart := reward.Sub(cut).Add(cut.MulRaw(13).QuoRaw(15))

	k, ctx, bank := setupKeeper(t)
	v := sdk.AccAddress("bond_floor_validator_")
	k.SetActiveValidator(ctx, v)
	k.AddEscrow(ctx, v, floor.Sub(math.NewInt(1_000_000)))

	below := ctx.WithBlockHeight(pow.SeatRuleActivationHeight - 1)
	require.NoError(t, k.DistributeReward(below, v, reward))
	require.Equal(t, floor.Sub(math.NewInt(1_000_000)).Add(minerPart), k.GetEscrowBalance(ctx, v), "below the height, all of it is escrowed")
	for _, s := range bank.SendCalls {
		require.False(t, v.Equals(s.RecipientAddr))
	}

	k.ClearEscrow(ctx, v)
	k.AddEscrow(ctx, v, floor.Sub(math.NewInt(1_000_000)))
	at := ctx.WithBlockHeight(pow.SeatRuleActivationHeight)
	require.NoError(t, k.DistributeReward(at, v, reward))
	require.Equal(t, floor, k.GetEscrowBalance(ctx, v), "escrow fills to the floor")
	paid := math.ZeroInt()
	for _, s := range bank.SendCalls {
		if v.Equals(s.RecipientAddr) {
			paid = paid.Add(s.Coins.AmountOf("uaeth"))
		}
	}
	require.Equal(t, minerPart.Sub(math.NewInt(1_000_000)), paid, "the rest is paid out")

	// Full escrow: everything is paid out, and the bond's unlock still
	// moves forward.
	later := at.WithBlockHeight(pow.SeatRuleActivationHeight + 50)
	require.NoError(t, k.DistributeReward(later, v, reward))
	require.Equal(t, floor, k.GetEscrowBalance(ctx, v))
	unlock, ok := k.GetEscrowUnlockHeight(ctx, v)
	require.True(t, ok)
	require.Equal(t, pow.SeatRuleActivationHeight+50+k.GetBondCooldown(ctx), unlock)
}
