package pow_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/pow"
)

// On a fresh chain every rule the testnet switched on at a height
// applies from block 1: here, seat work and the seat rule.
func TestFreshChain_SeatRuleFromBlockOne(t *testing.T) {
	k, ctx, _ := setupKeeper(t)
	ctx = ctx.WithChainID(gates.MainnetChainID)
	srv := pow.NewMsgServerImpl(k)
	hash := []byte("block-hash-fresh-chain-native")
	ctx = setupRecentBlock(k, ctx, 1, hash, 2).WithBlockHeight(2).WithBlockTime(time.Unix(1_900_000_000, 0))
	k.SetBlockReward(ctx, math.NewInt(5_000_000))
	miner, addr := validMinerAddr(t)
	var err error
	for nonce := uint64(0); nonce < 200; nonce++ {
		if _, err = srv.SubmitPoW(ctx, newNativeSubmitMsg(addr, 1, ctx.BlockTime().Unix(), hash, []byte("merkle"), nonce, 2)); err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.Equal(t, math.NewInt(2), seatWork(k, ctx, 0, miner), "seat work from block 2")

	k.SetValidatorPubkey(ctx, miner, genValidatorPubkey(t))
	updates := k.ComputeValidatorUpdates(ctx.WithBlockHeight(1439), 0)
	require.Len(t, updates, 1)
	require.Equal(t, pow.SeatPowerScale, updates[0].Power, "power by seat work, not the flat ValidatorVotingPower")

	// The same submission on the testnet's chain ID, at the same height,
	// records no seat work.
	k2, ctx2, _ := setupKeeper(t)
	ctx2 = setupRecentBlock(k2, ctx2.WithChainID(gates.TestnetChainID), 1, hash, 2).WithBlockHeight(2).WithBlockTime(time.Unix(1_900_000_000, 0))
	k2.SetBlockReward(ctx2, math.NewInt(5_000_000))
	srv2 := pow.NewMsgServerImpl(k2)
	for nonce := uint64(0); nonce < 200; nonce++ {
		if _, err = srv2.SubmitPoW(ctx2, newNativeSubmitMsg(addr, 1, ctx2.BlockTime().Unix(), hash, []byte("merkle"), nonce, 2)); err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.True(t, seatWork(k2, ctx2, 0, sdk.MustAccAddressFromBech32(addr)).IsZero())
}

// A fresh chain's genesis validator is active under an address made from
// its own consensus key. When a miner registers that key, it takes the
// validator over: one seat, all of its power, and no removal sent for
// the key it now holds. On the testnet's chain ID nothing changes.
func TestFreshChain_MinerInheritsGenesisValidator(t *testing.T) {
	run := func(chainID string) ([]int64, bool) {
		k, ctx, _ := setupKeeper(t)
		ctx = ctx.WithChainID(chainID).WithBlockHeight(pow.SeatRuleActivationHeight + 1439)
		pub, _ := genValidatorKeyPair(t)
		consAddr := cometed25519.PubKey(pub).Address()
		genesis := sdk.AccAddress(consAddr)
		k.SetValidatorPubkey(ctx, genesis, pub)
		k.SetConsensusToMiner(ctx, consAddr, genesis)
		k.SetActiveValidator(ctx, genesis)

		miner := sdk.AccAddress("miner_taking_over___")
		k.SetValidatorPubkey(ctx, miner, pub)
		k.SetConsensusToMiner(ctx, consAddr, miner)
		k.AddSeatWork(ctx, seatEpoch, miner, math.NewInt(100))

		var powers []int64
		for _, u := range k.ComputeValidatorUpdates(ctx, seatEpoch) {
			powers = append(powers, u.Power)
		}
		return powers, k.IsActiveValidator(ctx, genesis)
	}

	powers, genesisActive := run(gates.RehearsalPrefix + "test")
	require.Equal(t, []int64{pow.SeatPowerScale}, powers, "one update for the key, with all the power")
	require.False(t, genesisActive)

	powers, genesisActive = run(gates.TestnetChainID)
	require.Len(t, powers, 2, "the testnet keeps two seats on the key, as 3988485 does")
	require.True(t, genesisActive)
}

func genValidatorKeyPair(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv := cometed25519.GenPrivKey()
	return priv.PubKey().Bytes(), priv.Bytes()
}
