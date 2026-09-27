package accountauth

import (
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	signing "cosmossdk.io/x/tx/signing"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// fakeRouter is a minimal baseapp.MessageRouter stand-in: every
// message type succeeds trivially except those explicitly listed in
// unrouted, letting tests exercise both the dispatch-success path and
// the "unrecognized message route" error path without needing a real
// bank keeper wired up.
type fakeRouter struct {
	unrouted map[string]bool
}

func (r fakeRouter) Handler(msg sdk.Msg) baseapp.MsgServiceHandler {
	return r.HandlerByTypeURL(sdk.MsgTypeURL(msg))
}

func (r fakeRouter) HandlerByTypeURL(typeURL string) baseapp.MsgServiceHandler {
	if r.unrouted[typeURL] {
		return nil
	}
	return func(ctx sdk.Context, _ sdk.Msg) (*sdk.Result, error) {
		return &sdk.Result{}, nil
	}
}

var _ baseapp.MessageRouter = fakeRouter{}

// newTestCodec builds a codec whose interface registry has a real
// bech32 address codec configured -- required for GetMsgV1Signers (used
// by ExecAuthenticated to check each inner message's signer) to be
// able to decode bech32 address fields like MsgSend.from_address at
// all; a bare codectypes.NewInterfaceRegistry() has none and fails
// every such lookup.
func newTestCodec() codec.Codec {
	registry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: gogoproto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
			ValidatorAddressCodec: address.NewBech32Codec(sdk.GetConfig().GetBech32ValidatorAddrPrefix()),
		},
	})
	if err != nil {
		panic(err)
	}
	banktypes.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}

func setupMsgServer(t *testing.T) (MsgServer, Keeper, sdk.Context) {
	t.Helper()

	storeKey := storetypes.NewKVStoreKey(StoreKey)
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	require.NoError(t, stateStore.LoadLatestVersion())

	ctx := sdk.NewContext(stateStore, tmproto.Header{}, false, log.NewNopLogger()).
		WithChainID("aether-test").
		WithBlockTime(time.Unix(1_700_000_000, 0))

	keeper := NewKeeper(newTestCodec(), storeKey, fakeRouter{})

	return NewMsgServerImpl(keeper), keeper, ctx
}

func genSessionKey(t *testing.T) (*mldsa.PrivKey, []byte, sdk.AccAddress) {
	t.Helper()
	priv, err := mldsa.GenPrivKey()
	require.NoError(t, err)
	pub := priv.PubKey().(*mldsa.PubKey)
	return priv, pub.Bytes(), sdk.AccAddress(pub.Address())
}

func anyOf(t *testing.T, m sdk.Msg) *codectypes.Any {
	t.Helper()
	any, err := codectypes.NewAnyWithValue(m)
	require.NoError(t, err)
	return any
}

// --- RegisterAuthenticator: session key validation ---

func TestRegisterAuthenticator_SessionKey_Validations(t *testing.T) {
	ms, keeper, ctx := setupMsgServer(t)
	_, pub, _ := genSessionKey(t)
	account := sdk.AccAddress("account_____________")

	validSK := &SessionKey{
		Pubkey:          pub,
		ExpiresAtUnix:   ctx.BlockTime().Unix() + 3600,
		AllowedMsgTypes: []string{sdk.MsgTypeURL(&banktypes.MsgSend{})},
		SpendLimitUaeth: "1000",
	}

	cases := []struct {
		name    string
		mutate  func(sk *SessionKey)
		wantErr bool
	}{
		{"wrong pubkey size", func(sk *SessionKey) { sk.Pubkey = []byte("too-short") }, true},
		{"expired", func(sk *SessionKey) { sk.ExpiresAtUnix = ctx.BlockTime().Unix() - 1 }, true},
		{"empty allow-list", func(sk *SessionKey) { sk.AllowedMsgTypes = nil }, true},
		{"invalid spend limit", func(sk *SessionKey) { sk.SpendLimitUaeth = "not-a-number" }, true},
		{"valid", func(sk *SessionKey) {}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sk := *validSK
			tc.mutate(&sk)
			resp, err := ms.RegisterAuthenticator(sdk.WrapSDKContext(ctx), &MsgRegisterAuthenticator{
				Account: account.String(),
				Authenticator: &Authenticator{
					Kind: &Authenticator_SessionKey{SessionKey: &sk},
				},
			})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint64(1), resp.Id)

			stored, found := keeper.GetAuthenticator(ctx, account, resp.Id)
			require.True(t, found)
			storedSK := stored.Kind.(*Authenticator_SessionKey).SessionKey
			require.Equal(t, "0", storedSK.SpentUaeth, "SpentUaeth must always reset to zero on registration")
		})
	}
}

