package counterparty

import (
	porttypes "github.com/cosmos/ibc-go/v8/modules/core/05-port/types"

	transfer "github.com/cosmos/ibc-go/v8/modules/apps/transfer"
	ibctransfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
)

// newIBCRouter wires only ICS-20 transfer -- this chain exists to be a
// real second process for testing Aether's IBC transfer path with a
// real relayer, not a general-purpose IBC chain.
func newIBCRouter(app *App) *porttypes.Router {
	router := porttypes.NewRouter()
	router.AddRoute(ibctransfertypes.ModuleName, transfer.NewIBCModule(app.TransferKeeper))
	return router
}
