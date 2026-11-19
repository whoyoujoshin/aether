package accountauth

import (
	"context"
	"fmt"

	sdkerrors "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/gates"
)

// A session key's spend limit holds whatever it runs.
//
// Before SessionKeyCapHeight the limit counted only the uaeth in each
// MsgSend's amount, so a session key allowed anything else got past it
// (GHSA-j2qr-v7fr-w36f):
//   - MsgMultiSend, an IBC transfer or an escrow moved uaeth uncounted;
//   - a MsgSend of any other denom (USDC) wasn't counted at all;
//   - authz MsgGrant or a fee allowance handed the account's funds to
//     another key, which then spent them outside the session;
//   - MsgRegisterAuthenticator registered a second session key with a
//     bigger limit (or none worth the name).
//
// From the height:
//   - What a session key spends is what leaves the account: each message's
//     fall in the account's uaeth balance, read from the bank before and
//     after it runs, whatever the message. Its spent total and limit are
//     in uaeth, so a message that lowers the account's balance of any
//     other denom is refused.
//   - A session key never runs a message that hands the account's
//     authority to another key or moves value already out of its balance
//     (sessionNeverDelegable), and can't be registered allowing one.

// SessionKeyCapHeight switches this on. Fresh chains have it from block
// 1. On the testnet it needs every validator on a build that has it: from
// the height an older binary runs what this one refuses, so it splits at
// the first such transaction.
//
// On the testnet: 812,880, chosen 2026-11-13 at a tip of 733,035, about
// 10:20 UTC on Wednesday 2026-11-18 at 5.66 s blocks, 720 blocks from each
// seat boundary (docs/UPGRADE-SESSION-KEY-CAP.md). It adds no store, so
// nothing halts at it. Earlier builds of this fix carried 70,000,000.
const SessionKeyCapHeight int64 = 812_880

// SessionKeyCapOn reports whether the rule is on at ctx's height.
func SessionKeyCapOn(ctx sdk.Context) bool {
	return gates.On(ctx, SessionKeyCapHeight)
}

// sessionNeverDelegable is every message a session key may never run,
// with why. Type URLs as strings: the modules that define them aren't
// imports of this one. TestSessionKeyCap_EveryMessageClassified holds
// every message type the app routes to this list or to the ones a session
// key may run, so a new message type has to be placed in one.
var sessionNeverDelegable = map[string]string{
	// Hand the account's authority to another key.
	"/cosmos.authz.v1beta1.MsgGrant":                                                   "an authz grant lets another key act for the account outside the session",
	"/cosmos.authz.v1beta1.MsgExec":                                                    "it acts on grants others made to the account",
	"/cosmos.feegrant.v1beta1.MsgGrantAllowance":                                       "a fee allowance lets another key spend the account's funds on fees",
	"/aether.accountauth.v1.MsgRegisterAuthenticator":                                  "it registers another authenticator for the account",
	"/aether.accountauth.v1.MsgRevokeAuthenticator":                                    "it removes the account's other authenticators",
	"/aether.accountauth.v1.MsgExecAuthenticated":                                      "it runs another authenticator",
	"/aether.pow.v1.MsgRegisterValidatorPubkey":                                        "it names the consensus key that holds the account's seat",
	"/aether.accountauth.v1.MsgRotateKey":                                              "it changes the account's key",
	"/aether.accountauth.v1.MsgCancelPendingRotation":                                  "it changes the account's key",
	"/aether.accountauth.v1.MsgSetRecovery":                                            "it changes the account's recovery set",
	"/aether.accountauth.v1.MsgRecoverKey":                                             "it changes the account's key",
	"/aether.accountauth.v1.MsgGuardianCancel":                                         "it changes the account's key",
	"/aether.accountauth.v1.MsgGuardianSetRecovery":                                    "it changes the account's recovery set",
	"/aether.accountauth.v1.MsgWatchtowerCancel":                                       "it changes the account's key",
	"/ibc.applications.interchain_accounts.controller.v1.MsgRegisterInterchainAccount": "it opens an account on another chain that this one controls",
	"/ibc.applications.interchain_accounts.controller.v1.MsgSendTx":                    "it spends from the account's accounts on other chains",
	// Move value that is no longer in the account's balance.
	"/aether.escrow.v1.MsgReleaseEscrow": "it pays out funds the account already locked in escrow",
}

// SessionNeverDelegable reports whether a session key may never run
// typeURL, and why.
func SessionNeverDelegable(typeURL string) (string, bool) {
	why, ok := sessionNeverDelegable[typeURL]
	return why, ok
}

