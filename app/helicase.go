package app

import (
	"context"
	"fmt"
	"sync/atomic"

	sdkerrors "cosmossdk.io/errors"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/mempool"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
)

// Helicase lets the block proposer bring IBC packets in from other chains
// itself, so no relayer ever has to sign a transaction on Aether.
//
// Every message that moves a packet onto a chain -- a client update, a
// packet, an acknowledgement, a timeout -- carries a proof that the light
// client checks against the other chain's validator signatures. Who
// submitted it adds nothing to its security; on other chains the
// relayer's signature exists only to pay fees and stop spam. Aether can't
// take stock relayers' signatures (PostQuantumDecorator accepts only
// ML-DSA-44), so instead a relay transaction here carries no signature at
// all, and only the block proposer can include one:
//
//   - A relay transaction is one with no signatures whose messages are
//     all MsgUpdateClient, MsgRecvPacket, MsgAcknowledgement, MsgTimeout
//     or MsgTimeoutOnClose, each naming HelicaseAddress as its signer,
//     with memo "helicase", no fee, no timeout height and no extension
//     options.
//   - CheckTx and simulation refuse it, so it never enters a mempool;
//     it reaches a block only through the proposer's PrepareProposal.
//   - Every validator checks each one in ProcessProposal (shape, and at
//     most helicaseMaxTxsPerBlock of them) and then executes it like any
//     other transaction: the light client verifies its proofs, and a
//     message that fails verification fails its transaction, nothing
//     more. A dishonest proposer can leave packets out, and the next
//     proposer brings them in; it can't forge one.
//
// Everything this path can do is limited to what the other chain's
// validators already signed. It never touches Aether accounts, balances
// or signatures, and every transaction a person or agent signs still has
// to be ML-DSA-44.
//
// PLACEHOLDER: replace with a height agreed with the operators and
// confirmed against the live tip immediately before the cutover. No store
// is added, so there is no halt at this height; nodes simply start
// accepting relay transactions from it, which is why every node has to be
// on a binary carrying it before then.
const HelicaseActivationHeight int64 = 1_000_000

// helicaseActivationHeight is what the handlers read, so tests can cross
// the activation at a small height.
var helicaseActivationHeight = HelicaseActivationHeight

// HelicaseModuleName names the address relay messages are attributed to.
// It's a module address: no key exists for it, and nothing holds funds
// there.
const HelicaseModuleName = "helicase"

// HelicaseMemo marks a relay transaction, so explorers and people can
// tell it from a transaction someone signed.
const HelicaseMemo = "helicase"

// HelicaseTxGas is the gas limit the proposer gives each relay
// transaction, and helicaseMaxGas the most one may ask for. A client
// update verifying a whole validator set is the most expensive of them.
const (
	HelicaseTxGas  uint64 = 2_000_000
	helicaseMaxGas uint64 = 10_000_000
)

// helicaseMaxTxsPerBlock bounds the proposer's relay transactions per
// block, so a proposer can't fill blocks with proof verification.
const helicaseMaxTxsPerBlock = 100

// HelicaseAddress is the signer every relay message names.
var HelicaseAddress = authtypes.NewModuleAddress(HelicaseModuleName)

var (
	ErrHelicaseNotFromProposer = sdkerrors.Register("app", 2, "a relay transaction without signatures can only be included by the block proposer")
	ErrHelicaseInvalid         = sdkerrors.Register("app", 3, "invalid relay transaction")
)

// HelicaseSource supplies the relay transactions a proposer puts at the
// front of its block. The node's Helicase worker (package helicase)
// implements it; a node without one configured proposes none.
type HelicaseSource interface {
	// RelayTxs returns encoded relay transactions for the block at height,
	// in the order they must execute (each client update before the
	// packets proven against it).
	RelayTxs(height int64) [][]byte
}

type helicaseSourceHolder struct{ HelicaseSource }

// helicaseState holds the source PrepareProposal reads, which the worker
// sets from another goroutine.
type helicaseState struct {
	source atomic.Pointer[helicaseSourceHolder]
}

// SetHelicaseSource attaches the worker whose relay transactions this
// node proposes. nil detaches it.
func (app *App) SetHelicaseSource(s HelicaseSource) {
	if s == nil {
		app.helicase.source.Store(nil)
		return
	}
	app.helicase.source.Store(&helicaseSourceHolder{s})
}

func helicaseActive(height int64) bool {
	return height >= helicaseActivationHeight
}

// isRelayMsg reports whether msg is one Helicase carries, and returns
// the signer it names.
func isRelayMsg(msg sdk.Msg) (string, bool) {
	switch m := msg.(type) {
	case *clienttypes.MsgUpdateClient:
		return m.Signer, true
	case *channeltypes.MsgRecvPacket:
		return m.Signer, true
	case *channeltypes.MsgAcknowledgement:
		return m.Signer, true
	case *channeltypes.MsgTimeout:
		return m.Signer, true
	case *channeltypes.MsgTimeoutOnClose:
		return m.Signer, true
	}
	return "", false
}

