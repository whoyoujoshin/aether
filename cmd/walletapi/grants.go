package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// Agent permissions: an account lets another (typically an AI agent)
// send from it up to a limit until a date (x/authz), and optionally pays
// that account's transaction fees (x/feegrant). The chain enforces
// both; the owner can revoke them at any time.

type grantsResponse struct {
	Address  string              `json:"address"`
	Given    []wallet.Permission `json:"given"`
	Received []wallet.Permission `json:"received"`
}

func loadPermissions(c wallet.PermissionSource, address string) (grantsResponse, error) {
	given, received, err := wallet.LoadPermissions(c, address)
	return grantsResponse{Address: address, Given: given, Received: received}, err
}

// GET /api/grants?name=mywallet -- what the account has given and received.
// POST /api/grants -- give (or replace) a permission; see grantRequest.
func handleGrants(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleGrant(w, r)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing 'name' query parameter"))
		return
	}
	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	acc, err := wal.GetAccount(name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("account %q not found: %w", name, err))
		return
	}
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()
	resp, err := loadPermissions(client, acc.Address)
	if err != nil {
		writeError(w, chainStatus(err), fmt.Errorf("failed to load grants: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func chainStatus(err error) int {
	if errors.Is(err, wallet.ErrAuthzNotActive) || errors.Is(err, wallet.ErrFeegrantNotActive) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

type grantRequest struct {
	From    string `json:"from"`    // keyring account name
	Grantee string `json:"grantee"` // the agent's address
	Limit   string `json:"limit"`   // uaeth the agent may send in total
	// ExpiresAt is a Unix time; the permission ends then.
	ExpiresAt int64 `json:"expiresAt"`
	// AllowList, if set, is the only addresses the agent may pay.
	AllowList []string `json:"allowList,omitempty"`
	// FeeLimit, if set, also pays the agent's fees, up to this many
	// uaeth ("unlimited": no cap) until ExpiresAt.
	FeeLimit string `json:"feeLimit,omitempty"`
}

type revokeRequest struct {
	From    string `json:"from"`
	Grantee string `json:"grantee"`
}

// maxGrantDuration keeps a mistyped expiry from granting for decades.
const maxGrantDuration = 366 * 24 * time.Hour

func handleGrant(w http.ResponseWriter, r *http.Request) {
	var req grantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	limit, err := parseUaeth(req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("limit: %w", err))
		return
	}
	expires := time.Unix(req.ExpiresAt, 0).UTC()
	if now := time.Now(); !expires.After(now) || expires.Sub(now) > maxGrantDuration {
		writeError(w, http.StatusBadRequest, fmt.Errorf("expiresAt must be in the future and at most a year away"))
		return
	}
	var feeLimit sdk.Coins
	if req.FeeLimit != "" && req.FeeLimit != "unlimited" {
		fl, err := parseUaeth(req.FeeLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("feeLimit: %w", err))
			return
		}
		feeLimit = sdk.NewCoins(sdk.NewCoin("uaeth", fl))
	}
	withAccount(w, req.From, func(acc wallet.Account, client *wallet.Client) ([]sdk.Msg, error) {
		grant, err := wallet.SendGrantMsg(acc.Address, req.Grantee, sdk.NewCoins(sdk.NewCoin("uaeth", limit)), req.AllowList, expires)
		if err != nil {
			return nil, badRequest{err}
		}
		msgs := []sdk.Msg{grant}
		// A fee allowance can't be granted over an existing one: replace
		// it in the same transaction.
		existing, err := client.FeeAllowancesGiven(acc.Address)
		if err != nil {
			return nil, err
		}
		for _, f := range existing {
			if f.Grantee == req.Grantee {
				revoke, err := wallet.RevokeFeeAllowanceMsg(acc.Address, req.Grantee)
				if err != nil {
					return nil, err
				}
				msgs = append(msgs, revoke)
			}
		}
		if req.FeeLimit != "" {
			fees, err := wallet.FeeAllowanceMsg(acc.Address, req.Grantee, feeLimit, expires)
			if err != nil {
				return nil, badRequest{err}
			}
			msgs = append(msgs, fees)
		}
		return msgs, nil
	})
}

// POST /api/grants/revoke -- withdraw everything given to grantee.
func handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var req revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	withAccount(w, req.From, func(acc wallet.Account, client *wallet.Client) ([]sdk.Msg, error) {
		perms, err := loadPermissions(client, acc.Address)
		if err != nil {
			return nil, err
		}
		var msgs []sdk.Msg
		for _, p := range perms.Given {
			if p.Account != req.Grantee {
				continue
			}
			if p.Send != nil {
				m, err := wallet.RevokeSendMsg(acc.Address, req.Grantee)
				if err != nil {
					return nil, badRequest{err}
				}
				msgs = append(msgs, m)
			}
			if p.Fees != nil {
				m, err := wallet.RevokeFeeAllowanceMsg(acc.Address, req.Grantee)
				if err != nil {
					return nil, badRequest{err}
				}
				msgs = append(msgs, m)
			}
		}
		if len(msgs) == 0 {
			return nil, badRequest{fmt.Errorf("%s has no send grant or fee allowance from this account", req.Grantee)}
		}
		return msgs, nil
	})
}

type badRequest struct{ error }

// withAccount signs and broadcasts the messages build returns, from the
// keyring account named from, and writes the result like /api/send.
func withAccount(w http.ResponseWriter, from string, build func(wallet.Account, *wallet.Client) ([]sdk.Msg, error)) {
	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	acc, err := wal.GetAccount(from)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("account %q not found: %w", from, err))
		return
	}
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()
	msgs, err := build(acc, client)
	var bad badRequest
	if errors.As(err, &bad) {
		writeError(w, http.StatusBadRequest, bad.error)
		return
	}
	if err != nil {
		writeError(w, chainStatus(err), err)
		return
	}
	accountNumber, sequence, err := client.GetAccountInfo(acc.Address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to fetch account info: %w", err))
		return
	}
	signed, err := wal.BuildAndSignMsgsTx(from, msgs, wallet.TxParams{
		ChainID: chainID, AccountNumber: accountNumber, Sequence: sequence, GasLimit: 400_000,
		Fees: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 0)),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to build/sign transaction: %w", err))
		return
	}
	result, err := client.BroadcastTx(signed)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to broadcast: %w", err))
		return
	}
	if result.Code != 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": false, "code": result.Code, "message": result.RawLog})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "tx_hash": result.TxHash})
}
