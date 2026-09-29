// Package escrow lets one account lock money for another until it's
// settled: the payer (or an arbiter) releases it to the payee, the
// payee (or the arbiter) refunds it to the payer, or, at the deadline,
// EndBlock does whichever of the two the payer chose when creating it.
// It's what lets agents hire each other for work that takes longer
// than one request: the worker sees the money is committed before it
// starts, and the buyer's money moves only on delivery -- or on the
// arbiter's word, or at the agreed deadline.
package escrow

import (
	"encoding/binary"
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"
)

const (
	ModuleName = "escrow"
	StoreKey   = ModuleName
)

// Limits. Fees are zero on this chain, so what an open escrow costs its
// payer is the money it locks and one of a bounded number of slots.
const (
	MinDuration         = time.Minute
	MaxDuration         = 365 * 24 * time.Hour
	MaxTermsLength      = 256
	MaxDenoms           = 10
	MaxOpenPerPayer     = 200
	MaxExpiriesPerBlock = 100
	MaxPageSize         = 100
)

// Event types and attributes. Settling by deadline emits the same
// released / refunded events as settling by hand, with by = "expiry", so
// one query finds every way money left escrow.
const (
	EventTypeCreated  = "escrow_created"
	EventTypeReleased = "escrow_released"
	EventTypeRefunded = "escrow_refunded"

	AttributeID        = "id"
	AttributePayer     = "payer"
	AttributePayee     = "payee"
	AttributeArbiter   = "arbiter"
	AttributeAmount    = "amount"
	AttributeExpiresAt = "expires_at"
	AttributeOnExpiry  = "on_expiry"
	AttributeTerms     = "terms"
	AttributeBy        = "by"

	ByExpiry = "expiry"
)

var (
	// next_id -> the id the next escrow gets (big-endian uint64).
	KeyNextID = []byte{0x01}
	// 0x02 | id -> a marshaled Escrow.
	KeyEscrowPrefix = []byte{0x02}
	// 0x03 | expires_at | id -> nothing: the deadline queue.
	KeyExpiryPrefix = []byte{0x03}
	// 0x04 | len-prefixed address | id -> nothing: escrows an address
	// is payer, payee or arbiter of.
	KeyPartyPrefix = []byte{0x04}
	// 0x05 | len-prefixed payer -> open escrows it has (big-endian uint64).
	KeyOpenCountPrefix = []byte{0x05}
)

func u64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func escrowKey(id uint64) []byte { return join(KeyEscrowPrefix, u64(id)) }

// expiryKey orders the queue by deadline; deadlines are validated to be
// after the block time, so never negative.
func expiryKey(expiresAt int64, id uint64) []byte {
	return join(KeyExpiryPrefix, u64(uint64(expiresAt)), u64(id))
}

func partyPrefix(addr sdk.AccAddress) []byte {
	return join(KeyPartyPrefix, address.MustLengthPrefix(addr))
}

func partyKey(addr sdk.AccAddress, id uint64) []byte { return join(partyPrefix(addr), u64(id)) }

func openCountKey(payer sdk.AccAddress) []byte {
	return join(KeyOpenCountPrefix, address.MustLengthPrefix(payer))
}

// parties is everyone an escrow's index lists it under.
func (e Escrow) parties() []string {
	out := []string{e.Payer, e.Payee}
	if e.Arbiter != "" {
		out = append(out, e.Arbiter)
	}
	return out
}

// validate checks what doesn't depend on the chain's state: addresses,
// amount, outcome, terms. The deadline is checked against block time by
// the message server.
func (e Escrow) validate() error {
	payer, err := sdk.AccAddressFromBech32(e.Payer)
	if err != nil {
		return fmt.Errorf("payer: %w", err)
	}
	payee, err := sdk.AccAddressFromBech32(e.Payee)
	if err != nil {
		return fmt.Errorf("payee: %w", err)
	}
	if payer.Equals(payee) {
		return fmt.Errorf("payer and payee are the same account")
	}
	if e.Arbiter != "" {
		arbiter, err := sdk.AccAddressFromBech32(e.Arbiter)
		if err != nil {
			return fmt.Errorf("arbiter: %w", err)
		}
		if arbiter.Equals(payer) || arbiter.Equals(payee) {
			return fmt.Errorf("the arbiter must be a third account, not the payer or payee")
		}
	}
	if err := e.Amount.Validate(); err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if e.Amount.IsZero() {
		return fmt.Errorf("amount is empty")
	}
	if len(e.Amount) > MaxDenoms {
		return fmt.Errorf("amount has %d denoms; at most %d", len(e.Amount), MaxDenoms)
	}
	if e.OnExpiry != ON_EXPIRY_REFUND && e.OnExpiry != ON_EXPIRY_RELEASE {
		return fmt.Errorf("on_expiry must be ON_EXPIRY_REFUND or ON_EXPIRY_RELEASE")
	}
	if len(e.Terms) > MaxTermsLength {
		return fmt.Errorf("terms are %d bytes; at most %d", len(e.Terms), MaxTermsLength)
	}
	if e.ExpiresAt <= 0 {
		return fmt.Errorf("expires_at must be a unix time in seconds")
	}
	return nil
}

func DefaultGenesisState() GenesisState {
	return GenesisState{NextId: 1}
}

func (g GenesisState) Validate() error {
	if g.NextId == 0 {
		return fmt.Errorf("next_id must be at least 1")
	}
	seen := map[uint64]bool{}
	open := map[string]int{}
	for _, e := range g.Escrows {
		if e.Id == 0 || e.Id >= g.NextId {
			return fmt.Errorf("escrow id %d is outside 1..%d", e.Id, g.NextId-1)
		}
		if seen[e.Id] {
			return fmt.Errorf("escrow id %d appears twice", e.Id)
		}
		seen[e.Id] = true
		if err := e.validate(); err != nil {
			return fmt.Errorf("escrow %d: %w", e.Id, err)
		}
		open[e.Payer]++
		if open[e.Payer] > MaxOpenPerPayer {
			return fmt.Errorf("payer %s has more than %d open escrows", e.Payer, MaxOpenPerPayer)
		}
	}
	return nil
}