// isHelicaseTx reports whether tx claims to be a relay transaction: no
// signatures, and at least one relay message. validateHelicaseTx then
// decides whether it is a valid one. Before the activation height such a
// transaction goes through the ordinary ante handler, which refuses it
// for having no signatures, exactly as it always has.
func isHelicaseTx(tx sdk.Tx) bool {
	sigTx, ok := tx.(signing.SigVerifiableTx)
	if !ok {
		return false
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil || len(sigs) != 0 {
		return false
	}
	for _, msg := range tx.GetMsgs() {
		if _, ok := isRelayMsg(msg); ok {
			return true
		}
	}
	return false
}

// validateHelicaseTx checks everything about a relay transaction except
// its proofs, which the light client checks when it executes.
func validateHelicaseTx(tx sdk.Tx) error {
	msgs := tx.GetMsgs()
	if len(msgs) == 0 {
		return sdkerrors.Wrap(ErrHelicaseInvalid, "no messages")
	}
	want := HelicaseAddress.String()
	for i, msg := range msgs {
		signer, ok := isRelayMsg(msg)
		if !ok {
			return sdkerrors.Wrapf(ErrHelicaseInvalid, "message %d is %s; only client updates, packets, acknowledgements and timeouts can be relayed", i, sdk.MsgTypeURL(msg))
		}
		if signer != want {
			return sdkerrors.Wrapf(ErrHelicaseInvalid, "message %d names signer %s, not %s", i, signer, want)
		}
	}
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return sdkerrors.Wrap(ErrHelicaseInvalid, "not a fee transaction")
	}
	if !feeTx.GetFee().IsZero() || len(feeTx.FeeGranter()) != 0 {
		return sdkerrors.Wrap(ErrHelicaseInvalid, "a relay transaction pays no fee")
	}
	if gas := feeTx.GetGas(); gas == 0 || gas > helicaseMaxGas {
		return sdkerrors.Wrapf(ErrHelicaseInvalid, "gas limit %d outside 1..%d", gas, helicaseMaxGas)
	}
	if memoTx, ok := tx.(sdk.TxWithMemo); !ok || memoTx.GetMemo() != HelicaseMemo {
		return sdkerrors.Wrapf(ErrHelicaseInvalid, "memo must be %q", HelicaseMemo)
	}
	if t, ok := tx.(sdk.TxWithTimeoutHeight); ok && t.GetTimeoutHeight() != 0 {
		return sdkerrors.Wrap(ErrHelicaseInvalid, "a relay transaction has no timeout height")
	}
	if ext, ok := tx.(authante.HasExtensionOptionsTx); ok && (len(ext.GetExtensionOptions()) != 0 || len(ext.GetNonCriticalExtensionOptions()) != 0) {
		return sdkerrors.Wrap(ErrHelicaseInvalid, "a relay transaction has no extension options")
	}
	return nil
}

// setRelaySigner makes msg name signer, if it's a relay message.
func setRelaySigner(msg sdk.Msg, signer string) bool {
	switch m := msg.(type) {
	case *clienttypes.MsgUpdateClient:
		m.Signer = signer
	case *channeltypes.MsgRecvPacket:
		m.Signer = signer
	case *channeltypes.MsgAcknowledgement:
		m.Signer = signer
	case *channeltypes.MsgTimeout:
		m.Signer = signer
	case *channeltypes.MsgTimeoutOnClose:
		m.Signer = signer
	default:
		return false
	}
	return true
}

// EncodeHelicaseTx builds the relay transaction carrying msgs, changing
// each one's signer to HelicaseAddress.
func EncodeHelicaseTx(txConfig client.TxConfig, msgs ...sdk.Msg) ([]byte, error) {
	signer := HelicaseAddress.String()
	for i, msg := range msgs {
		if !setRelaySigner(msg, signer) {
			return nil, fmt.Errorf("message %d is %s, which Helicase doesn't relay", i, sdk.MsgTypeURL(msg))
		}
	}
	b := txConfig.NewTxBuilder()
	if err := b.SetMsgs(msgs...); err != nil {
		return nil, err
	}
	b.SetMemo(HelicaseMemo)
	b.SetGasLimit(HelicaseTxGas)
	return txConfig.TxEncoder()(b.GetTx())
}

// helicaseAnteHandle replaces the whole ante chain for a relay
// transaction: there is no signature to verify, no fee to take and no
// account sequence to bump. It only refuses one outside a block, and
// sets the gas meter.
func helicaseAnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
	switch ctx.ExecMode() {
	case sdk.ExecModePrepareProposal, sdk.ExecModeProcessProposal, sdk.ExecModeFinalize:
	default:
		return ctx, ErrHelicaseNotFromProposer
	}
	if simulate {
		return ctx, ErrHelicaseNotFromProposer
	}
	if err := validateHelicaseTx(tx); err != nil {
		return ctx, err
	}
	gas := tx.(sdk.FeeTx).GetGas()
	return ctx.WithGasMeter(storetypes.NewGasMeter(gas)), nil
}

