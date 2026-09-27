package wallet

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// Registering, listing, revoking and using pluggable authenticators
// (x/accountauth): an account's own alternate ways to authorize
// actions -- a session key, or a guardian threshold -- without ever
// handing out its primary key. Mirrors grants.go's shape for x/authz,
// which this is the account-abstraction analog of.

// ErrAccountAuthNotActive means the node doesn't serve x/accountauth
// yet (pre-activation -- see app.AccountAuthActivationHeight).
var ErrAccountAuthNotActive = errors.New("x/accountauth is not active on this node yet")

// AuthenticatorInfo is one authenticator an account has registered,
// in a JSON-friendly shape.
type AuthenticatorInfo struct {
	ID   uint64 `json:"id"`
	Kind string `json:"kind"` // "session_key" or "guardian_threshold"

	// Set when Kind == "session_key".
	PubkeyHex       string    `json:"pubkeyHex,omitempty"`
	Address         string    `json:"address,omitempty"`
	ExpiresAt       time.Time `json:"expiresAt,omitempty"`
	AllowedMsgTypes []string  `json:"allowedMsgTypes,omitempty"`
	SpendLimitUaeth string    `json:"spendLimitUaeth,omitempty"`
	SpentUaeth      string    `json:"spentUaeth,omitempty"`

	// Set when Kind == "guardian_threshold".
	GuardianPubkeysHex []string `json:"guardianPubkeysHex,omitempty"`
	Threshold          uint32   `json:"threshold,omitempty"`
	NextSequence       uint64   `json:"nextSequence,omitempty"`
}

// Authenticators lists every authenticator address has registered.
func (c *Client) Authenticators(address string) ([]AuthenticatorInfo, error) {
	resp, err := accountauth.NewQueryClient(c.conn).Authenticators(context.Background(), &accountauth.QueryAuthenticatorsRequest{Account: address})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return nil, ErrAccountAuthNotActive
		}
		return nil, err
	}
	out := make([]AuthenticatorInfo, 0, len(resp.Authenticators))
	for _, a := range resp.Authenticators {
		out = append(out, authenticatorInfoFrom(a))
	}
	return out, nil
}

func authenticatorInfoFrom(a *accountauth.Authenticator) AuthenticatorInfo {
	info := AuthenticatorInfo{ID: a.Id}
	switch kind := a.Kind.(type) {
	case *accountauth.Authenticator_SessionKey:
		sk := kind.SessionKey
		info.Kind = "session_key"
		info.PubkeyHex = hex.EncodeToString(sk.Pubkey)
		info.Address = (&mldsa.PubKey{Key: sk.Pubkey}).Address().String()
		info.ExpiresAt = time.Unix(sk.ExpiresAtUnix, 0).UTC()
		info.AllowedMsgTypes = sk.AllowedMsgTypes
		info.SpendLimitUaeth = sk.SpendLimitUaeth
		info.SpentUaeth = sk.SpentUaeth
	case *accountauth.Authenticator_GuardianThreshold:
		gt := kind.GuardianThreshold
		info.Kind = "guardian_threshold"
		info.GuardianPubkeysHex = make([]string, len(gt.GuardianPubkeys))
		for i, pk := range gt.GuardianPubkeys {
			info.GuardianPubkeysHex[i] = hex.EncodeToString(pk)
		}
		info.Threshold = gt.Threshold
		info.NextSequence = gt.NextSequence
	}
	return info
}

