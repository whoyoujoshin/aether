package wallet

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

func testAccount(t *testing.T) string {
	t.Helper()
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey().(*mldsa.PubKey)
	return sdk.AccAddress(pub.Address()).String()
}

func testPubkey(t *testing.T) []byte {
	t.Helper()
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	return priv.PubKey().(*mldsa.PubKey).Bytes()
}

func TestRegisterSessionKeyMsg_Validations(t *testing.T) {
	account := testAccount(t)
	pubkey := testPubkey(t)
	future := time.Now().Add(time.Hour)

	_, err := RegisterSessionKeyMsg("not-an-address", pubkey, future, "1000", []string{"/cosmos.bank.v1beta1.MsgSend"})
	require.Error(t, err, "invalid account must be rejected")

	_, err = RegisterSessionKeyMsg(account, []byte("too-short"), future, "1000", []string{"/cosmos.bank.v1beta1.MsgSend"})
	require.Error(t, err, "wrong-size pubkey must be rejected")

	_, err = RegisterSessionKeyMsg(account, pubkey, time.Now().Add(-time.Hour), "1000", []string{"/cosmos.bank.v1beta1.MsgSend"})
	require.Error(t, err, "a past expiry must be rejected")

	_, err = RegisterSessionKeyMsg(account, pubkey, future, "1000", nil)
	require.Error(t, err, "an empty allow-list must be rejected")

	msg, err := RegisterSessionKeyMsg(account, pubkey, future, "1000", []string{"/cosmos.bank.v1beta1.MsgSend"})
	require.NoError(t, err)
	require.Equal(t, account, msg.Account)
	sk, ok := msg.Authenticator.Kind.(*accountauth.Authenticator_SessionKey)
	require.True(t, ok)
	require.Equal(t, pubkey, sk.SessionKey.Pubkey)
}

func TestRegisterGuardianThresholdMsg_Validations(t *testing.T) {
	account := testAccount(t)
	pub1, pub2, pub3 := testPubkey(t), testPubkey(t), testPubkey(t)

	_, err := RegisterGuardianThresholdMsg("not-an-address", [][]byte{pub1, pub2}, 1)
	require.Error(t, err, "invalid account must be rejected")

	_, err = RegisterGuardianThresholdMsg(account, [][]byte{pub1, pub2}, 0)
	require.Error(t, err, "a zero threshold must be rejected")

	_, err = RegisterGuardianThresholdMsg(account, [][]byte{pub1, pub2}, 3)
	require.Error(t, err, "a threshold above the guardian count must be rejected")

	_, err = RegisterGuardianThresholdMsg(account, [][]byte{pub1, []byte("short")}, 1)
	require.Error(t, err, "a wrong-size guardian pubkey must be rejected")

	msg, err := RegisterGuardianThresholdMsg(account, [][]byte{pub1, pub2, pub3}, 2)
	require.NoError(t, err)
	require.Equal(t, account, msg.Account)
}

func TestRevokeAuthenticatorMsg_ValidatesAccount(t *testing.T) {
	_, err := RevokeAuthenticatorMsg("not-an-address", 1)
	require.Error(t, err)

	account := testAccount(t)
	msg, err := RevokeAuthenticatorMsg(account, 7)
	require.NoError(t, err)
	require.Equal(t, account, msg.Account)
	require.Equal(t, uint64(7), msg.Id)
}

func TestExecAuthenticatedSendMsg_BuildsAndValidates(t *testing.T) {
	signer := testAccount(t)
	account := testAccount(t)
	recipient := testAccount(t)
	amount := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 100))

	_, err := ExecAuthenticatedSendMsg(signer, "not-an-address", recipient, amount, 1, nil)
	require.Error(t, err, "invalid account must be rejected")

	_, err = ExecAuthenticatedSendMsg(signer, account, "not-an-address", amount, 1, nil)
	require.Error(t, err, "invalid recipient must be rejected")

	msg, err := ExecAuthenticatedSendMsg(signer, account, recipient, amount, 3, nil)
	require.NoError(t, err)
	require.Equal(t, signer, msg.Signer)
	require.Equal(t, account, msg.Account)
	require.Equal(t, uint64(3), msg.AuthenticatorId)
	require.Len(t, msg.Msgs, 1)
}
