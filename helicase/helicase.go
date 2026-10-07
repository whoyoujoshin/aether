// Package helicase is the node-side half of Helicase (see app/helicase.go):
// a worker inside aetherd that watches another chain and prepares the
// relay transactions this node puts in its blocks when it proposes.
//
// Each cycle it reads Aether's light client of the other chain, finds
// every open channel over it, and gathers, as of the other chain's
// latest block:
//
//   - packets the other chain sent that Aether hasn't received;
//   - acknowledgements the other chain wrote for packets Aether sent,
//     which Aether hasn't processed yet;
//   - packets Aether sent that the other chain never received before
//     their timeout, so Aether can refund them;
//
// each with its proof, behind one client update to that block. Each
// message becomes its own relay transaction, so one that fails (say, a
// packet another proposer delivered first) doesn't take the others with
// it. The worker signs nothing and holds no key: it reads the other chain
// over RPC, and the light client checks everything it proves.
package helicase

import (
	"context"
	"errors"
	"sync"
	"time"

	"cosmossdk.io/log"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/relayer"
)

// Config is one path Helicase relays in from.
type Config struct {
	// CounterpartyRPC is the other chain's CometBFT RPC. It must serve
	// tx_search (a transaction index) and proofs.
	CounterpartyRPC string
	// ClientID is Aether's 07-tendermint client of the other chain;
	// every open channel over a connection on it is relayed.
	ClientID string
	// AetherRPC is this node's own RPC.
	AetherRPC string
	// Interval is how often to look for work.
	Interval time.Duration
	// RefreshAfter is how stale Aether's client of the other chain may get
	// on a quiet channel before Helicase updates it anyway
	// (relayer.Plan). Zero means DefaultRefreshAfter.
	RefreshAfter time.Duration
	// MaxMsgs caps the packets, acknowledgements and timeouts per cycle.
	// Zero means maxMsgsPerCycle; with several paths, Split divides it.
	MaxMsgs int
}

// DefaultRefreshAfter keeps Aether's view of the other chain within a
// few minutes of its tip. A transfer from Aether with a relative timeout
// (ibc-go's default is 1,000 of the other chain's blocks, ~12 min on
// Osmosis) counts from that view, so a staler one times the transfer out
// before it's sent. The updates ride in Aether blocks with no fee.
const DefaultRefreshAfter = 5 * time.Minute

// maxMsgsPerCycle leaves room under the chain's cap on relay
// transactions per block (helicaseMaxTxsPerBlock) for the client update,
// or one per path when several paths share a block.
const maxMsgsPerCycle = 60

// staleAfter: a batch computed more than this many blocks before the one
// being proposed isn't proposed; the next cycle replaces it.
const staleAfter = 2

// Worker implements app.HelicaseSource.
type Worker struct {
	cfg      Config
	txConfig client.TxConfig
	encode   func(client.TxConfig, ...sdk.Msg) ([]byte, error)
	logger   log.Logger

	aether *relayer.Chain
	cparty *relayer.Chain

	mu        sync.Mutex
	batch     [][]byte
	batchTip  int64 // Aether's committed height when batch was computed
	lastError string
}

// New connects to both chains. encode builds a relay transaction
// (app.EncodeHelicaseTx; passed in so this package doesn't import app).
func New(cfg Config, cdc codec.Codec, txConfig client.TxConfig, encode func(client.TxConfig, ...sdk.Msg) ([]byte, error), logger log.Logger) (*Worker, error) {
	if cfg.CounterpartyRPC == "" || cfg.ClientID == "" {
		return nil, errors.New("helicase needs a counterparty RPC and a client ID")
	}
	if cfg.AetherRPC == "" {
		cfg.AetherRPC = "http://127.0.0.1:26657"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.RefreshAfter <= 0 {
		cfg.RefreshAfter = DefaultRefreshAfter
	}
	if cfg.MaxMsgs <= 0 {
		cfg.MaxMsgs = maxMsgsPerCycle
	}
	aether, err := relayer.NewReadOnlyChain("aether", cfg.AetherRPC, "", cdc, txConfig)
	if err != nil {
		return nil, err
	}
	// The chain ID, which proofs' heights carry the revision of, comes
	// from the client once it can be read.
	cparty, err := relayer.NewReadOnlyChain("counterparty", cfg.CounterpartyRPC, "", cdc, txConfig)
	if err != nil {
		return nil, err
	}
	return &Worker{
		cfg: cfg, txConfig: txConfig, encode: encode,
		logger: logger.With("module", "helicase", "client", cfg.ClientID),
		aether: aether, cparty: cparty,
	}, nil
}

// RelayTxs returns the latest batch, unless it's too old to be worth
// proposing at height.
func (w *Worker) RelayTxs(height int64) [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.batchTip < height-staleAfter {
		return nil
	}
	return w.batch
}

// Run cycles until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("helicase started", "counterparty", w.cfg.CounterpartyRPC)
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		w.runCycle()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) runCycle() {
	plan, err := relayer.Plan(w.cparty, w.aether, w.cfg.ClientID, "", w.cfg.MaxMsgs, w.cfg.RefreshAfter)
	if err != nil {
		w.mu.Lock()
		repeat := err.Error() == w.lastError
		w.lastError = err.Error()
		w.batch = nil
		w.mu.Unlock()
		if !repeat {
			w.logger.Error("helicase cycle failed", "err", err)
		}
		return
	}
	var batch [][]byte
	for _, msg := range plan.Msgs {
		bz, err := w.encode(w.txConfig, msg)
		if err != nil {
			w.logger.Error("helicase: encoding a relay transaction", "msg", sdk.MsgTypeURL(msg), "err", err)
			return
		}
		batch = append(batch, bz)
	}
	w.mu.Lock()
	w.batch, w.batchTip, w.lastError = batch, plan.DstTip, ""
	w.mu.Unlock()
	if plan.Any() {
		w.logger.Info("helicase: relay transactions ready", "aether_tip", plan.DstTip, "proof_height", plan.ProofHeight,
			"packets", plan.Packets, "acks", plan.Acks, "timeouts", plan.Timeouts)
	}
}

// Split divides the per-cycle message budget between n paths, so that
// all of them together stay within one block's relay transactions.
func Split(n int) int {
	if n <= 1 {
		return maxMsgsPerCycle
	}
	if per := maxMsgsPerCycle / n; per > 0 {
		return per
	}
	return 1
}

// Sources is several workers, one per path (one chain Aether has a light
// client of), proposing their relay transactions in the same blocks.
type Sources []*Worker

// RelayTxs returns every worker's latest batch, path by path.
func (s Sources) RelayTxs(height int64) [][]byte {
	var all [][]byte
	for _, w := range s {
		all = append(all, w.RelayTxs(height)...)
	}
	return all
}
