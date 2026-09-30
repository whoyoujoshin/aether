package app

import (
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"

	"github.com/whoyoujoshin/aether/ligase"
	"github.com/whoyoujoshin/aether/x/escrow"
)

// LigaseActivationHeight is the first block at which a transfer to the
// Ligase address can carry an instruction (see package ligase): before
// it, and until x/escrow is live, every transfer is handled exactly as
// before. No store is added, so nothing halts here, but the result of
// such a transfer changes, so every validator needs a binary carrying
// the height before it.
//
// Set to 161,000 with the operator, the same cutover as x/escrow
// (docs/CUTOVER-161000.md).
const LigaseActivationHeight int64 = 161_000

// ligaseActivationHeight is what the host reads, so tests can cross the
// activation at a small height.
var ligaseActivationHeight = LigaseActivationHeight

// ligaseHost gives package ligase what it needs from the app, read at
// call time: x/escrow wires up after the IBC router is built.
type ligaseHost struct{ app *App }

var _ ligase.Host = ligaseHost{}

func (h ligaseHost) Active(ctx sdk.Context) bool {
	return h.app.escrowWired && ctx.BlockHeight() >= ligaseActivationHeight
}

func (h ligaseHost) Escrow() escrow.MsgServer { return escrow.NewMsgServerImpl(h.app.EscrowKeeper) }

func (h ligaseHost) GetEscrow(ctx sdk.Context, id uint64) (escrow.Escrow, bool) {
	return h.app.EscrowKeeper.GetEscrow(ctx, id)
}

func (h ligaseHost) Balances(ctx sdk.Context, addr sdk.AccAddress) sdk.Coins {
	return h.app.BankKeeper.GetAllBalances(ctx, addr)
}

func (h ligaseHost) DenomTrace(ctx sdk.Context, ibcDenom string) (transfertypes.DenomTrace, bool) {
	hash, err := transfertypes.ParseHexHash(strings.TrimPrefix(ibcDenom, "ibc/"))
	if err != nil {
		return transfertypes.DenomTrace{}, false
	}
	return h.app.TransferKeeper.GetDenomTrace(ctx, hash)
}

func (h ligaseHost) Transfer(ctx sdk.Context, msg *transfertypes.MsgTransfer) error {
	_, err := h.app.TransferKeeper.Transfer(ctx, msg)
	return err
}