// BankKeeper is the bank's balance reads the spend limit needs.
type BankKeeper interface {
	GetAllBalances(ctx context.Context, addr sdk.AccAddress) sdk.Coins
}

// WithBankKeeper gives the keeper the bank keeper session keys' spending
// is read from.
func (k Keeper) WithBankKeeper(bk BankKeeper) Keeper {
	k.bk = bk
	return k
}

// checkAuthenticatorPresent refuses, from SessionKeyCapHeight, a
// registration with no authenticator, or a kind with no body. Before it
// such a transaction failed by panicking in the handler (caught, the same
// on every node); its result now carries a plain error instead.
func checkAuthenticatorPresent(ctx sdk.Context, a *Authenticator) error {
	if !SessionKeyCapOn(ctx) {
		return nil
	}
	if a == nil {
		return sdkerrors.Wrap(ErrInvalidAuthenticator, "authenticator is required")
	}
	switch kind := a.Kind.(type) {
	case *Authenticator_SessionKey:
		if kind.SessionKey == nil {
			return sdkerrors.Wrap(ErrInvalidAuthenticator, "session_key is empty")
		}
	case *Authenticator_GuardianThreshold:
		if kind.GuardianThreshold == nil {
			return sdkerrors.Wrap(ErrInvalidAuthenticator, "guardian_threshold is empty")
		}
	}
	return nil
}

// checkSessionAllows refuses, from the height, a session key allowed to
// run a message it never may.
func checkSessionAllows(ctx sdk.Context, sk *SessionKey) error {
	if !SessionKeyCapOn(ctx) {
		return nil
	}
	for _, t := range sk.AllowedMsgTypes {
		if why, ok := SessionNeverDelegable(t); ok {
			return sdkerrors.Wrapf(ErrNotSessionDelegable, "%s: %s", t, why)
		}
	}
	return nil
}

// sessionSpend is what one message cost the account, from its balances
// before and after it ran: the fall in uaeth, or an error if any other
// denom fell.
func sessionSpend(before, after sdk.Coins) (sdkmath.Int, error) {
	spent := sdkmath.ZeroInt()
	for _, c := range before {
		fell := c.Amount.Sub(after.AmountOf(c.Denom))
		if !fell.IsPositive() {
			continue
		}
		if c.Denom != "uaeth" {
			return spent, sdkerrors.Wrapf(ErrSpendLimitExceeded, "a session key's limit is in uaeth, so it can't spend %s (this message would move %s)", c.Denom, sdk.NewCoin(c.Denom, fell))
		}
		spent = fell
	}
	return spent, nil
}

// runMetered runs a session key's messages as account, adding each
// message's spend to the session key's total and refusing the whole
// exec once it passes the limit, then saves the total. Any error reverts
// every message, since the exec as a whole fails.
func (k msgServer) runMetered(ctx sdk.Context, account sdk.AccAddress, a *Authenticator, sk *SessionKey, msgs []sdk.Msg) ([][]byte, error) {
	if k.bk == nil {
		return nil, fmt.Errorf("accountauth: no bank keeper to meter session keys with")
	}
	limit, _ := sdkmath.NewIntFromString(sk.SpendLimitUaeth)
	spent, ok := sdkmath.NewIntFromString(sk.SpentUaeth)
	if !ok {
		spent = sdkmath.ZeroInt()
	}
	results := make([][]byte, len(msgs))
	for i, m := range msgs {
		before := k.bk.GetAllBalances(ctx, account)
		data, err := k.dispatch(ctx, i, m)
		if err != nil {
			return nil, err
		}
		cost, err := sessionSpend(before, k.bk.GetAllBalances(ctx, account))
		if err != nil {
			return nil, sdkerrors.Wrapf(err, "message %d", i)
		}
		total, err := spent.SafeAdd(cost)
		if err != nil {
			return nil, sdkerrors.Wrapf(ErrSpendLimitExceeded, "message %d takes this session key's spending past what a uaeth amount can hold", i)
		}
		spent = total
		if spent.GT(limit) {
			return nil, sdkerrors.Wrapf(ErrSpendLimitExceeded, "this session key's lifetime spend limit is %s uaeth; message %d would take it to %s", limit, i, spent)
		}
		results[i] = data
	}
	sk.SpentUaeth = spent.String()
	a.Kind = &Authenticator_SessionKey{SessionKey: sk}
	k.Keeper.SetAuthenticator(ctx, account, *a)
	return results, nil
}