// helicasePrepareProposal puts the worker's relay transactions at the
// front of the block, then fills the rest as next would. A relay
// transaction that doesn't decode or isn't valid is dropped here rather
// than get the whole proposal rejected.
func (app *App) helicasePrepareProposal(next sdk.PrepareProposalHandler) sdk.PrepareProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
		holder := app.helicase.source.Load()
		if !helicaseActive(req.Height) || holder == nil {
			return next(ctx, req)
		}

		// Relay transactions get at most half the block's bytes, and half
		// its gas if it has a gas limit (this chain's is -1: none).
		budget := req.MaxTxBytes / 2
		var maxGas int64
		if b := ctx.ConsensusParams().Block; b != nil && b.MaxGas > 0 {
			maxGas = b.MaxGas
		}
		var relay [][]byte
		var used, gas int64
		for _, bz := range holder.RelayTxs(req.Height) {
			if len(relay) == helicaseMaxTxsPerBlock || used+int64(len(bz)) > budget {
				break
			}
			tx, err := app.TxDecode(bz)
			if err == nil && isHelicaseTx(tx) {
				err = validateHelicaseTx(tx)
			} else if err == nil {
				err = ErrHelicaseInvalid
			}
			if err != nil {
				app.Logger().Error("helicase: dropping an invalid relay transaction from the proposal", "err", err)
				continue
			}
			txGas := int64(tx.(sdk.FeeTx).GetGas())
			if maxGas > 0 && gas+txGas > maxGas/2 {
				break
			}
			relay = append(relay, bz)
			used += int64(len(bz))
			gas += txGas
		}
		if len(relay) == 0 {
			return next(ctx, req)
		}

		rest := *req
		rest.MaxTxBytes -= used
		if maxGas > 0 {
			cp := ctx.ConsensusParams()
			block := *cp.Block
			block.MaxGas -= gas
			cp.Block = &block
			ctx = ctx.WithConsensusParams(cp)
		}
		resp, err := next(ctx, &rest)
		if err != nil {
			return nil, err
		}
		resp.Txs = append(relay, resp.Txs...)
		return resp, nil
	}
}

// helicaseProcessProposal rejects a proposal carrying a malformed relay
// transaction, or more than helicaseMaxTxsPerBlock of them, before
// next's checks. Their proofs are checked when they execute.
func (app *App) helicaseProcessProposal(next sdk.ProcessProposalHandler) sdk.ProcessProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
		if helicaseActive(req.Height) {
			n := 0
			for _, bz := range req.Txs {
				tx, err := app.TxDecode(bz)
				if err != nil || !isHelicaseTx(tx) {
					continue // not a relay transaction: next's business
				}
				n++
				if n > helicaseMaxTxsPerBlock {
					app.Logger().Error("helicase: rejecting a proposal with too many relay transactions", "height", req.Height)
					return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
				}
				if err := validateHelicaseTx(tx); err != nil {
					app.Logger().Error("helicase: rejecting a proposal with an invalid relay transaction", "height", req.Height, "err", err)
					return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
				}
			}
		}
		return next(ctx, req)
	}
}

// helicaseMempool wraps an app-side mempool (app.toml mempool.max-txs >=
// 0) for relay transactions. After a transaction executes, BaseApp
// removes it from the mempool, and the SDK's mempools answer an unsigned
// transaction with an error rather than "not found", which would fail
// the relay transaction on nodes with a mempool and not on nodes
// without: different app hashes. A relay transaction is never in a
// mempool (CheckTx refuses it), so Remove says exactly that.
type helicaseMempool struct {
	mempool.Mempool
}

func (m helicaseMempool) Remove(tx sdk.Tx) error {
	if isHelicaseTx(tx) {
		return mempool.ErrTxNotFound
	}
	return m.Mempool.Remove(tx)
}

// SelectBy keeps the wrapped mempool's own SelectBy, which
// PrepareProposal uses when there is one.
func (m helicaseMempool) SelectBy(ctx context.Context, txs [][]byte, callback func(sdk.Tx) bool) {
	mempool.SelectBy(ctx, m.Mempool, txs, callback)
}

// wrapHelicaseMempool installs helicaseMempool around bApp's mempool, if
// it has one.
func wrapHelicaseMempool(bApp *baseapp.BaseApp) {
	mp := bApp.Mempool()
	if mp == nil {
		return
	}
	if _, noop := mp.(mempool.NoOpMempool); noop {
		return
	}
	bApp.SetMempool(helicaseMempool{mp})
}
