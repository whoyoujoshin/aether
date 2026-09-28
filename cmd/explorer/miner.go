package main

import (
	"fmt"
	"net/http"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// --- GET /api/miner?addr= ---
//
// A miner's standing this epoch: registered key, work, rank among
// eligible miners, blocks until the validator set is picked, and whether
// it's in the set now. See wallet.MinerStatus for each field.
func handleMiner(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("addr")
	if _, err := sdk.AccAddressFromBech32(addr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid 'addr': %w", err))
		return
	}
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer client.Close()
	st, err := client.MinerStatus(r.Context(), addr)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to load miner status: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, st)
}
