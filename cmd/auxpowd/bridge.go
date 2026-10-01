package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	pow "github.com/whoyoujoshin/aether/x/pow"
	powtypes "github.com/whoyoujoshin/aether/x/pow/types"
)

// chain is the bridge's view of an Aether node.
type chain interface {
	State(ctx context.Context) (chainState, error)
	// Submit signs and broadcasts an AuxPoW submission and returns its tx
	// hash once the node has accepted it into its mempool.
	Submit(ctx context.Context, d *pow.AuxPowData) (string, error)
	// TxResult reports a broadcast tx's outcome once it's in a block
	// (found false until then).
	TxResult(ctx context.Context, hash string) (res txResult, found bool, err error)
}

type txResult struct {
	Height    int64
	Code      uint32
	Codespace string
	Log       string
}

// rpcError is a JSON-RPC error with bitcoind's codes.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

const (
	rpcMiscError       = -1
	rpcInvalidParams   = -8
	rpcInvalidAddress  = -5
	rpcDeserialization = -22
	rpcNotActive       = -10 // bitcoind's "client in initial download": no work to hand out yet
)

type bridge struct {
	chain         chain
	templates     *templates
	defaultReward sdk.AccAddress // getauxblock's reward address; nil refuses getauxblock
	log           *log.Logger
	trackFor      time.Duration // how long to follow a broadcast tx for its outcome
	pollEvery     time.Duration

	submitMu      sync.Mutex
	lastSubmitTip int64 // the tip at the last broadcast: one submission per block

	m metrics
}

type metrics struct {
	templates atomic.Int64 // createauxblock/getauxblock answers
	invalid   atomic.Int64 // proofs that don't parse or don't pass x/pow's check
	stale     atomic.Int64 // valid proofs too late: template too old, or the block's slot taken
	submitted atomic.Int64 // broadcast to the chain
	accepted  atomic.Int64 // included and paid
	rejected  atomic.Int64 // included but refused, other than for the slot
	failed    atomic.Int64 // couldn't be broadcast
}

func newBridge(c chain, defaultReward sdk.AccAddress, logger *log.Logger) *bridge {
	return &bridge{
		chain:         c,
		templates:     newTemplates(),
		defaultReward: defaultReward,
		log:           logger,
		trackFor:      2 * time.Minute,
		pollEvery:     2 * time.Second,
	}
}

// state reads the chain and refuses to hand out or take work before
// MergedMiningActivationHeight: below it the chain pays whoever signs the
// submission, the bridge's own key, not the pool, and reads parent work in
// the wrong byte order.
func (b *bridge) state(ctx context.Context) (chainState, error) {
	st, err := b.chain.State(ctx)
	if err != nil {
		return chainState{}, &rpcError{rpcMiscError, "reading the Aether node: " + err.Error()}
	}
	if st.Height+1 < pow.MergedMiningActivationHeight {
		return chainState{}, &rpcError{rpcNotActive, fmt.Sprintf(
			"merged mining isn't active: it starts at Aether height %d and the chain is at %d",
			pow.MergedMiningActivationHeight, st.Height)}
	}
	return st, nil
}

func (b *bridge) createAuxBlock(ctx context.Context, address string) (*auxBlock, error) {
	reward, err := sdk.AccAddressFromBech32(strings.TrimSpace(address))
	if err != nil {
		return nil, &rpcError{rpcInvalidAddress, fmt.Sprintf("invalid Aether address %q: %v", address, err)}
	}
	st, err := b.state(ctx)
	if err != nil {
		return nil, err
	}
	t := b.templates.get(st, reward)
	b.m.templates.Add(1)
	ab := t.auxBlock(st)
	return &ab, nil
}

// submitAuxBlock checks a proof against the template it names and x/pow's
// own rules, then broadcasts it. It reports true once the node takes it
// into its mempool, as Namecoin reports true for an accepted block; the
// outcome on chain is logged and counted when it's known.
func (b *bridge) submitAuxBlock(ctx context.Context, hashHex, auxPowHex string) (bool, error) {
	hash, err := hex.DecodeString(strings.TrimSpace(hashHex))
	if err != nil || len(hash) != 32 {
		return false, &rpcError{rpcInvalidParams, "hash must be 32 bytes of hex"}
	}
	t := b.templates.lookup(reversed(hash))
	if t == nil {
		return false, &rpcError{rpcInvalidParams, "block hash unknown"}
	}
	raw, err := hex.DecodeString(strings.TrimSpace(auxPowHex))
	if err != nil {
		return false, &rpcError{rpcDeserialization, "auxpow isn't hex"}
	}
	a, err := parseAuxPow(raw)
	if err != nil {
		b.m.invalid.Add(1)
		return false, &rpcError{rpcDeserialization, "auxpow: " + err.Error()}
	}
	d, err := a.auxPowData(t)
	if err != nil {
		b.m.invalid.Add(1)
		b.log.Printf("refused proof for template %x: %v", t.Hash, err)
		return false, nil
	}

	st, err := b.state(ctx)
	if err != nil {
		return false, err
	}
	next := st.Height + 1
	if next-t.Height > st.RecencyWindow {
		b.m.stale.Add(1)
		b.log.Printf("stale: template at height %d is more than %d blocks behind height %d", t.Height, st.RecencyWindow, next)
		return false, nil
	}
	if err := pow.CheckAuxPow(d, st.AuxDifficulty, next); err != nil {
		b.m.invalid.Add(1)
		b.log.Printf("refused proof for template at height %d: %v", t.Height, err)
		return false, nil
	}

	b.submitMu.Lock()
	defer b.submitMu.Unlock()
	if b.lastSubmitTip == st.Height {
		// Each block takes one AuxPoW submission, and this bridge has
		// already sent one since the last block.
		b.m.stale.Add(1)
		b.log.Printf("stale: a submission is already on its way into block %d", next)
		return false, nil
	}
	txHash, err := b.chain.Submit(ctx, d)
	if err != nil {
		b.m.failed.Add(1)
		b.log.Printf("broadcast failed: %v", err)
		return false, nil
	}
	b.lastSubmitTip = st.Height
	b.m.submitted.Add(1)
	b.log.Printf("submitted %s: template height %d, reward %s", txHash, t.Height, d.RewardAddress)
	go b.track(txHash, d.RewardAddress)
	return true, nil
}

