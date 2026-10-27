package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/x/accountauth"
)

func TestAuthenticatorToDTO_SessionKeyAndGuardian(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	expired := authenticatorToDTO(accountauth.Authenticator{
		Id: 3,
		Kind: &accountauth.Authenticator_SessionKey{SessionKey: &accountauth.SessionKey{
			Pubkey:          []byte{0x01, 0x02, 0x0a, 0x0b, 0x10, 0x11, 0x1a, 0x1b, 0xff},
			ExpiresAtUnix:   now.Add(-time.Hour).Unix(),
			AllowedMsgTypes: []string{"/cosmos.bank.v1beta1.MsgSend"},
			SpendLimitUaeth: "1000000",
			SpentUaeth:      "250000",
		}},
	}, now)
	require.Equal(t, "session_key", expired.Kind)
	require.True(t, expired.Expired)
	require.Equal(t, "01020a0b10111a1b…", expired.Pubkey)
	require.Equal(t, 9, expired.PubkeyBytes)
	require.Equal(t, []string{"/cosmos.bank.v1beta1.MsgSend"}, expired.AllowedMsgTypes)
	require.Equal(t, "1000000", expired.SpendLimitUaeth)

	live := authenticatorToDTO(accountauth.Authenticator{
		Id: 4,
		Kind: &accountauth.Authenticator_GuardianThreshold{GuardianThreshold: &accountauth.GuardianThreshold{
			GuardianPubkeys: [][]byte{{0xaa, 0xbb}, {0x01}},
			Threshold:       2,
			NextSequence:    7,
		}},
	}, now)
	require.Equal(t, "guardian_threshold", live.Kind)
	require.Equal(t, 2, live.Guardians)
	require.Equal(t, uint32(2), live.Threshold)
	require.Equal(t, uint64(7), live.NextSequence)
	require.Equal(t, []string{"aabb", "01"}, live.GuardianKeys)
	require.False(t, live.Expired)
}

func TestAccountsFrom_OrdersByLatestMessage(t *testing.T) {
	auth := map[string][]aaAuthDTO{
		"aether1old": {{ID: 1, Kind: "session_key"}},
		"aether1new": {},
	}
	txs := []aaTxDTO{
		{Account: "aether1old", Height: 10, Action: "MsgRegisterAuthenticator"},
		{Account: "aether1new", Height: 40, Action: "MsgExecAuthenticated"},
		{Account: "aether1old", Height: 20, Action: "MsgRevokeAuthenticator"},
	}
	rows := accountsFrom(auth, txs)
	require.Equal(t, []string{"aether1new", "aether1old"}, []string{rows[0].Address, rows[1].Address})
	require.Equal(t, int64(40), rows[0].LastHeight)
	require.Equal(t, "MsgExecAuthenticated", rows[0].LastAction)
	require.Equal(t, "MsgRevokeAuthenticator", rows[1].LastAction)
	require.Empty(t, rows[0].Authenticators)
	require.Len(t, rows[1].Authenticators, 1)
}
