package main

import (
	"context"

	"cosmossdk.io/math"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// amountDTOFromUaethString parses a stored uaeth amount string (e.g.
// SessionKey.SpendLimitUaeth/SpentUaeth) into the same two-unit shape
// every other amount in this server reports. Falls back to zero rather
// than erroring: this is read-only reporting, not something that
// enforces spend limits itself.
func amountDTOFromUaethString(s string) amountDTO {
	amount, ok := math.NewIntFromString(s)
	if !ok {
		amount = math.ZeroInt()
	}
	return newAmountDTO(amount)
}

// get_account_authenticators: read-only visibility into x/accountauth
// (pluggable account abstraction) -- a second, chain-native way an
// account can delegate authority, alongside the authz grant this
// server's own "grant mode" already uses. This server doesn't act as a
// session key or guardian itself (that's a bigger mode alongside
// hot-wallet/grant, not part of this milestone); it just lets an agent
// or its operator check what's registered for any address, including
// its own.

type getAccountAuthenticatorsInput struct {
	Address string `json:"address,omitempty" jsonschema:"address to check; defaults to this agent's own account if omitted"`
}

type sessionKeyDTO struct {
	Address         string    `json:"address" jsonschema:"the session key's own account address, derived from its public key"`
	ExpiresAt       string    `json:"expiresAt" jsonschema:"RFC3339; the session key stops working at or after this time"`
	AllowedMsgTypes []string  `json:"allowedMsgTypes"`
	SpendLimit      amountDTO `json:"spendLimit" jsonschema:"total this key may ever move, lifetime"`
	Spent           amountDTO `json:"spent" jsonschema:"running total already spent"`
}

type guardianThresholdDTO struct {
	GuardianCount int    `json:"guardianCount"`
	Threshold     uint32 `json:"threshold" jsonschema:"how many guardians must jointly sign to authorize an action"`
	NextSequence  uint64 `json:"nextSequence" jsonschema:"replay-protection counter the next accepted exec's signatures must cover"`
}

type authenticatorDTO struct {
	ID                uint64                `json:"id"`
	Kind              string                `json:"kind" jsonschema:"session_key or guardian_threshold"`
	SessionKey        *sessionKeyDTO        `json:"sessionKey,omitempty"`
	GuardianThreshold *guardianThresholdDTO `json:"guardianThreshold,omitempty"`
}

type getAccountAuthenticatorsOutput struct {
	Address        string             `json:"address"`
	Authenticators []authenticatorDTO `json:"authenticators"`
}

func toolGetAccountAuthenticators(_ context.Context, _ *mcp.CallToolRequest, input getAccountAuthenticatorsInput) (*mcp.CallToolResult, getAccountAuthenticatorsOutput, error) {
	address, err := agentOrAddress(input.Address)
	if err != nil {
		return nil, getAccountAuthenticatorsOutput{}, err
	}
	out, err := authenticatorsFor(address)
	return nil, out, err
}

// authenticatorsFor reads an address. It does not open the keyring.
func authenticatorsFor(address string) (getAccountAuthenticatorsOutput, error) {
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return getAccountAuthenticatorsOutput{}, err
	}
	defer client.Close()

	authenticators, err := client.Authenticators(address)
	if err != nil {
		return getAccountAuthenticatorsOutput{}, err
	}

	out := make([]authenticatorDTO, len(authenticators))
	for i, a := range authenticators {
		d := authenticatorDTO{ID: a.ID, Kind: a.Kind}
		switch a.Kind {
		case "session_key":
			d.SessionKey = &sessionKeyDTO{
				Address:         a.Address,
				ExpiresAt:       a.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
				AllowedMsgTypes: a.AllowedMsgTypes,
				SpendLimit:      amountDTOFromUaethString(a.SpendLimitUaeth),
				Spent:           amountDTOFromUaethString(a.SpentUaeth),
			}
		case "guardian_threshold":
			d.GuardianThreshold = &guardianThresholdDTO{
				GuardianCount: len(a.GuardianPubkeysHex),
				Threshold:     a.Threshold,
				NextSequence:  a.NextSequence,
			}
		}
		out[i] = d
	}
	return getAccountAuthenticatorsOutput{Address: address, Authenticators: out}, nil
}
