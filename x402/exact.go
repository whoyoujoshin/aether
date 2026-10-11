package x402

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/math"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// maxTxBytes bounds the transaction a payload may carry: a one-message
// ML-DSA-44 send is about 4 KB (most of it the 1312-byte key and the
// 2420-byte signature).
const maxTxBytes = 16 << 10

// Chain is what the exact scheme needs from a node.
type Chain interface {
	// Account is the account's number and next sequence; an error if it
	// doesn't exist.
	Account(ctx context.Context, address string) (number, sequence uint64, err error)
	// LatestHeight is the newest block's height.
	LatestHeight(ctx context.Context) (int64, error)
	// Simulate runs the transaction without committing it and returns the
	// gas it used.
	Simulate(ctx context.Context, txBytes []byte) (gasUsed uint64, err error)
	// Broadcast submits it (sync: checked, not yet in a block).
	Broadcast(ctx context.Context, txBytes []byte) (hash string, code uint32, log string, err error)
	// TxResult is the result of a transaction in a block; found is false
	// until it's in one.
	TxResult(ctx context.Context, hash string) (found bool, code uint32, log string, err error)
}

// Facilitator verifies and settles exact payments on one Aether chain.
// It holds no key.
type Facilitator struct {
	ChainID string
	Chain   Chain
	// PollInterval and SettleTimeout bound the wait for a settled
	// transaction to land in a block (defaults 1s and 90s; a requirement's
	// maxTimeoutSeconds, when larger, wins).
	PollInterval  time.Duration
	SettleTimeout time.Duration

	mu      sync.Mutex
	settled map[string]bool // tx hashes settled or being settled
}

// NewFacilitator is a Facilitator for chainID.
func NewFacilitator(chainID string, chain Chain) *Facilitator {
	return &Facilitator{ChainID: chainID, Chain: chain}
}

// Supported is the facilitator's GET /supported answer.
func (f *Facilitator) Supported() SupportedResponse {
	return SupportedResponse{
		Kinds: []SupportedKind{{
			X402Version: Version, Scheme: SchemeExact, Network: Network(f.ChainID),
			Extra: map[string]any{"feeDenom": "uaeth", "feePayer": "payer"},
		}},
		Extensions: []string{},
		Signers:    map[string][]string{},
	}
}

// checked is a payment that passed every check but the chain's.
type checked struct {
	payer   string
	txBytes []byte
	hash    string
}

// Verify checks a payment without settling it.
func (f *Facilitator) Verify(ctx context.Context, p PaymentPayload, req PaymentRequirements) VerifyResponse {
	c, reason := f.check(ctx, p, req)
	if reason != "" {
		return VerifyResponse{InvalidReason: reason, Payer: c.payer}
	}
	return VerifyResponse{IsValid: true, Payer: c.payer}
}

// Settle verifies a payment again and, if it holds, broadcasts it and
// waits for it to land in a block. A transaction is settled at most once
// by this facilitator: a second Settle with the same bytes fails, so one
// payment can't buy two requests.
func (f *Facilitator) Settle(ctx context.Context, p PaymentPayload, req PaymentRequirements) SettleResponse {
	network := Network(f.ChainID)
	fail := func(payer, reason, hash string) SettleResponse {
		return SettleResponse{ErrorReason: reason, Payer: payer, Transaction: hash, Network: network}
	}
	c, reason := f.check(ctx, p, req)
	if reason != "" {
		return fail(c.payer, reason, "")
	}
	f.mu.Lock()
	if f.settled == nil {
		f.settled = map[string]bool{}
	}
	if f.settled[c.hash] {
		f.mu.Unlock()
		return fail(c.payer, ReasonAlreadySettled, c.hash)
	}
	f.settled[c.hash] = true
	f.mu.Unlock()

	hash, code, log, err := f.Chain.Broadcast(ctx, c.txBytes)
	if err != nil {
		// Unknown whether it reached the mempool: keep the hash marked, so
		// the same bytes aren't accepted again here.
		return fail(c.payer, ReasonUnexpectedSettle, c.hash)
	}
	if hash == "" {
		hash = c.hash
	}
	if code != 0 {
		f.unmark(c.hash) // refused at CheckTx: nothing happened
		return fail(c.payer, rejectReason(log), hash)
	}

	timeout := f.SettleTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if t := time.Duration(req.MaxTimeoutSeconds) * time.Second; t > timeout {
		timeout = t
	}
	poll := f.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		found, code, log, err := f.Chain.TxResult(ctx, hash)
		if err == nil && found {
			if code != 0 {
				return fail(c.payer, rejectReason(log), hash)
			}
			return SettleResponse{Success: true, Payer: c.payer, Transaction: hash, Network: network, Amount: req.Amount}
		}
		if time.Now().After(deadline) {
			return fail(c.payer, ReasonTransactionState, hash)
		}
		select {
		case <-ctx.Done():
			return fail(c.payer, ReasonUnexpectedSettle, hash)
		case <-time.After(poll):
		}
	}
}