// --- RegisterAuthenticator: guardian threshold validation ---

func TestRegisterAuthenticator_GuardianThreshold_Validations(t *testing.T) {
	ms, keeper, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")

	_, pub1, _ := genSessionKey(t)
	_, pub2, _ := genSessionKey(t)
	_, pub3, _ := genSessionKey(t)

	validGT := &GuardianThreshold{
		GuardianPubkeys: [][]byte{pub1, pub2, pub3},
		Threshold:       2,
		NextSequence:    99, // must be reset to 0 regardless of what's submitted
	}

	cases := []struct {
		name    string
		mutate  func(gt *GuardianThreshold)
		wantErr bool
	}{
		{"no guardians", func(gt *GuardianThreshold) { gt.GuardianPubkeys = nil }, true},
		{"wrong pubkey size", func(gt *GuardianThreshold) { gt.GuardianPubkeys[0] = []byte("short") }, true},
		{"duplicate pubkey", func(gt *GuardianThreshold) { gt.GuardianPubkeys[1] = gt.GuardianPubkeys[0] }, true},
		{"threshold zero", func(gt *GuardianThreshold) { gt.Threshold = 0 }, true},
		{"threshold too large", func(gt *GuardianThreshold) { gt.Threshold = 4 }, true},
		{"valid", func(gt *GuardianThreshold) {}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gt := *validGT
			gt.GuardianPubkeys = append([][]byte{}, validGT.GuardianPubkeys...)
			tc.mutate(&gt)
			resp, err := ms.RegisterAuthenticator(sdk.WrapSDKContext(ctx), &MsgRegisterAuthenticator{
				Account: account.String(),
				Authenticator: &Authenticator{
					Kind: &Authenticator_GuardianThreshold{GuardianThreshold: &gt},
				},
			})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			stored, found := keeper.GetAuthenticator(ctx, account, resp.Id)
			require.True(t, found)
			storedGT := stored.Kind.(*Authenticator_GuardianThreshold).GuardianThreshold
			require.Equal(t, uint64(0), storedGT.NextSequence, "NextSequence must always reset to zero on registration")
		})
	}
}

// --- RevokeAuthenticator ---

func TestRevokeAuthenticator(t *testing.T) {
	ms, keeper, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")
	_, pub, _ := genSessionKey(t)

	_, err := ms.RevokeAuthenticator(sdk.WrapSDKContext(ctx), &MsgRevokeAuthenticator{Account: account.String(), Id: 1})
	require.Error(t, err, "revoking a nonexistent authenticator must fail")

	reg, err := ms.RegisterAuthenticator(sdk.WrapSDKContext(ctx), &MsgRegisterAuthenticator{
		Account: account.String(),
		Authenticator: &Authenticator{Kind: &Authenticator_SessionKey{SessionKey: &SessionKey{
			Pubkey:          pub,
			ExpiresAtUnix:   ctx.BlockTime().Unix() + 3600,
			AllowedMsgTypes: []string{sdk.MsgTypeURL(&banktypes.MsgSend{})},
			SpendLimitUaeth: "1000",
		}}},
	})
	require.NoError(t, err)

	_, err = ms.RevokeAuthenticator(sdk.WrapSDKContext(ctx), &MsgRevokeAuthenticator{Account: account.String(), Id: reg.Id})
	require.NoError(t, err)

	_, found := keeper.GetAuthenticator(ctx, account, reg.Id)
	require.False(t, found)
}

