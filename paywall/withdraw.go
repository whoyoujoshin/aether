package paywall

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// Withdrawals: an agent takes back what it hasn't spent of a prepaid
// balance. It POSTs to WithdrawPath with a WithdrawalRequest body,
// signed exactly like a paid request (aether-prepaid X-PAYMENT, with
// requestId as the withdrawal's ID), and the server pays the amount
// back on chain -- always to the signing account itself, so a leaked
// or replayed signature can only ever return the account's own money.
//
// Each withdrawal ID pays out once. The amount leaves the balance and
// the signed payout is saved before it is broadcast; asking again with
// the same ID re-sends those same bytes (the chain includes them at
// most once) and reports where they stand. The balance comes back only
// for a payout that can never land.

// WithdrawPath is where a service with withdrawals takes them.
const WithdrawPath = "/.well-known/x402/withdraw"

// Withdrawal error codes (the "error" field of a failed withdrawal).
const (
	ErrWithdrawalsUnavailable = "withdrawals_unavailable"
	ErrWithdrawalIDReused     = "withdrawal_id_reused" // same ID, different amount
	ErrBelowMinimum           = "below_minimum_withdrawal"
	ErrPayoutFailed           = "payout_failed"      // not paid; the balance is back
	ErrPayoutUnavailable      = "payout_unavailable" // try again with the same ID
)

// sequenceSpentGrace is how long a payout whose sequence was used, but
// which isn't on chain, is given to show up (the indexer can lag the
// block) before it's re-signed.
const sequenceSpentGrace = 2 * time.Minute

// WithdrawalRequest is a withdrawal's JSON body.
type WithdrawalRequest struct {
	// Amount in uaeth, or "all" (the default) for the whole balance.
	Amount string `json:"amount,omitempty"`
}

// WithdrawalResponse reports a withdrawal.
type WithdrawalResponse struct {
	X402Version  int    `json:"x402Version"`
	WithdrawalID string `json:"withdrawalId,omitempty"`
	Account      string `json:"account,omitempty"`
	Amount       string `json:"amount,omitempty"` // uaeth
	AmountAeth   string `json:"amountAeth,omitempty"`
	// Status is pending (sent, not in a block yet), confirmed, or
	// reserved (taken from the balance, payout not sent yet: ask again).
	Status  string `json:"status,omitempty"`
	TxHash  string `json:"txHash,omitempty"`
	Balance string `json:"balance,omitempty"` // uaeth left
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

// Payout pays withdrawals from the seller's payout account.
type Payout interface {
	// Sign signs, without broadcasting, a payment of amount uaeth to
	// `to`, returning the transaction and the sequence it used.
	Sign(to string, amount math.Int, memo string) (txBytes []byte, sequence uint64, err error)
	// Submit (re)broadcasts signed bytes and reports where they stand.
	Submit(txBytes []byte, sequence uint64) (PayoutState, error)
}

// PayoutState is where a signed payout stands.
type PayoutState struct {
	// Status is one of PayoutPending, PayoutConfirmed, PayoutFailed
	// (can never land) or PayoutSequenceSpent (not on chain, and its
	// sequence was used by something).
	Status string
	Log    string
}

const (
	PayoutPending       = "pending"
	PayoutConfirmed     = "confirmed"
	PayoutFailed        = "failed"
	PayoutSequenceSpent = "sequence_spent"
)

// WithdrawHandler serves WithdrawPath. Without a Payout it explains
// that this service doesn't offer withdrawals.
func (p *Paywall) WithdrawHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(status int, code, msg string) {
			writeJSON(w, status, WithdrawalResponse{X402Version: X402Version, Error: code, Message: msg})
		}
		if p.cfg.Prepaid == nil || p.cfg.Prepaid.Payout == nil {
			fail(http.StatusNotImplemented, ErrWithdrawalsUnavailable, "this service doesn't pay back prepaid balances; ask its operator")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			fail(http.StatusMethodNotAllowed, ErrInvalidPayment, "POST a signed withdrawal request")
			return
		}
		var pay PaymentPayload
		if err := DecodeHeader(r.Header.Get(HeaderPayment), &pay); err != nil {
			fail(http.StatusBadRequest, ErrInvalidPayment, "sign the withdrawal like a prepaid request: X-PAYMENT must be base64-encoded JSON")
			return
		}
		if pay.Scheme != SchemePrepaid || pay.Network != p.cfg.Network {
			fail(http.StatusBadRequest, ErrUnsupportedScheme, fmt.Sprintf("withdrawals are signed with %s on network %q", SchemePrepaid, p.cfg.Network))
			return
		}
		req, bad := p.verifySigned(r, pay.Payload)
		if bad != nil {
			status := bad.status
			if status == 0 {
				status = http.StatusBadRequest
				if bad.code == ErrInvalidSignature || bad.code == ErrStaleRequest {
					status = http.StatusForbidden
				}
			}
			fail(status, bad.code, bad.message)
			return
		}
		var body WithdrawalRequest
		if len(strings.TrimSpace(string(req.body))) > 0 {
			if err := json.Unmarshal(req.body, &body); err != nil {
				fail(http.StatusBadRequest, ErrInvalidPayment, `the body must be {"amount":"all"} or {"amount":"<uaeth>"}`)
				return
			}
		}
		var amount *math.Int
		if a := strings.TrimSpace(body.Amount); a != "" && a != "all" {
			v, err := wallet.ParseUaeth(a)
			if err != nil || !v.IsPositive() {
				fail(http.StatusBadRequest, ErrInvalidPayment, `amount must be "all" or a positive whole number of uaeth`)
				return
			}
			amount = &v
		}
		p.withdraw(w, req.pay.Account, req.pay.RequestID, amount)
	})
}

