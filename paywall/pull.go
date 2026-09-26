package paywall

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// The aether-pull scheme: the buyer's money stays in its own account
// until it's owed.
//
//  1. The buyer grants the seller's collector account (Grantee) a send
//     allowance on chain (x/authz SendAuthorization): a spend limit, an
//     expiry, and payTo as the only allowed recipient. The chain
//     enforces all three, and the buyer can revoke it at any time.
//  2. Each request is signed like an aether-prepaid one (PullSigningMessage)
//     and served at once: the seller checks the allowance covers what the
//     buyer owes plus this request, and records the charge.
//  3. The seller collects what each buyer owes in batches, with one
//     MsgExec moving it from the buyer to payTo under the allowance.
//
// The seller's risk is what a buyer owes between collections, capped by
// Credit: a buyer who revokes the allowance before a collection lands
// keeps up to that much. A failed collection is remembered, and that
// buyer is refused until an allowance covering it is granted -- the next
// collection then takes it too.

const (
	pullSigningDomain = "aether-pull-request/v1\n"
	// defaultCreditRequests is Credit, in requests, when unset.
	defaultCreditRequests = 100
	// grantCacheTTL is how long a looked-up allowance is trusted. The
	// cached limit may be stale by collections landed since, but those
	// only ever lower it by what the ledger already counts as owed.
	grantCacheTTL = 15 * time.Second
	// CollectionMemoPrefix + the collection ID is a collection's memo.
	CollectionMemoPrefix = "x402-pull:"
)

// PullConfig enables the aether-pull scheme.
type PullConfig struct {
	Ledger    PullLedger
	Collector Collector
	// Grantee is the address allowances must be granted to: the account
	// Collector signs with. It needs no funds of its own.
	Grantee string
	// Credit is the most a buyer may owe before it's collected (default
	// 100 requests). It's also the seller's loss if a buyer revokes its
	// allowance just before a collection.
	Credit math.Int
	// CollectEvery is how often owed amounts are collected (default 1m);
	// a buyer reaching half its credit is collected sooner.
	CollectEvery time.Duration
	// Grants looks up the allowance granter gave grantee
	// (wallet.Client.GetSendGrant).
	Grants func(granter, grantee string) (*wallet.SendGrant, error)
}

// Collector moves owed amounts from buyers to the seller.
type Collector interface {
	// SignCollect signs, without broadcasting, a transfer of amount from
	// `from` to the seller under from's allowance, returning the
	// transaction and the sequence it used.
	SignCollect(from string, amount math.Int, memo string) (txBytes []byte, sequence uint64, err error)
	// Submit (re)broadcasts signed bytes and reports where they stand.
	Submit(txBytes []byte, sequence uint64) (PayoutState, error)
}

// PullLedger records what aether-pull buyers owe. Implementations must
// make each method atomic and durable.
type PullLedger interface {
	// Accrue charges amount to account for requestID unless it was
	// charged before (fresh false). ok is false, and nothing changes, if
	// what the account owes would then exceed limit.
	Accrue(account, requestID string, amount, limit math.Int, forgetAfter time.Time) (owed math.Int, ok, fresh bool, err error)
	// Unaccrue reverses an Accrue not yet taken into a collection.
	Unaccrue(account, requestID string, amount math.Int) error
	PullAccount(account string) (PullAccount, error)
	// Collectable lists accounts with anything accrued or a collection
	// open.
	Collectable() ([]string, error)
	// OpenCollection returns account's open collection, or opens one
	// for everything accrued (ok false if there's nothing to collect).
	OpenCollection(account, id string, at time.Time) (c Collection, ok bool, err error)
	SaveCollection(c Collection) error
	// CloseCollection ends account's open collection: collected, or
	// failed, in which case its amount becomes Unpaid.
	CloseCollection(account string, collected bool) error
	// Reinstate moves account's Unpaid back to Accrued, to collect again.
	Reinstate(account string) error
}

// PullAccount is where one buyer stands.
type PullAccount struct {
	Accrued  math.Int // charged, not yet being collected
	InFlight math.Int // in the open collection
	Unpaid   math.Int // from failed collections
}

// Owed is everything the buyer owes.
func (a PullAccount) Owed() math.Int { return a.Accrued.Add(a.InFlight).Add(a.Unpaid) }

// Collection is one transfer of what a buyer owes.
type Collection struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Amount  string `json:"amount"` // uaeth
	Status  string `json:"status"` // reserved, pending, confirmed or failed
	// The signed transfer, kept from before it's broadcast until it's in
	// a block, so it is only ever re-sent, never collected twice.
	TxHash          string    `json:"txHash,omitempty"`
	TxBytes         []byte    `json:"txBytes,omitempty"`
	Sequence        uint64    `json:"sequence,omitempty"`
	At              time.Time `json:"at"`
	SequenceSpentAt time.Time `json:"sequenceSpentAt,omitempty"`
	Log             string    `json:"log,omitempty"` // why it failed
}