// --- ExecAuthenticated: session key ---

func registerSessionKey(t *testing.T, ms MsgServer, ctx sdk.Context, account sdk.AccAddress, pub []byte, allowed []string, spendLimit string, expiresIn time.Duration) uint64 {
	t.Helper()
	resp, err := ms.RegisterAuthenticator(sdk.WrapSDKContext(ctx), &MsgRegisterAuthenticator{
		Account: account.String(),
		Authenticator: &Authenticator{Kind: &Authenticator_SessionKey{SessionKey: &SessionKey{
			Pubkey:          pub,
			ExpiresAtUnix:   ctx.BlockTime().Add(expiresIn).Unix(),
			AllowedMsgTypes: allowed,
			SpendLimitUaeth: spendLimit,
		}}},
	})
	require.NoError(t, err)
	return resp.Id
}

func TestExecAuthenticated_SessionKey_WrongSigner(t *testing.T) {
	ms, _, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")
	_, pub, _ := genSessionKey(t)
	id := registerSessionKey(t, ms, ctx, account, pub, []string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, "1000", time.Hour)

	other := sdk.AccAddress("someone_else________")
	send := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: other.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10))}

	_, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          other.String(), // not the session key's derived address
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, send)},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrWrongSigner)
}

func TestExecAuthenticated_SessionKey_Expired(t *testing.T) {
	ms, _, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")
	priv, pub, signerAddr := genSessionKey(t)
	id := registerSessionKey(t, ms, ctx, account, pub, []string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, "1000", time.Second)

	send := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: signerAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10))}
	_ = priv

	laterCtx := ctx.WithBlockTime(ctx.BlockTime().Add(time.Hour))
	_, err := ms.ExecAuthenticated(sdk.WrapSDKContext(laterCtx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, send)},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSessionKeyExpired)
}

func TestExecAuthenticated_SessionKey_DisallowedMsgType(t *testing.T) {
	ms, _, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")
	_, pub, signerAddr := genSessionKey(t)
	// Allow-list something other than MsgSend.
	id := registerSessionKey(t, ms, ctx, account, pub, []string{"/aether.pow.v1.MsgSubmitPoW"}, "1000", time.Hour)

	send := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: signerAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10))}
	_, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, send)},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMsgTypeNotAllowed)
}

func TestExecAuthenticated_SessionKey_SpendLimit(t *testing.T) {
	ms, keeper, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")
	_, pub, signerAddr := genSessionKey(t)
	id := registerSessionKey(t, ms, ctx, account, pub, []string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, "100", time.Hour)

	sendOver := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: signerAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 150))}
	_, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, sendOver)},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSpendLimitExceeded)

	// A within-limit spend succeeds and is tracked cumulatively.
	sendOK := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: signerAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 60))}
	resp, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, sendOK)},
	})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)

	stored, _ := keeper.GetAuthenticator(ctx, account, id)
	require.Equal(t, "60", stored.Kind.(*Authenticator_SessionKey).SessionKey.SpentUaeth)

	// A second spend that would push cumulative spend past the limit fails.
	_, err = ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, sendOK)}, // another 60, would total 120 > 100
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSpendLimitExceeded)
}

func TestExecAuthenticated_WrongMsgSigner(t *testing.T) {
	ms, _, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")
	other := sdk.AccAddress("someone_else________")
	_, pub, signerAddr := genSessionKey(t)
	id := registerSessionKey(t, ms, ctx, account, pub, []string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, "1000", time.Hour)

	// Inner message's FromAddress is not the authorizing account.
	send := &banktypes.MsgSend{FromAddress: other.String(), ToAddress: signerAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10))}
	_, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, send)},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrWrongMsgSigner)
}

// --- ExecAuthenticated: guardian threshold ---

