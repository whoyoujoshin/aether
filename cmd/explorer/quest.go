package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/quest"
	"github.com/whoyoujoshin/aether/wallet"
)

// --- GET /api/quest ---
//
// The agent quest's board (package quest), read from the chain: payments
// to the quest address, the directory and each listed payee. Loading it
// is a handful of scans, so it's cached for questTTL.

var questStart int64

const questTTL = time.Minute

type questResponse struct {
	quest.Board
	// Agent is the address asked about, if it has joined.
	Agent *quest.Progress `json:"agent,omitempty"`
}

type chainQuestSource struct{}

func (chainQuestSource) Incoming(_ context.Context, address string, since int64) ([]wallet.IncomingPayment, error) {
	c, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.GetIncomingPayments(address, since, 5000)
}

func (chainQuestSource) Listings(ctx context.Context) ([]directory.Listing, error) {
	return serviceDirectory().Listings(ctx)
}

var questCache struct {
	sync.Mutex
	board quest.Board
	at    time.Time
	load  func(ctx context.Context) (quest.Board, error) // nil: the chain
}

func loadQuest(ctx context.Context) (quest.Board, error) {
	questCache.Lock()
	defer questCache.Unlock()
	if !questCache.at.IsZero() && time.Since(questCache.at) < questTTL {
		return questCache.board, nil
	}
	load := questCache.load
	if load == nil {
		load = func(ctx context.Context) (quest.Board, error) { return quest.Load(ctx, chainQuestSource{}, questStart) }
	}
	b, err := load(ctx)
	if err != nil {
		return b, err
	}
	questCache.board, questCache.at = b, time.Now()
	return b, nil
}

func handleQuest(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("addr")
	if addr != "" {
		if _, err := sdk.AccAddressFromBech32(addr); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	b, err := loadQuest(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if b.Agents == nil {
		b.Agents = []quest.Progress{}
	}
	out := questResponse{Board: b}
	if addr != "" {
		out.Agent = b.Find(addr)
	}
	writeJSON(w, http.StatusOK, out)
}