func (f *Facilitator) unmark(hash string) {
	f.mu.Lock()
	delete(f.settled, hash)
	f.mu.Unlock()
}

func rejectReason(log string) string {
	if strings.Contains(log, "insufficient funds") {
		return ReasonInsufficientFunds
	}
	if strings.Contains(log, "insufficient fee") {
		return ReasonFee // below the node's minimum gas price
	}
	if strings.Contains(log, "out of gas") {
		return ReasonGas
	}
	if strings.Contains(log, "account sequence mismatch") || strings.Contains(log, "incorrect account sequence") {
		return ReasonSequence
	}
	return ReasonSettlementRejected
}

// check runs every check on a payment. It returns the payer whenever the
// transaction got far enough to name one.
func (f *Facilitator) check(ctx context.Context, p PaymentPayload, req PaymentRequirements) (checked, string) {
	var c checked
	network := Network(f.ChainID)
	if p.X402Version != Version {
		return c, ReasonVersion
	}
	if req.Scheme != SchemeExact || p.Accepted.Scheme != SchemeExact {
		return c, ReasonScheme
	}
	if req.Network != network || p.Accepted.Network != network {
		return c, ReasonNetwork
	}
	amount, ok := math.NewIntFromString(req.Amount)
	if !ok || !amount.IsPositive() || sdk.ValidateDenom(req.Asset) != nil {
		return c, ReasonRequirements
	}
	if _, err := sdk.AccAddressFromBech32(req.PayTo); err != nil {
		return c, ReasonRequirements
	}
	// The client must have paid what this resource asks, not what an
	// earlier or different 402 offered.
	if p.Accepted.Amount != req.Amount || p.Accepted.Asset != req.Asset || p.Accepted.PayTo != req.PayTo {
		return c, ReasonAcceptedMismatch
	}

	var ep ExactPayload
	if err := json.Unmarshal(p.Payload, &ep); err != nil || ep.Transaction == "" {
		return c, ReasonPayload
	}
	txBytes, err := base64.StdEncoding.DecodeString(ep.Transaction)
	if err != nil || len(txBytes) == 0 || len(txBytes) > maxTxBytes {
		return c, ReasonPayload
	}
	c.txBytes = txBytes
	c.hash = TxHash(txBytes)

	enc := encoding()
	// The chain's own decoder: anything it would refuse (unknown fields,
	// bad encoding) is refused here too.
	if _, err := enc.TxConfig.TxDecoder()(txBytes); err != nil {
		return c, ReasonTransaction
	}
	var raw txtypes.TxRaw
	var body txtypes.TxBody
	var auth txtypes.AuthInfo
	if raw.Unmarshal(txBytes) != nil || body.Unmarshal(raw.BodyBytes) != nil || auth.Unmarshal(raw.AuthInfoBytes) != nil {
		return c, ReasonTransaction
	}

	// Exactly one message: a bank send of exactly the amount to payTo.
	if len(body.Messages) != 1 || len(body.ExtensionOptions) != 0 || len(body.NonCriticalExtensionOptions) != 0 {
		return c, ReasonMessage
	}
	var msg sdk.Msg
	if err := enc.InterfaceRegistry.UnpackAny(body.Messages[0], &msg); err != nil {
		return c, ReasonMessage
	}
	send, ok := msg.(*banktypes.MsgSend)
	if !ok {
		return c, ReasonMessage
	}
	c.payer = send.FromAddress
	if send.ToAddress != req.PayTo {
		return c, ReasonRecipientMismatch
	}
	if !send.Amount.Equal(sdk.NewCoins(sdk.NewCoin(req.Asset, amount))) {
		return c, ReasonAmountMismatch
	}
	payer, err := sdk.AccAddressFromBech32(send.FromAddress)
	if err != nil {
		return c, ReasonMessage
	}

	// One signer, the payer's own ML-DSA key, in direct mode.
	if len(auth.SignerInfos) != 1 || len(raw.Signatures) != 1 || auth.SignerInfos[0].PublicKey == nil {
		return c, ReasonSigner
	}
	si := auth.SignerInfos[0]
	if si.ModeInfo == nil || si.ModeInfo.GetSingle() == nil || si.ModeInfo.GetSingle().Mode != signingtypes.SignMode_SIGN_MODE_DIRECT {
		return c, ReasonSigner
	}
	var pub cryptotypes.PubKey
	if err := enc.InterfaceRegistry.UnpackAny(si.PublicKey, &pub); err != nil {
		return c, ReasonSigner
	}
	if _, ok := pub.(*mldsa.PubKey); !ok || !sdk.AccAddress(pub.Address()).Equals(payer) {
		return c, ReasonSigner
	}
	// The payer pays the fee itself.
	if auth.Fee == nil || auth.Fee.Granter != "" || (auth.Fee.Payer != "" && auth.Fee.Payer != send.FromAddress) {
		return c, ReasonFee
	}

	if body.TimeoutHeight != 0 {
		h, err := f.Chain.LatestHeight(ctx)
		if err != nil {
			return c, ReasonUnexpectedVerify
		}
		if body.TimeoutHeight <= uint64(h) {
			return c, ReasonExpired
		}
	}

	// The signature, over the same bytes the chain checks, with the
	// account number from the chain; and a sequence not yet used.
	number, sequence, err := f.Chain.Account(ctx, send.FromAddress)
	if err != nil {
		return c, ReasonInsufficientFunds // no account: it has never held anything
	}
	if si.Sequence != sequence {
		return c, ReasonSequence
	}
	doc := txtypes.SignDoc{BodyBytes: raw.BodyBytes, AuthInfoBytes: raw.AuthInfoBytes, ChainId: f.ChainID, AccountNumber: number}
	signBytes, err := doc.Marshal()
	if err != nil {
		return c, ReasonUnexpectedVerify
	}
	if !pub.VerifySignature(signBytes, raw.Signatures[0]) {
		return c, ReasonSignature
	}

	// Last, the chain itself: balance for the amount and the fee. The node
	// simulates without a gas limit, so the limit is checked here; a tx
	// short of gas would land in a block, charge its fee and pay nothing.
	gasUsed, err := f.Chain.Simulate(ctx, txBytes)
	if err != nil {
		return c, rejectReason(err.Error())
	}
	if gasUsed > auth.Fee.GasLimit {
		return c, ReasonGas
	}
	return c, ""
}

// TxHash is a transaction's hash as the chain names it (uppercase hex SHA-256).
func TxHash(txBytes []byte) string {
	return fmt.Sprintf("%X", sha256.Sum256(txBytes))
}

var (
	encOnce sync.Once
	encCfg  app.EncodingConfig
)

func encoding() app.EncodingConfig {
	encOnce.Do(func() { encCfg = app.MakeEncodingConfig() })
	return encCfg
}