// Collection statuses: reserved (not signed yet), pending (signed, maybe
// broadcast), confirmed, failed.
const CollectionFailed = "failed"

// PullPayment is an aether-pull X-PAYMENT payload: a PrepaidPayment
// without a deposit, signed with PullSigningMessage.
type PullPayment = PrepaidPayment

// EncodePullPayment signs f with sign and builds an aether-pull
// X-PAYMENT header value.
func EncodePullPayment(account string, f RequestFields, sign Signer) (string, error) {
	f.DepositTx = ""
	sig, pub, err := sign(PullSigningMessage(f))
	if err != nil {
		return "", err
	}
	return encodePayment(SchemePull, f.Network, PullPayment{
		Account: account, PubKey: base64.StdEncoding.EncodeToString(pub),
		Timestamp: f.Timestamp, RequestID: f.RequestID, MaxPrice: f.MaxPrice.String(),
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
}

// pullState is the Paywall's aether-pull runtime state.
type pullState struct {
	mu     sync.Mutex
	grants map[string]cachedGrant
	wake   chan struct{}
	// collecting serializes collections: they're transactions from one
	// account.
	collecting sync.Mutex
}

type cachedGrant struct {
	g  *wallet.SendGrant
	at time.Time
}

// grant returns buyer's allowance, from cache if fresh (cached true).
// Only allowances found are cached: a buyer who just granted one must
// not be told it has none.
func (p *Paywall) grant(buyer string) (g *wallet.SendGrant, cached bool, err error) {
	now := p.cfg.Now()
	p.pull.mu.Lock()
	c, ok := p.pull.grants[buyer]
	p.pull.mu.Unlock()
	if ok && now.Sub(c.at) < grantCacheTTL {
		return c.g, true, nil
	}
	if g, err = p.cfg.Pull.Grants(buyer, p.cfg.Pull.Grantee); err != nil {
		return nil, false, err
	}
	p.pull.mu.Lock()
	p.pull.grants[buyer] = cachedGrant{g: g, at: now}
	p.pull.mu.Unlock()
	return g, false, nil
}

func (p *Paywall) forgetGrant(buyer string) {
	p.pull.mu.Lock()
	delete(p.pull.grants, buyer)
	p.pull.mu.Unlock()
}

// usable reports why g can't pay this seller, or "" if it can.
func (p *Paywall) unusable(g *wallet.SendGrant) string {
	if g.Expiration != nil && g.Expiration.Before(p.cfg.Now().Add(2*p.cfg.Pull.CollectEvery)) {
		return fmt.Sprintf("your allowance for %s expires too soon to collect under; grant one lasting at least %s", p.cfg.Pull.Grantee, 2*p.cfg.Pull.CollectEvery)
	}
	if len(g.AllowList) > 0 {
		for _, a := range g.AllowList {
			if a == p.cfg.PayTo {
				return ""
			}
		}
		return "your allowance doesn't allow paying " + p.cfg.PayTo
	}
	return ""
}

func (p *Paywall) servePull(w http.ResponseWriter, r *http.Request, raw json.RawMessage, next http.Handler) {
	refuse := func(code, msg, account string) { p.paymentRequiredFor(w, r, code, msg, "", account) }
	req, bad := p.verifySignedIn(pullSigningDomain, r, raw)
	if bad != nil {
		if bad.status != 0 {
			http.Error(w, bad.message, bad.status)
		} else {
			refuse(bad.code, bad.message, "")
		}
		return
	}
	pay, cfg := req.pay, p.cfg.Pull
	if pay.DepositTx != "" {
		refuse(ErrInvalidPayment, "aether-pull requests carry no deposit", "")
		return
	}
	if req.maxPrice.LT(p.cfg.Price) {
		refuse(ErrPriceAboveMax, fmt.Sprintf("the price is %s uaeth; the request allows at most %s", p.cfg.Price, req.maxPrice), pay.Account)
		return
	}
	buyer := pay.Account
	acct, err := cfg.Ledger.PullAccount(buyer)
	if err != nil {
		log.Printf("paywall: reading %s's pull account: %v", buyer, err)
		http.Error(w, "failed to read your account", http.StatusInternalServerError)
		return
	}
	// check reports why grant can't pay for this request, if it can't.
	var grant *wallet.SendGrant
	check := func() (code, msg string) {
		if why := p.unusable(grant); why != "" {
			return ErrNoGrant, why
		}
		covers := func(amount math.Int) bool {
			return grant.Unlimited || grant.SpendLimit.AmountOf(Asset).GTE(amount)
		}
		if !covers(acct.Owed().Add(p.cfg.Price)) {
			if acct.Unpaid.IsPositive() {
				return ErrPullUnpaid, fmt.Sprintf("collecting %s uaeth you owe failed; grant an allowance covering it plus this request (%s uaeth) to continue", acct.Unpaid, acct.Owed().Add(p.cfg.Price))
			}
			return ErrGrantTooLow, fmt.Sprintf("your allowance has %s uaeth left and you owe %s uaeth not yet collected; this request needs %s more", grant.SpendLimit.AmountOf(Asset), acct.Owed(), p.cfg.Price)
		}
		return "", ""
	}
	var cached bool
	for {
		grant, cached, err = p.grant(buyer)
		if errors.Is(err, wallet.ErrGrantNotFound) {
			refuse(ErrNoGrant, fmt.Sprintf("grant %s a send allowance limited to %s first (x/authz SendAuthorization)", cfg.Grantee, p.cfg.PayTo), buyer)
			return
		}
		if err != nil {
			log.Printf("paywall: looking up %s's allowance: %v", buyer, err)
			w.Header().Set("Retry-After", "10")
			http.Error(w, "could not reach the chain to check your allowance; retry", http.StatusServiceUnavailable)
			return
		}
		code, msg := check()
		if code == "" {
			break
		}
		if cached { // it may have been raised since: look again before refusing
			p.forgetGrant(buyer)
			continue
		}
		refuse(code, msg, buyer)
		return
	}
	if acct.Unpaid.IsPositive() {
		// The allowance covers the failed collection too: collect it
		// with the next batch.
		if err := cfg.Ledger.Reinstate(buyer); err != nil {
			log.Printf("paywall: reinstating %s: %v", buyer, err)
			http.Error(w, "failed to update your account", http.StatusInternalServerError)
			return
		}
		acct.Accrued, acct.Unpaid = acct.Accrued.Add(acct.Unpaid), math.ZeroInt()
	}
	// What's being collected counts against the credit until it lands.
	owed, ok, fresh, err := cfg.Ledger.Accrue(buyer, pay.RequestID, p.cfg.Price, cfg.Credit.Sub(acct.InFlight), p.cfg.Now().Add(requestIDRetention))
	if err != nil {
		log.Printf("paywall: charging %s: %v", buyer, err)
		http.Error(w, "failed to record the charge", http.StatusInternalServerError)
		return
	}
	if !fresh {
		refuse(ErrAlreadyRedeemed, "this requestId was already charged and served", buyer)
		return
	}
	if !ok {
		p.wakeCollector()
		w.Header().Set("Retry-After", "10")
		refuse(ErrSettling, fmt.Sprintf("you owe %s uaeth, this service's limit before collecting; it's being collected -- retry shortly", owed.Add(acct.InFlight)), buyer)
		return
	}
	owedAll := owed.Add(acct.InFlight)
	if owed.GTE(cfg.Credit.QuoRaw(2)) {
		p.wakeCollector()
	}
	settle := SettlementResponse{Success: true, Network: p.cfg.Network, Payer: buyer, Owed: owedAll.String()}
	if !grant.Unlimited {
		settle.Allowance = grant.SpendLimit.AmountOf(Asset).Sub(owedAll).String()
	}
	header, _ := EncodeHeader(settle)
	w.Header().Set(HeaderPaymentResponse, header)
	if status := p.serveReceipted(w, r, next, paidRequest{scheme: SchemePull, payer: buyer, payment: pay.RequestID, body: req.body}); status >= 500 {
		if err := cfg.Ledger.Unaccrue(buyer, pay.RequestID, p.cfg.Price); err != nil {
			log.Printf("paywall: uncharging %s for a failed request: %v", buyer, err)
		}
	}
}

func (p *Paywall) wakeCollector() {
	select {
	case p.pull.wake <- struct{}{}:
	default:
	}
}

// RunCollector collects what aether-pull buyers owe until ctx ends. Run
// it once per Paywall offering aether-pull.
func (p *Paywall) RunCollector(ctx context.Context) {
	if p.cfg.Pull == nil {
		return
	}
	t := time.NewTicker(p.cfg.Pull.CollectEvery)
	defer t.Stop()
	for {
		p.CollectAll()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-p.pull.wake:
		}
	}
}

// CollectAll makes one pass over every buyer with something to collect.
func (p *Paywall) CollectAll() {
	accounts, err := p.cfg.Pull.Ledger.Collectable()
	if err != nil {
		log.Printf("paywall: listing pull accounts: %v", err)
		return
	}
	for _, a := range accounts {
		if err := p.collect(a); err != nil {
			log.Printf("paywall: collecting from %s: %v", a, err)
		}
	}
}

// collect moves one step towards collecting what account owes: it
// opens, signs, saves (before broadcast) and sends a collection, or
// follows up on one already sent.
func (p *Paywall) collect(account string) error {
	p.pull.collecting.Lock()
	defer p.pull.collecting.Unlock()
	cfg := p.cfg.Pull
	c, ok, err := cfg.Ledger.OpenCollection(account, newCollectionID(), p.cfg.Now())
	if err != nil || !ok {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		if c.Status == WithdrawalReserved {
			amt, _ := math.NewIntFromString(c.Amount)
			bz, seq, err := cfg.Collector.SignCollect(account, amt, CollectionMemoPrefix+c.ID)
			if err != nil {
				return fmt.Errorf("signing collection %s: %w", c.ID, err)
			}
			c.TxBytes, c.Sequence, c.TxHash = bz, seq, wallet.TxHash(wallet.SignedTx{Bytes: bz})
			c.Status, c.SequenceSpentAt = WithdrawalPending, time.Time{}
			// Saved before it's broadcast: from here it's only re-sent.
			if err := cfg.Ledger.SaveCollection(c); err != nil {
				return fmt.Errorf("saving collection %s: %w", c.ID, err)
			}
		}
		state, err := cfg.Collector.Submit(c.TxBytes, c.Sequence)
		if err != nil {
			return fmt.Errorf("submitting collection %s: %w", c.ID, err)
		}
		switch state.Status {
		case PayoutConfirmed:
			p.forgetGrant(account)
			return cfg.Ledger.CloseCollection(account, true)
		case PayoutPending:
			return nil // checked again next pass
		case PayoutFailed:
			// Revoked, expired or exhausted allowance, or an empty
			// account: the buyer owes it, and is refused until an
			// allowance covers it.
			log.Printf("paywall: collecting %s uaeth from %s failed; refusing it until it grants enough: %s", c.Amount, account, state.Log)
			p.forgetGrant(account)
			return cfg.Ledger.CloseCollection(account, false)
		case PayoutSequenceSpent:
			now := p.cfg.Now()
			if c.SequenceSpentAt.IsZero() {
				c.SequenceSpentAt = now
				if err := cfg.Ledger.SaveCollection(c); err != nil {
					return err
				}
			}
			if now.Sub(c.SequenceSpentAt) < sequenceSpentGrace {
				return nil
			}
			c.Status = WithdrawalReserved // can never land: sign afresh
		default:
			return fmt.Errorf("unexpected collection state %q", state.Status)
		}
	}
	return nil
}

func newCollectionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ChainCollector collects under buyers' allowances, signing with the
// keyring account allowances are granted to (ChainPayout's Address).
type ChainCollector struct {
	ChainPayout
	PayTo string // where collected amounts go
}

func (c *ChainCollector) SignCollect(from string, amount math.Int, memo string) ([]byte, uint64, error) {
	msg, err := wallet.ExecSendMsg(c.Address, from, c.PayTo, sdk.NewCoins(sdk.NewCoin(Asset, amount)))
	if err != nil {
		return nil, 0, err
	}
	return c.sign(func(accNum, seq, gas uint64) (wallet.SignedTx, error) {
		return c.Wallet.BuildAndSignMsgTx(c.KeyName, msg, wallet.TxParams{
			ChainID: c.ChainID, AccountNumber: accNum, Sequence: seq, GasLimit: gas, Memo: memo,
			Fees: sdk.NewCoins(sdk.NewCoin(Asset, math.ZeroInt())),
		})
	})
}

const pullInstructions = "For many requests, paying only for what you use: grant grantee a send allowance on chain " +
	"(x/authz MsgGrant with a SendAuthorization: spend_limit, allow_list [payTo], and an expiration), then sign each request " +
	"with your account's ML-DSA key and send X-PAYMENT: base64 of " +
	`{"x402Version":1,"scheme":"aether-pull","network":"<network>","payload":{account,pubKey,timestamp,requestId,maxPrice,signature}}` +
	" -- see package paywall's PullSigningMessage. Each requestId is charged once, and what you owe is collected from your account " +
	"in batches under the allowance, up to credit at a time. Revoke the allowance to stop."

func newPullState() *pullState {
	return &pullState{grants: map[string]cachedGrant{}, wake: make(chan struct{}, 1)}
}
