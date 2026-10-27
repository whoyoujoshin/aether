package app

import (
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// InflowCapActivationHeight is the first block that executes with
// x/inflowcap live (docs/IBC-INFLOW-CAP.md): from it, a token from
// another chain comes in only if it is listed for its channel, and only
// up to its cap. Same mechanism as EscrowActivationHeight: it adds a
// store, so an un-restarted node halts at it rather than run it without.
// On a fresh chain it is on from block 1, with the list from genesis.
//
// A placeholder far past the testnet's tip: the testnet height is chosen
// after a devnet round trip with Helicase and outbound, together with
// testnetInflowEntries.
const InflowCapActivationHeight int64 = 30_000_000

// inflowCapActivationHeight is what New() reads, so tests can cross the
// activation at a small height.
var inflowCapActivationHeight = InflowCapActivationHeight

var inflowCapStoreKeys = []string{inflowcap.StoreKey}

type inflowCapPlan struct {
	wire      bool // mount the store and register keeper, module and messages
	addStores bool // this is the one startup that creates the store
}

// planInflowCap mirrors planEscrow.
func planInflowCap(lastCommittedHeight, activationHeight int64) inflowCapPlan {
	if lastCommittedHeight < activationHeight-1 {
		return inflowCapPlan{}
	}
	return inflowCapPlan{wire: true, addStores: lastCommittedHeight == activationHeight-1}
}

// inflowCapMarkerKey keeps the new store non-empty from its first block,
// for the reason escrowMarkerKey does. 0xff is no prefix the module uses.
var inflowCapMarkerKey = []byte("\xffaether/inflowcap-activated")

// testnetInflowEntries is the list aether-testnet-1 starts with at its
// activation height, where no genesis sets one: the two paths in use,
// with low caps, so the trial on the testnet exercises both refusals.
// Every other voucher (INJ, the dead Osmosis channel-1) is refused from
// then on. Governance changes the list afterwards.
//
//   - USDC from Injective over channel-2 (wallet.TestnetUSDC), 100 USDC.
//   - OSMO from Osmosis over channel-3 (docs/OSMOSIS-POOL.md), 1,000 OSMO.
func testnetInflowEntries() []inflowcap.Entry {
	return []inflowcap.Entry{
		{ChannelId: "channel-2", Denom: "erc20:0x0C382e685bbeeFE5d3d9C29e29E341fEE8E84C5d", Cap: sdkmath.NewInt(100_000_000)},
		{ChannelId: "channel-3", Denom: "uosmo", Cap: sdkmath.NewInt(1_000_000_000)},
	}
}

// checkInflowCapActivation halts a node that reaches the activation
// height without the store, like checkEscrowActivation. At that height
// on a chain whose genesis didn't set the list (the testnet), it sets
// testnetInflowEntries.
func (app *App) checkInflowCapActivation(ctx sdk.Context) error {
	if app.inflowCapWired {
		if gates.At(ctx, inflowCapActivationHeight) {
			for _, name := range inflowCapStoreKeys {
				ctx.KVStore(app.keys[name]).Set(inflowCapMarkerKey, []byte{1})
			}
			if !gates.Fresh(ctx.ChainID()) && !app.InflowCapKeeper.HasParams(ctx) {
				app.InflowCapKeeper.SetParams(ctx, inflowcap.Params{Entries: testnetInflowEntries()})
			}
		}
		return nil
	}
	if !gates.On(ctx, inflowCapActivationHeight) {
		return nil
	}
	return fmt.Errorf(
		"x/inflowcap activates at height %d: restart this node (e.g. `systemctl restart aetherd`) to add its store, then it will resume from this block",
		gates.Height(ctx.ChainID(), inflowCapActivationHeight),
	)
}

// inflowCapHost gives the middleware what it needs from the app, read at
// call time: x/inflowcap wires up after the IBC router is built.
type inflowCapHost struct{ app *App }

var _ inflowcap.Host = inflowCapHost{}

func (h inflowCapHost) Active(ctx sdk.Context) bool {
	return h.app.inflowCapWired && gates.On(ctx, inflowCapActivationHeight)
}

func (h inflowCapHost) Params(ctx sdk.Context) inflowcap.Params {
	return h.app.InflowCapKeeper.GetParams(ctx)
}

func (h inflowCapHost) Supply(ctx sdk.Context, denom string) sdk.Coin {
	return h.app.BankKeeper.GetSupply(ctx, denom)
}

func (h inflowCapHost) InFlight(ctx sdk.Context, voucher string) sdkmath.Int {
	return h.app.InflowCapKeeper.InFlight(ctx, voucher)
}

func (h inflowCapHost) AddInFlight(ctx sdk.Context, s inflowcap.InFlightSend) {
	h.app.InflowCapKeeper.AddInFlight(ctx, s)
}

func (h inflowCapHost) ReleaseInFlight(ctx sdk.Context, portID, channelID string, sequence uint64) {
	h.app.InflowCapKeeper.ReleaseInFlight(ctx, portID, channelID, sequence)
}

func (h inflowCapHost) GetInFlightSend(ctx sdk.Context, portID, channelID string, sequence uint64) (inflowcap.InFlightSend, bool) {
	return h.app.InflowCapKeeper.GetInFlightSend(ctx, portID, channelID, sequence)
}