func TestExecAuthenticated_GuardianThreshold(t *testing.T) {
	ms, keeper, ctx := setupMsgServer(t)
	account := sdk.AccAddress("account_____________")

	g1, pub1, _ := genSessionKey(t)
	g2, pub2, _ := genSessionKey(t)
	g3, pub3, _ := genSessionKey(t)
	relayer := sdk.AccAddress("relayer_____________")

	reg, err := ms.RegisterAuthenticator(sdk.WrapSDKContext(ctx), &MsgRegisterAuthenticator{
		Account: account.String(),
		Authenticator: &Authenticator{Kind: &Authenticator_GuardianThreshold{GuardianThreshold: &GuardianThreshold{
			GuardianPubkeys: [][]byte{pub1, pub2, pub3},
			Threshold:       2,
		}}},
	})
	require.NoError(t, err)
	id := reg.Id

	send := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: relayer.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10))}
	msgsAny := []*codectypes.Any{anyOf(t, send)}

	sign := func(priv *mldsa.PrivKey, pub []byte, sequence uint64) *GuardianSignature {
		bz := GuardianExecSigningBytes(ctx.ChainID(), account.String(), id, sequence, msgsAny)
		sig, err := priv.Sign(bz)
		require.NoError(t, err)
		return &GuardianSignature{Pubkey: pub, Signature: sig}
	}

	// Below threshold: only one valid signature.
	_, err = ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:             relayer.String(),
		Account:            account.String(),
		AuthenticatorId:    id,
		Msgs:               msgsAny,
		GuardianSignatures: []*GuardianSignature{sign(g1, pub1, 0)},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrThresholdNotMet)

	// Threshold met: two valid, distinct signatures over sequence 0.
	sigs := []*GuardianSignature{sign(g1, pub1, 0), sign(g2, pub2, 0)}
	resp, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:             relayer.String(),
		Account:            account.String(),
		AuthenticatorId:    id,
		Msgs:               msgsAny,
		GuardianSignatures: sigs,
	})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)

	stored, _ := keeper.GetAuthenticator(ctx, account, id)
	require.Equal(t, uint64(1), stored.Kind.(*Authenticator_GuardianThreshold).GuardianThreshold.NextSequence)

	// Replay: the exact same (sequence-0) signatures no longer verify
	// against the now-advanced sequence, so the exec is rejected again.
	_, err = ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:             relayer.String(),
		Account:            account.String(),
		AuthenticatorId:    id,
		Msgs:               msgsAny,
		GuardianSignatures: sigs,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrThresholdNotMet)

	// A fresh, correctly-sequenced pair of signatures succeeds again.
	freshSigs := []*GuardianSignature{sign(g2, pub2, 1), sign(g3, pub3, 1)}
	_, err = ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:             relayer.String(),
		Account:            account.String(),
		AuthenticatorId:    id,
		Msgs:               msgsAny,
		GuardianSignatures: freshSigs,
	})
	require.NoError(t, err)
}

func TestExecAuthenticated_UnrecognizedRoute(t *testing.T) {
	storeKey := storetypes.NewKVStoreKey(StoreKey)
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	require.NoError(t, stateStore.LoadLatestVersion())
	ctx := sdk.NewContext(stateStore, tmproto.Header{}, false, log.NewNopLogger()).
		WithChainID("aether-test").
		WithBlockTime(time.Unix(1_700_000_000, 0))

	unrouted := map[string]bool{sdk.MsgTypeURL(&banktypes.MsgSend{}): true}
	keeper := NewKeeper(newTestCodec(), storeKey, fakeRouter{unrouted: unrouted})
	ms := NewMsgServerImpl(keeper)

	account := sdk.AccAddress("account_____________")
	_, pub, signerAddr := genSessionKey(t)
	id := registerSessionKey(t, ms, ctx, account, pub, []string{sdk.MsgTypeURL(&banktypes.MsgSend{})}, "1000", time.Hour)

	send := &banktypes.MsgSend{FromAddress: account.String(), ToAddress: signerAddr.String(), Amount: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 10))}
	_, err := ms.ExecAuthenticated(sdk.WrapSDKContext(ctx), &MsgExecAuthenticated{
		Signer:          signerAddr.String(),
		Account:         account.String(),
		AuthenticatorId: id,
		Msgs:            []*codectypes.Any{anyOf(t, send)},
	})
	require.Error(t, err)
}