// track follows a broadcast submission until it lands in a block.
func (b *bridge) track(txHash, reward string) {
	ctx, cancel := context.WithTimeout(context.Background(), b.trackFor)
	defer cancel()
	tick := time.NewTicker(b.pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			b.log.Printf("%s: not in a block after %s", txHash, b.trackFor)
			return
		case <-tick.C:
		}
		res, found, err := b.chain.TxResult(ctx, txHash)
		if err != nil || !found {
			continue
		}
		switch {
		case res.Code == 0:
			b.m.accepted.Add(1)
			b.log.Printf("%s: accepted at height %d, paid to %s", txHash, res.Height, reward)
		case res.Codespace == powtypes.ErrTooManySubmissionsThisBlock.Codespace() &&
			res.Code == powtypes.ErrTooManySubmissionsThisBlock.ABCICode():
			b.m.stale.Add(1)
			b.log.Printf("%s: stale, another AuxPoW submission took block %d's slot", txHash, res.Height)
		default:
			b.m.rejected.Add(1)
			b.log.Printf("%s: refused at height %d: code %d (%s): %s", txHash, res.Height, res.Code, res.Codespace, res.Log)
		}
		return
	}
}

// getAuxBlock is the legacy call: without arguments it hands out work paid
// to --reward-address, and with a hash and proof it submits.
func (b *bridge) getAuxBlock(ctx context.Context, params []string) (any, error) {
	switch len(params) {
	case 0:
		if b.defaultReward == nil {
			return nil, &rpcError{rpcMiscError, "getauxblock without arguments needs auxpowd --reward-address; use createauxblock <address>"}
		}
		return b.createAuxBlock(ctx, b.defaultReward.String())
	case 2:
		return b.submitAuxBlock(ctx, params[0], params[1])
	default:
		return nil, &rpcError{rpcInvalidParams, "getauxblock takes no arguments, or a hash and an auxpow"}
	}
}

// validateAddress answers as bitcoind does. ismine is true for the address
// getauxblock pays (--reward-address): the pool's own.
func (b *bridge) validateAddress(address string) map[string]any {
	addr, err := sdk.AccAddressFromBech32(strings.TrimSpace(address))
	if err != nil {
		return map[string]any{"isvalid": false}
	}
	return map[string]any{
		"isvalid": true,
		"address": addr.String(),
		"ismine":  b.defaultReward != nil && addr.Equals(b.defaultReward),
	}
}

var errNoMethod = errors.New("method not found")

// call dispatches one JSON-RPC method.
func (b *bridge) call(ctx context.Context, method string, params []string) (any, error) {
	need := func(n int) error {
		if len(params) != n {
			return &rpcError{rpcInvalidParams, fmt.Sprintf("%s takes %d argument(s)", method, n)}
		}
		return nil
	}
	switch method {
	case "createauxblock":
		if err := need(1); err != nil {
			return nil, err
		}
		return b.createAuxBlock(ctx, params[0])
	case "submitauxblock":
		if err := need(2); err != nil {
			return nil, err
		}
		return b.submitAuxBlock(ctx, params[0], params[1])
	case "getauxblock":
		return b.getAuxBlock(ctx, params)
	case "getblockcount":
		st, err := b.chain.State(ctx)
		if err != nil {
			return nil, &rpcError{rpcMiscError, err.Error()}
		}
		return st.Height, nil

	// Calls pool software makes on every daemon it mines, merged-mined ones
	// included, answered from the Aether chain in bitcoind's shapes.
	case "getblocktemplate":
		st, err := b.state(ctx)
		if err != nil {
			return nil, err
		}
		return newBlockTemplate(st, time.Now().Unix()), nil
	case "getdifficulty":
		st, err := b.chain.State(ctx)
		if err != nil {
			return nil, &rpcError{rpcMiscError, err.Error()}
		}
		return float64(st.AuxDifficulty), nil
	case "getmininginfo":
		st, err := b.chain.State(ctx)
		if err != nil {
			return nil, &rpcError{rpcMiscError, err.Error()}
		}
		return map[string]any{
			"blocks":     st.Height,
			"difficulty": float64(st.AuxDifficulty),
			"chain":      st.ChainID,
		}, nil
	case "validateaddress":
		if err := need(1); err != nil {
			return nil, err
		}
		return b.validateAddress(params[0]), nil
	default:
		return nil, errNoMethod
	}
}
