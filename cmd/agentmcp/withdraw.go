package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"cosmossdk.io/math"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

// Taking back unspent prepaid balances (package paywall's withdrawals),
// and remembering where the agent has them.

// prepaidBalance is a seller's last report of this agent's balance.
type prepaidBalance struct {
	Service   string    `json:"service"` // scheme://host
	Balance   string    `json:"balanceUaeth"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// notePrepaidBalance records what payTo last said this agent has left.
func notePrepaidBalance(payTo, service string, balance math.Int) {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := loadState()
	if err != nil {
		return
	}
	st.PrepaidBalances[payTo] = &prepaidBalance{Service: service, Balance: balance.String(), UpdatedAt: time.Now().UTC()}
	_ = st.save()
}

func serviceBase(u *url.URL) string { return u.Scheme + "://" + u.Host }

type listPrepaidBalancesInput struct{}

type prepaidBalanceDTO struct {
	Service   string    `json:"service"`
	PayTo     string    `json:"payTo"`
	Balance   amountDTO `json:"balance" jsonschema:"as the service last reported it"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type listPrepaidBalancesOutput struct {
	Balances []prepaidBalanceDTO `json:"balances"`
}

func toolListPrepaidBalances(_ context.Context, _ *mcp.CallToolRequest, _ listPrepaidBalancesInput) (*mcp.CallToolResult, listPrepaidBalancesOutput, error) {
	stateMu.Lock()
	st, err := loadState()
	stateMu.Unlock()
	if err != nil {
		return nil, listPrepaidBalancesOutput{}, err
	}
	out := listPrepaidBalancesOutput{Balances: []prepaidBalanceDTO{}}
	for payTo, b := range st.PrepaidBalances {
		bal, ok := math.NewIntFromString(b.Balance)
		if !ok || !bal.IsPositive() {
			continue
		}
		out.Balances = append(out.Balances, prepaidBalanceDTO{Service: b.Service, PayTo: payTo, Balance: newAmountDTO(bal), UpdatedAt: b.UpdatedAt})
	}
	sort.Slice(out.Balances, func(i, j int) bool { return out.Balances[i].Service < out.Balances[j].Service })
	return nil, out, nil
}

type withdrawPrepaidInput struct {
	Service        string `json:"service" jsonschema:"the service holding the balance: its URL, e.g. https://api.example.com (any URL on it works)"`
	Amount         string `json:"amount,omitempty" jsonschema:"how much, WITH its unit (e.g. \"0.5 AETH\"), or \"all\" (the default)"`
	IdempotencyKey string `json:"idempotencyKey" jsonschema:"unique ID for this withdrawal. Asking again with the same key never withdraws twice: it reports the same payout"`
}

type withdrawPrepaidOutput struct {
	Status  string     `json:"status" jsonschema:"pending (sent to you, not in a block yet: wait_for_transaction on txHash), confirmed, or reserved (set aside, not sent yet: call again with the same idempotencyKey)"`
	Amount  *amountDTO `json:"amount,omitempty"`
	TxHash  string     `json:"txHash,omitempty"`
	Balance *amountDTO `json:"balance,omitempty" jsonschema:"left with the service"`
	Message string     `json:"message,omitempty"`
}

// withdrawalIDFor is the ID the seller pays out at most once.
func withdrawalIDFor(key string) string {
	sum := sha256.Sum256([]byte("withdraw_prepaid/" + key))
	return hex.EncodeToString(sum[:16])
}

func toolWithdrawPrepaid(ctx context.Context, _ *mcp.CallToolRequest, in withdrawPrepaidInput) (*mcp.CallToolResult, withdrawPrepaidOutput, error) {
	u, err := url.Parse(strings.TrimSpace(in.Service))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, withdrawPrepaidOutput{}, newError(codeInvalidArgument, fmt.Sprintf("service %q must be the service's http(s) URL", in.Service))
	}
	if in.IdempotencyKey == "" || len(in.IdempotencyKey) > maxIdempotencyKeyLength {
		return nil, withdrawPrepaidOutput{}, newError(codeInvalidArgument, fmt.Sprintf("idempotencyKey is required (1-%d characters) so a retried call can't withdraw twice", maxIdempotencyKeyLength))
	}
	body := paywall.WithdrawalRequest{Amount: "all"}
	if a := strings.TrimSpace(in.Amount); a != "" && !strings.EqualFold(a, "all") {
		amt, err := parseAmount(a)
		if err != nil {
			return nil, withdrawPrepaidOutput{}, err
		}
		body.Amount = amt.String()
	}
	base := serviceBase(u)

	// The manifest says who the seller is and whether it pays back.
	m, err := getManifest(ctx, base)
	if err != nil {
		return nil, withdrawPrepaidOutput{}, err
	}
	if m.Network != chainID {
		return nil, withdrawPrepaidOutput{}, newError(codePaymentUnsupported, fmt.Sprintf("the service is on network %q, not %s", m.Network, chainID))
	}
	if m.WithdrawPath == "" {
		return nil, withdrawPrepaidOutput{}, newError(codeWithdrawalsUnavailable, "this service doesn't offer withdrawals: its operator holds the balance")
	}
	// A path, never something that would change the host when appended ("@evil.example/").
	if !strings.HasPrefix(m.WithdrawPath, "/") || strings.HasPrefix(m.WithdrawPath, "//") {
		return nil, withdrawPrepaidOutput{}, newError(codePaymentUnsupported, "the manifest's withdrawPath is not a path on this service")
	}

	w, err := newWallet()
	if err != nil {
		return nil, withdrawPrepaidOutput{}, err
	}
	agent, err := getOrCreateAgentAccount(w)
	if err != nil {
		return nil, withdrawPrepaidOutput{}, err
	}
	reqBody, _ := json.Marshal(body)
	path := m.WithdrawPath
	header, err := paywall.EncodePrepaidPayment(agent.Address, paywall.RequestFields{
		Network: chainID, PayTo: m.PayTo, Host: u.Host, Method: http.MethodPost, Path: path, Body: reqBody,
		MaxPrice: math.ZeroInt(), Timestamp: time.Now().Unix(), RequestID: withdrawalIDFor(in.IdempotencyKey),
	}, func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(accountName, msg) })
	if err != nil {
		return nil, withdrawPrepaidOutput{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, withdrawPrepaidOutput{}, newError(codeInvalidArgument, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(paywall.HeaderPayment, header)
	resp, err := fetchHTTPClient.Do(req)
	if err != nil {
		return nil, withdrawPrepaidOutput{}, newError(codeHTTPError, err.Error())
	}
	defer resp.Body.Close()
	var res paywall.WithdrawalResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&res); err != nil {
		return nil, withdrawPrepaidOutput{}, newError(codeHTTPError, fmt.Sprintf("the service answered HTTP %d without a withdrawal result", resp.StatusCode))
	}

	balance := func() *amountDTO {
		if res.Balance == "0" {
			d := newAmountDTO(math.ZeroInt())
			return &d
		}
		if b, err := wallet.ParseUaeth(res.Balance); err == nil {
			d := newAmountDTO(b)
			return &d
		}
		return nil
	}()
	if balance != nil {
		bal, _ := math.NewIntFromString(balance.Uaeth)
		notePrepaidBalance(m.PayTo, base, bal)
	}

	if res.Error != "" {
		code := codeWithdrawalRejected
		switch res.Error {
		case paywall.ErrWithdrawalsUnavailable:
			code = codeWithdrawalsUnavailable
		case paywall.ErrInsufficientBalance, paywall.ErrBelowMinimum:
			code = codeInsufficientPrepaid
		case paywall.ErrWithdrawalIDReused:
			code = codeIdempotencyConflict
		case paywall.ErrPayoutUnavailable, paywall.ErrPayoutFailed:
			code = codeWithdrawalFailed
		}
		e := newError(code, "the service refused the withdrawal ("+res.Error+"): "+res.Message)
		return nil, withdrawPrepaidOutput{}, e
	}
	out := withdrawPrepaidOutput{Status: res.Status, TxHash: res.TxHash, Balance: balance, Message: res.Message}
	if a, err := wallet.ParseUaeth(res.Amount); err == nil {
		d := newAmountDTO(a)
		out.Amount = &d
	}
	switch res.Status {
	case paywall.WithdrawalPending:
		out.Message = "sent to this agent; call wait_for_transaction on txHash to see it confirm"
	case paywall.WithdrawalReserved:
		out.Message = "set aside but not sent yet: call withdraw_prepaid again with the same idempotencyKey"
	}
	return nil, out, nil
}

func getManifest(ctx context.Context, base string) (*paywall.Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+paywall.ManifestPath, nil)
	if err != nil {
		return nil, newError(codeInvalidArgument, err.Error())
	}
	resp, err := fetchHTTPClient.Do(req)
	if err != nil {
		return nil, newError(codeHTTPError, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, newError(codePaymentUnsupported, fmt.Sprintf("%s%s answered HTTP %d: not an Aether paid service", base, paywall.ManifestPath, resp.StatusCode))
	}
	var m paywall.Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m); err != nil || m.PayTo == "" {
		return nil, newError(codePaymentUnsupported, base+paywall.ManifestPath+" is not a valid manifest")
	}
	return &m, nil
}