// RegisterSessionKeyMsg registers a session key: a secondary ML-DSA-44
// keypair (pubkey) that may execute allowedMsgTypes on account's behalf,
// up to spendLimit uaeth lifetime, until expiresAt.
func RegisterSessionKeyMsg(account string, pubkey []byte, expiresAt time.Time, spendLimitUaeth string, allowedMsgTypes []string) (*accountauth.MsgRegisterAuthenticator, error) {
	if _, err := sdk.AccAddressFromBech32(account); err != nil {
		return nil, fmt.Errorf("invalid account %q: %w", account, err)
	}
	if len(pubkey) != mldsa.PubKeySize {
		return nil, fmt.Errorf("session key pubkey must be exactly %d bytes, got %d", mldsa.PubKeySize, len(pubkey))
	}
	if !expiresAt.After(time.Now()) {
		return nil, errors.New("expiresAt must be in the future")
	}
	if len(allowedMsgTypes) == 0 {
		return nil, errors.New("a session key must allow at least one message type")
	}
	return &accountauth.MsgRegisterAuthenticator{
		Account: account,
		Authenticator: &accountauth.Authenticator{
			Kind: &accountauth.Authenticator_SessionKey{SessionKey: &accountauth.SessionKey{
				Pubkey:          pubkey,
				ExpiresAtUnix:   expiresAt.Unix(),
				AllowedMsgTypes: allowedMsgTypes,
				SpendLimitUaeth: spendLimitUaeth,
			}},
		},
	}, nil
}

// RegisterGuardianThresholdMsg registers threshold-of-N guardians who
// may jointly authorize actions on account's behalf.
func RegisterGuardianThresholdMsg(account string, guardianPubkeys [][]byte, threshold uint32) (*accountauth.MsgRegisterAuthenticator, error) {
	if _, err := sdk.AccAddressFromBech32(account); err != nil {
		return nil, fmt.Errorf("invalid account %q: %w", account, err)
	}
	if threshold == 0 || int(threshold) > len(guardianPubkeys) {
		return nil, fmt.Errorf("threshold must be between 1 and %d", len(guardianPubkeys))
	}
	for i, pk := range guardianPubkeys {
		if len(pk) != mldsa.PubKeySize {
			return nil, fmt.Errorf("guardian pubkey %d must be exactly %d bytes, got %d", i, mldsa.PubKeySize, len(pk))
		}
	}
	return &accountauth.MsgRegisterAuthenticator{
		Account: account,
		Authenticator: &accountauth.Authenticator{
			Kind: &accountauth.Authenticator_GuardianThreshold{GuardianThreshold: &accountauth.GuardianThreshold{
				GuardianPubkeys: guardianPubkeys,
				Threshold:       threshold,
			}},
		},
	}, nil
}

// RevokeAuthenticatorMsg withdraws authenticator id from account.
func RevokeAuthenticatorMsg(account string, id uint64) (*accountauth.MsgRevokeAuthenticator, error) {
	if _, err := sdk.AccAddressFromBech32(account); err != nil {
		return nil, fmt.Errorf("invalid account %q: %w", account, err)
	}
	return &accountauth.MsgRevokeAuthenticator{Account: account, Id: id}, nil
}

// ExecAuthenticatedSendMsg dispatches a single bank send on account's
// behalf via authenticatorID, signed (as this outer tx's signer) by
// signer -- the session key itself for a session-key authenticator, or
// any relayer carrying guardianSigs for a guardian-threshold one.
func ExecAuthenticatedSendMsg(signer, account, to string, amount sdk.Coins, authenticatorID uint64, guardianSigs []*accountauth.GuardianSignature) (*accountauth.MsgExecAuthenticated, error) {
	signerAddr, accountAddr, err := pair(signer, account)
	if err != nil {
		return nil, err
	}
	toAddr, err := sdk.AccAddressFromBech32(to)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient %q: %w", to, err)
	}
	send := banktypes.NewMsgSend(accountAddr, toAddr, amount)
	any, err := codectypes.NewAnyWithValue(send)
	if err != nil {
		return nil, fmt.Errorf("could not pack inner message: %w", err)
	}
	return &accountauth.MsgExecAuthenticated{
		Signer:             signerAddr.String(),
		Account:            accountAddr.String(),
		AuthenticatorId:    authenticatorID,
		Msgs:               []*codectypes.Any{any},
		GuardianSignatures: guardianSigs,
	}, nil
}
