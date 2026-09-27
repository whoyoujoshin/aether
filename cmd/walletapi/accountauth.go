package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// Pluggable account abstraction (x/accountauth): registering session
// keys and guardian thresholds, listing what an account has registered,
// revoking them, and executing a bank send through one. Mirrors
// grants.go's shape for x/authz -- the other, chain-native way an
// account can let something act on its behalf.

// GET /api/accountauth?name=mywallet -- authenticators this account has registered.
func handleAccountAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use GET"))
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
	authenticators, err := client.Authenticators(acc.Address)
	if err != nil {
		writeError(w, accountAuthStatus(err), fmt.Errorf("failed to load authenticators: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"address": acc.Address, "authenticators": authenticators})
}

func accountAuthStatus(err error) int {
	if errors.Is(err, wallet.ErrAccountAuthNotActive) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

type registerSessionKeyRequest struct {
	From            string   `json:"from"`      // keyring account name of the account granting access
	PubkeyHex       string   `json:"pubkeyHex"` // the session key's raw ML-DSA-44 public key
	ExpiresAt       int64    `json:"expiresAt"` // Unix time
	SpendLimit      string   `json:"spendLimit"`
	AllowedMsgTypes []string `json:"allowedMsgTypes"`
}

// POST /api/accountauth/session-key
func handleRegisterSessionKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var req registerSessionKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	pubkey, err := hex.DecodeString(req.PubkeyHex)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("pubkeyHex: %w", err))
		return
	}
	expires := time.Unix(req.ExpiresAt, 0).UTC()
	if now := time.Now(); !expires.After(now) || expires.Sub(now) > maxGrantDuration {
		writeError(w, http.StatusBadRequest, fmt.Errorf("expiresAt must be in the future and at most a year away"))
		return
	}
	withAccount(w, req.From, func(acc wallet.Account, _ *wallet.Client) ([]sdk.Msg, error) {
		msg, err := wallet.RegisterSessionKeyMsg(acc.Address, pubkey, expires, req.SpendLimit, req.AllowedMsgTypes)
		if err != nil {
			return nil, badRequest{err}
		}
		return []sdk.Msg{msg}, nil
	})
}

type registerGuardianThresholdRequest struct {
	From              string   `json:"from"` // keyring account name
	GuardianPubkeyHex []string `json:"guardianPubkeyHex"`
	Threshold         uint32   `json:"threshold"`
}

// POST /api/accountauth/guardian-threshold
func handleRegisterGuardianThreshold(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var req registerGuardianThresholdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	pubkeys := make([][]byte, len(req.GuardianPubkeyHex))
	for i, h := range req.GuardianPubkeyHex {
		pk, err := hex.DecodeString(h)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("guardianPubkeyHex[%d]: %w", i, err))
			return
		}
		pubkeys[i] = pk
	}
	withAccount(w, req.From, func(acc wallet.Account, _ *wallet.Client) ([]sdk.Msg, error) {
		msg, err := wallet.RegisterGuardianThresholdMsg(acc.Address, pubkeys, req.Threshold)
		if err != nil {
			return nil, badRequest{err}
		}
		return []sdk.Msg{msg}, nil
	})
}

type revokeAuthenticatorRequest struct {
	From string `json:"from"` // keyring account name
	ID   uint64 `json:"id"`
}

// POST /api/accountauth/revoke
func handleRevokeAuthenticator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var req revokeAuthenticatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	withAccount(w, req.From, func(acc wallet.Account, _ *wallet.Client) ([]sdk.Msg, error) {
		msg, err := wallet.RevokeAuthenticatorMsg(acc.Address, req.ID)
		if err != nil {
			return nil, badRequest{err}
		}
		return []sdk.Msg{msg}, nil
	})
}

type execAuthenticatedSendRequest struct {
	// From is the keyring account name signing this outer tx: the
	// session key itself for a session-key authenticator, or any
	// relayer for a guardian-threshold one.
	From            string                `json:"from"`
	Account         string                `json:"account"` // whose behalf this executes on
	AuthenticatorID uint64                `json:"authenticatorId"`
	To              string                `json:"to"`
	Amount          string                `json:"amount"` // uaeth, plain integer
	GuardianSigs    []guardianSigResource `json:"guardianSigs,omitempty"`
}

type guardianSigResource struct {
	PubkeyHex    string `json:"pubkeyHex"`
	SignatureHex string `json:"signatureHex"`
}

// POST /api/accountauth/exec -- send uaeth from account, authorized by
// authenticatorId, via this outer tx's own signer (from).
func handleExecAuthenticated(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var req execAuthenticatedSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	amount, err := parseUaeth(req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("amount: %w", err))
		return
	}
	guardianSigs := make([]*accountauth.GuardianSignature, len(req.GuardianSigs))
	for i, g := range req.GuardianSigs {
		pub, err := hex.DecodeString(g.PubkeyHex)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("guardianSigs[%d].pubkeyHex: %w", i, err))
			return
		}
		sig, err := hex.DecodeString(g.SignatureHex)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("guardianSigs[%d].signatureHex: %w", i, err))
			return
		}
		guardianSigs[i] = &accountauth.GuardianSignature{Pubkey: pub, Signature: sig}
	}
	withAccount(w, req.From, func(acc wallet.Account, _ *wallet.Client) ([]sdk.Msg, error) {
		msg, err := wallet.ExecAuthenticatedSendMsg(acc.Address, req.Account, req.To, sdk.NewCoins(sdk.NewCoin("uaeth", amount)), req.AuthenticatorID, guardianSigs)
		if err != nil {
			return nil, badRequest{err}
		}
		return []sdk.Msg{msg}, nil
	})
}
