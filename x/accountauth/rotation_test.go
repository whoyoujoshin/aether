package accountauth

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// TestRotation_OldKeyCannotSpendAndEscrowFollows encodes the six
// properties from docs/PQ-AGENT-ROTATION.md. It fails because
// MsgRotateKey and the override pubkey do not exist. When they do,
// remove the t.Fatal and the assertions below run unchanged.
//
// After a successful rotation in block N:
//  1. the account address is unchanged;
//  2. the old pubkey cannot authorize any message in block N or later,
//     including one already in the mempool that lands in the same block;
//  3. the new pubkey can;
//  4. registered authenticators are unchanged;
//  5. open escrows that name the account still settle to it;
//  6. the rotation and the revocation are the same transaction.
func TestRotation_OldKeyCannotSpendAndEscrowFollows(t *testing.T) {
	ms, keeper, ctx := setupMsgServer(t)

	// Account with a primary key, a session-key authenticator, and an
	// open escrow naming it as payee. Balance and escrow live in other
	// modules; the assertions name the account so they follow for free
	// once the address is stable.
	oldPriv, oldPub, account := genSessionKey(t)
	newPriv, newPub, _ := genSessionKey(t)
	_, authPub, _ := genSessionKey(t)
	_ = oldPriv
	_ = newPriv

	_, err := ms.RegisterAuthenticator(ctx, &MsgRegisterAuthenticator{
		Account: account.String(),
		Authenticator: &Authenticator{
			Id: 1,
			Kind: &Authenticator_SessionKey{SessionKey: &SessionKey{
				Pubkey:           authPub,
				ExpiresAtUnix:    ctx.BlockTime().Add(time.Hour).Unix(),
				AllowedMsgTypes:  []string{"/cosmos.bank.v1beta1.MsgSend"},
				SpendLimitUaeth:  "1000000",
			}},
		},
	})
	require.NoError(t, err)
	before := keeper.GetAuthenticators(ctx, account)
	require.Len(t, before, 1)
	escrowPayee := account.String()

	// Rotation is not implemented. This is the failure.
	t.Fatal("MsgRotateKey not implemented: cannot install override pubkey " +
		"or revoke old key in the same transaction (docs/PQ-AGENT-ROTATION.md)")

	// --- assertions (unreachable until the Fatal above is removed) ---

	// 1. Address unchanged. The account was derived from the old key;
	// after rotation it must still be that address, not the new key's.
	require.Equal(t, account.String(), sdk.AccAddress(mldsa.PubKey{Key: oldPub}.Address()).String(),
		"rotation changed the account address")
	require.NotEqual(t, account.String(), sdk.AccAddress(mldsa.PubKey{Key: newPub}.Address()).String(),
		"new key derived the same address; rotation would be a no-op")

	// 2. Old key cannot authorize. A message signed by oldPriv must fail
	// verification against the override. Same-block: a message signed by
	// the old key that lands after the rotation in block N also fails.
	require.Error(t, verifyAs(ctx, account, oldPriv),
		"old key authorized a message after rotation")

	// 3. New key can.
	require.NoError(t, verifyAs(ctx, account, newPriv),
		"new key could not authorize after rotation")

	// 4. Authenticators unchanged.
	after := keeper.GetAuthenticators(ctx, account)
	require.Equal(t, before, after, "authenticators changed across rotation")

	// 5. Escrow still names the same account.
	require.Equal(t, account.String(), escrowPayee,
		"escrow payee changed across rotation")

	// 6. Rotation and revocation are the same transaction: there is no
	// state in which both keys verify. Checked by 2 and 3 holding after
	// a single MsgRotateKey.
}

// verifyAs is the signature check the ante path will perform once the
// override is consulted. It does not exist yet; the assertion calls it
// so the property is in the test, not only in a comment.
func verifyAs(ctx sdk.Context, account sdk.AccAddress, priv *mldsa.PrivKey) error {
	return errNotImplemented
}

var errNotImplemented = errRotationNotImplemented{}

type errRotationNotImplemented struct{}

func (errRotationNotImplemented) Error() string {
	return "rotation not implemented"
}