func (p *Paywall) withdraw(w http.ResponseWriter, account, id string, amount *math.Int) {
	// One at a time: a payout is a transaction from one account, and
	// the same ID must never be worked on twice at once.
	p.withdrawMu.Lock()
	defer p.withdrawMu.Unlock()

	ledger, payout := p.cfg.Prepaid.Ledger, p.cfg.Prepaid.Payout
	respond := func(status int, wd Withdrawal, msg string) {
		out := WithdrawalResponse{X402Version: X402Version, WithdrawalID: wd.ID, Account: account, Amount: wd.Amount,
			Status: wd.Status, TxHash: wd.TxHash, Message: msg}
		if a, ok := math.NewIntFromString(wd.Amount); ok {
			out.AmountAeth = wallet.FormatAeth(a)
		}
		if bal, err := ledger.Balance(account); err == nil {
			out.Balance = bal.String()
		}
		writeJSON(w, status, out)
	}
	fail := func(status int, code, msg string) {
		out := WithdrawalResponse{X402Version: X402Version, WithdrawalID: id, Account: account, Error: code, Message: msg}
		if bal, err := ledger.Balance(account); err == nil {
			out.Balance = bal.String()
		}
		writeJSON(w, status, out)
	}

	requested := "all"
	if amount != nil {
		requested = amount.String()
	}
	wd, _, fresh, err := ledger.ReserveWithdrawal(account, id, amount, p.cfg.Prepaid.MinDeposit, p.cfg.Now())
	switch {
	case errors.Is(err, ErrLedgerInsufficient):
		fail(http.StatusConflict, ErrInsufficientBalance, "the balance doesn't cover that withdrawal")
		return
	case errors.Is(err, ErrLedgerBelowMinimum):
		fail(http.StatusConflict, ErrBelowMinimum, fmt.Sprintf("withdraw at least %s uaeth, or the whole balance", p.cfg.Prepaid.MinDeposit))
		return
	case err != nil:
		log.Printf("paywall: reserving withdrawal %s/%s: %v", account, id, err)
		fail(http.StatusInternalServerError, ErrPayoutUnavailable, "failed to record the withdrawal; nothing was taken")
		return
	}
	if !fresh && wd.Requested != requested {
		fail(http.StatusConflict, ErrWithdrawalIDReused, fmt.Sprintf("withdrawal %q was for %s; use a new ID for a new withdrawal", id, wd.Requested))
		return
	}

	for attempt := 0; attempt < 2; attempt++ {
		if wd.Status == WithdrawalConfirmed {
			respond(http.StatusOK, wd, "paid back")
			return
		}
		if wd.Status == WithdrawalReserved {
			amt, _ := math.NewIntFromString(wd.Amount)
			bz, seq, err := payout.Sign(account, amt, "prepaid-withdrawal:"+id)
			if err != nil {
				log.Printf("paywall: signing withdrawal %s/%s: %v", account, id, err)
				respond(http.StatusServiceUnavailable, wd, "the payout couldn't be signed right now; the amount is set aside -- ask again with the same withdrawal ID")
				return
			}
			wd.TxBytes, wd.Sequence, wd.TxHash = bz, seq, wallet.TxHash(wallet.SignedTx{Bytes: bz})
			wd.Status, wd.SequenceSpentAt = WithdrawalPending, time.Time{}
			// Saved before it's broadcast: from here it's only re-sent.
			if err := ledger.SaveWithdrawal(wd); err != nil {
				log.Printf("paywall: saving withdrawal %s/%s: %v", account, id, err)
				respond(http.StatusServiceUnavailable, Withdrawal{ID: id, Amount: wd.Amount, Status: WithdrawalReserved}, "failed to record the payout; ask again with the same withdrawal ID")
				return
			}
		}

		state, err := payout.Submit(wd.TxBytes, wd.Sequence)
		if err != nil {
			log.Printf("paywall: submitting withdrawal %s/%s: %v", account, id, err)
			respond(http.StatusAccepted, wd, "the payout is signed but the chain couldn't be reached to confirm it was sent; ask again with the same withdrawal ID")
			return
		}
		switch state.Status {
		case PayoutConfirmed:
			wd.Status, wd.TxBytes = WithdrawalConfirmed, nil
			if err := ledger.SaveWithdrawal(wd); err != nil {
				log.Printf("paywall: saving withdrawal %s/%s: %v", account, id, err)
			}
			respond(http.StatusOK, wd, "paid back")
			return
		case PayoutPending:
			respond(http.StatusOK, wd, "sent; in a block within about a minute")
			return
		case PayoutFailed:
			if err := ledger.CancelWithdrawal(account, id); err != nil {
				log.Printf("paywall: cancelling withdrawal %s/%s: %v", account, id, err)
				respond(http.StatusServiceUnavailable, wd, "the payout failed; ask again with the same withdrawal ID")
				return
			}
			log.Printf("paywall: withdrawal %s/%s failed: %s", account, id, state.Log)
			fail(http.StatusServiceUnavailable, ErrPayoutFailed, "the service couldn't pay this out right now, so nothing was paid and the balance is back; try again later")
			return
		case PayoutSequenceSpent:
			now := p.cfg.Now()
			if wd.SequenceSpentAt.IsZero() {
				wd.SequenceSpentAt = now
				if err := ledger.SaveWithdrawal(wd); err != nil {
					log.Printf("paywall: saving withdrawal %s/%s: %v", account, id, err)
				}
			}
			if now.Sub(wd.SequenceSpentAt) < sequenceSpentGrace {
				respond(http.StatusOK, wd, "sent; not visible on chain yet -- ask again with the same withdrawal ID")
				return
			}
			// Long enough: these bytes can never land. Sign afresh.
			wd.Status = WithdrawalReserved
		default:
			respond(http.StatusServiceUnavailable, wd, "unexpected payout state; ask again with the same withdrawal ID")
			return
		}
	}
	respond(http.StatusOK, wd, "sent")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// PayoutChain is what ChainPayout needs from a node.
type PayoutChain interface {
	GetAccountInfo(address string) (accountNumber, sequence uint64, err error)
	BroadcastTx(signed wallet.SignedTx) (wallet.BroadcastResult, error)
	GetTransactionByHash(hash string) (*wallet.TransactionDetail, error)
}

// ChainPayout pays withdrawals from a keyring account.
type ChainPayout struct {
	Wallet   *wallet.Wallet
	KeyName  string
	Address  string
	Chain    PayoutChain
	ChainID  string
	GasLimit uint64 // default 400000

	mu   sync.Mutex
	next uint64 // lowest sequence not yet signed by this process
}

// SDK root codespace CheckTx codes.
const (
	codeTxInMempool   = 19
	codeWrongSequence = 32
)

func (c *ChainPayout) Sign(to string, amount math.Int, memo string) ([]byte, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	accNum, seq, err := c.Chain.GetAccountInfo(c.Address)
	if err != nil {
		return nil, 0, err
	}
	if seq < c.next {
		seq = c.next // an earlier payout is still in the mempool
	}
	gas := c.GasLimit
	if gas == 0 {
		gas = 400_000
	}
	signed, err := c.Wallet.BuildAndSignSendTx(c.KeyName, c.Address, to, sdk.NewCoins(sdk.NewCoin(Asset, amount)), wallet.TxParams{
		ChainID: c.ChainID, AccountNumber: accNum, Sequence: seq, GasLimit: gas, Memo: memo,
	})
	if err != nil {
		return nil, 0, err
	}
	c.next = seq + 1
	return signed.Bytes, seq, nil
}

func (c *ChainPayout) Submit(txBytes []byte, sequence uint64) (PayoutState, error) {
	signed := wallet.SignedTx{Bytes: txBytes}
	hash := wallet.TxHash(signed)
	if st, found, err := c.lookup(hash); err != nil || found {
		return st, err
	}
	res, err := c.Chain.BroadcastTx(signed)
	if err != nil {
		return PayoutState{}, err
	}
	if res.Code == 0 || (res.Codespace == "sdk" && res.Code == codeTxInMempool) {
		return PayoutState{Status: PayoutPending}, nil
	}
	// Rejected -- unless it has landed since the lookup above.
	if st, found, err := c.lookup(hash); err != nil || found {
		return st, err
	}
	if res.Codespace == "sdk" && res.Code == codeWrongSequence {
		c.resetSequence(sequence)
		return PayoutState{Status: PayoutSequenceSpent, Log: res.RawLog}, nil
	}
	c.resetSequence(sequence)
	return PayoutState{Status: PayoutFailed, Log: res.RawLog}, nil
}

func (c *ChainPayout) lookup(hash string) (PayoutState, bool, error) {
	d, err := c.Chain.GetTransactionByHash(hash)
	if errors.Is(err, wallet.ErrTransactionNotFound) {
		return PayoutState{}, false, nil
	}
	if err != nil {
		return PayoutState{}, false, err
	}
	if d.Code != 0 {
		return PayoutState{Status: PayoutFailed, Log: d.RawLog}, true, nil
	}
	return PayoutState{Status: PayoutConfirmed}, true, nil
}

// resetSequence forgets locally reserved sequences from a rejected
// payout on, so the next one starts from the chain's.
func (c *ChainPayout) resetSequence(sequence uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sequence < c.next {
		c.next = 0
	}
}
