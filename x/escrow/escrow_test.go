package escrow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeBank keeps balances in memory; the module account is "module:escrow".
type fakeBank struct {
	balances map[string]sdk.Coins
}

func (b *fakeBank) move(from, to string, amt sdk.Coins) error {
	have := b.balances[from]
	left, neg := have.SafeSub(amt...)
	if neg {
		return sdkerrors.ErrInsufficientFunds.Wrapf("%s has %s, needs %s", from, have, amt)
	}
	b.balances[from] = left
	b.balances[to] = b.balances[to].Add(amt...)
	return nil
}

func (b *fakeBank) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, module string, amt sdk.Coins) error {
	return b.move(sender.String(), "module:"+module, amt)
}

func (b *fakeBank) SendCoinsFromModuleToAccount(_ context.Context, module string, to sdk.AccAddress, amt sdk.Coins) error {
	return b.move("module:"+module, to.String(), amt)
}

var (
	payer   = sdk.AccAddress("escrow_test_payer___").String()
	payee   = sdk.AccAddress("escrow_test_payee___").String()
	arbiter = sdk.AccAddress("escrow_test_arbiter_").String()
	other   = sdk.AccAddress("escrow_test_other___").String()
	start   = time.Unix(1_800_000_000, 0)
)

func aeth(n int64) sdk.Coins { return sdk.NewCoins(sdk.NewCoin("uaeth", math.NewInt(n))) }

type fixture struct {
	k    Keeper
	srv  MsgServer
	q    QueryServer
	bank *fakeBank
	ctx  sdk.Context
	cdc  codec.Codec
}

func setup(t *testing.T) *fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(StoreKey)
	db := dbm.NewMemDB()
	ms := store.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	ms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, db)
	require.NoError(t, ms.LoadLatestVersion())
	ctx := sdk.NewContext(ms, tmproto.Header{Height: 10, Time: start}, false, log.NewNopLogger())

	registry := codectypes.NewInterfaceRegistry()
	AppModuleBasic{}.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	bank := &fakeBank{balances: map[string]sdk.Coins{payer: aeth(1_000_000_000)}}
	k := NewKeeper(cdc, key, bank, log.NewNopLogger())
	return &fixture{k: k, srv: NewMsgServerImpl(k), q: NewQueryServerImpl(k), bank: bank, ctx: ctx, cdc: cdc}
}

func (f *fixture) at(d time.Duration) sdk.Context {
	return f.ctx.WithBlockTime(start.Add(d)).WithEventManager(sdk.NewEventManager())
}

func (f *fixture) msg(mut ...func(*MsgCreateEscrow)) *MsgCreateEscrow {
	m := &MsgCreateEscrow{
		Payer: payer, Payee: payee, Arbiter: arbiter, Amount: aeth(5_000_000),
		ExpiresAt: start.Add(time.Hour).Unix(), OnExpiry: ON_EXPIRY_REFUND, Terms: "invoice #42",
	}
	for _, fn := range mut {
		fn(m)
	}
	return m
}

func (f *fixture) create(t *testing.T, mut ...func(*MsgCreateEscrow)) uint64 {
	t.Helper()
	res, err := f.srv.CreateEscrow(f.ctx, f.msg(mut...))
	require.NoError(t, err)
	return res.Id
}

func (f *fixture) balance(addr string) int64 { return f.bank.balances[addr].AmountOf("uaeth").Int64() }

func TestCreate_LocksMoneyAndIndexes(t *testing.T) {
	f := setup(t)
	id := f.create(t)
	require.Equal(t, uint64(1), id)
	require.EqualValues(t, 995_000_000, f.balance(payer))
	require.EqualValues(t, 5_000_000, f.balance("module:escrow"))

	e, ok := f.k.GetEscrow(f.ctx, id)
	require.True(t, ok)
	require.Equal(t, int64(10), e.CreatedHeight)
	require.Equal(t, "invoice #42", e.Terms)
	require.EqualValues(t, 1, f.k.OpenCount(f.ctx, sdk.MustAccAddressFromBech32(payer)))

	var created sdk.Event
	for _, ev := range f.ctx.EventManager().Events() {
		if ev.Type == EventTypeCreated {
			created = ev
		}
	}
	require.Equal(t, EventTypeCreated, created.Type)
	attrs := map[string]string{}
	for _, a := range created.Attributes {
		attrs[a.Key] = a.Value
	}
	require.Equal(t, "1", attrs[AttributeID])
	require.Equal(t, payee, attrs[AttributePayee])
	require.Equal(t, "5000000uaeth", attrs[AttributeAmount])
	require.Equal(t, "ON_EXPIRY_REFUND", attrs[AttributeOnExpiry])

	require.Equal(t, uint64(2), f.create(t), "ids count up")
}

func TestCreate_Rejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*MsgCreateEscrow)
		err  error
	}{
		{"payer pays itself", func(m *MsgCreateEscrow) { m.Payee = payer }, ErrInvalidEscrow},
		{"arbiter is payer", func(m *MsgCreateEscrow) { m.Arbiter = payer }, ErrInvalidEscrow},
		{"arbiter is payee", func(m *MsgCreateEscrow) { m.Arbiter = payee }, ErrInvalidEscrow},
		{"bad payee", func(m *MsgCreateEscrow) { m.Payee = "nope" }, ErrInvalidEscrow},
		{"no amount", func(m *MsgCreateEscrow) { m.Amount = nil }, ErrInvalidEscrow},
		{"no outcome", func(m *MsgCreateEscrow) { m.OnExpiry = ON_EXPIRY_UNSPECIFIED }, ErrInvalidEscrow},
		{"long terms", func(m *MsgCreateEscrow) { m.Terms = string(make([]byte, MaxTermsLength+1)) }, ErrInvalidEscrow},
		{"deadline too soon", func(m *MsgCreateEscrow) { m.ExpiresAt = start.Add(59 * time.Second).Unix() }, ErrInvalidDeadline},
		{"deadline in the past", func(m *MsgCreateEscrow) { m.ExpiresAt = start.Add(-time.Hour).Unix() }, ErrInvalidDeadline},
		{"deadline too far", func(m *MsgCreateEscrow) { m.ExpiresAt = start.Add(MaxDuration + time.Second).Unix() }, ErrInvalidDeadline},
		{"more than the payer has", func(m *MsgCreateEscrow) { m.Amount = aeth(2_000_000_000) }, sdkerrors.ErrInsufficientFunds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			_, err := f.srv.CreateEscrow(f.ctx, f.msg(tc.mut))
			require.ErrorIs(t, err, tc.err)
			require.EqualValues(t, 1_000_000_000, f.balance(payer), "nothing moved")
			_, ok := f.k.GetEscrow(f.ctx, 1)
			require.False(t, ok)
		})
	}

	f := setup(t)
	f.create(t, func(m *MsgCreateEscrow) { m.Arbiter = ""; m.ExpiresAt = start.Add(MinDuration).Unix() })
	f.create(t, func(m *MsgCreateEscrow) { m.ExpiresAt = start.Add(MaxDuration).Unix() })
}

func TestReleaseAndRefund_WhoMayDoWhat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release bool
		sender  string
		ok      bool
	}{
		{"payer releases", true, payer, true},
		{"arbiter releases", true, arbiter, true},
		{"payee can't release to itself", true, payee, false},
		{"stranger can't release", true, other, false},
		{"payee refunds", false, payee, true},
		{"arbiter refunds", false, arbiter, true},
		{"payer can't take it back", false, payer, false},
		{"stranger can't refund", false, other, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			id := f.create(t)
			var err error
			if tc.release {
				_, err = f.srv.ReleaseEscrow(f.ctx, &MsgReleaseEscrow{Sender: tc.sender, Id: id})
			} else {
				_, err = f.srv.RefundEscrow(f.ctx, &MsgRefundEscrow{Sender: tc.sender, Id: id})
			}
			if !tc.ok {
				require.ErrorIs(t, err, ErrNotAllowed)
				_, open := f.k.GetEscrow(f.ctx, id)
				require.True(t, open)
				return
			}
			require.NoError(t, err)
			if tc.release {
				require.EqualValues(t, 5_000_000, f.balance(payee))
				require.EqualValues(t, 995_000_000, f.balance(payer))
			} else {
				require.EqualValues(t, 1_000_000_000, f.balance(payer))
				require.EqualValues(t, 0, f.balance(payee))
			}
			require.EqualValues(t, 0, f.balance("module:escrow"))
			_, open := f.k.GetEscrow(f.ctx, id)
			require.False(t, open)
			require.EqualValues(t, 0, f.k.OpenCount(f.ctx, sdk.MustAccAddressFromBech32(payer)))

			// Settled once: a second attempt finds nothing.
			_, err = f.srv.ReleaseEscrow(f.ctx, &MsgReleaseEscrow{Sender: arbiter, Id: id})
			require.ErrorIs(t, err, ErrNotFound)
			_, err = f.q.Escrow(f.ctx, &QueryEscrowRequest{Id: id})
			require.Equal(t, codes.NotFound, status.Code(err))
		})
	}
}

func TestExpiry_SettlesTheWayThePayerChose(t *testing.T) {
	f := setup(t)
	refund := f.create(t)
	release := f.create(t, func(m *MsgCreateEscrow) {
		m.OnExpiry = ON_EXPIRY_RELEASE
		m.ExpiresAt = start.Add(2 * time.Hour).Unix()
	})

	early := f.at(time.Hour - time.Second)
	f.k.ProcessExpired(early)
	_, open := f.k.GetEscrow(early, refund)
	require.True(t, open, "not due yet")

	due := f.at(time.Hour) // exactly at the deadline
	f.k.ProcessExpired(due)
	_, open = f.k.GetEscrow(due, refund)
	require.False(t, open)
	require.EqualValues(t, 995_000_000, f.balance(payer), "the refund-on-expiry escrow came back; the other is still locked")
	var by string
	for _, ev := range due.EventManager().Events() {
		if ev.Type == EventTypeRefunded {
			for _, a := range ev.Attributes {
				if a.Key == AttributeBy {
					by = a.Value
				}
			}
		}
	}
	require.Equal(t, ByExpiry, by)

	later := f.at(3 * time.Hour)
	f.k.ProcessExpired(later)
	_, open = f.k.GetEscrow(later, release)
	require.False(t, open)
	require.EqualValues(t, 5_000_000, f.balance(payee))
	require.EqualValues(t, 0, f.balance("module:escrow"))
}

func TestExpiry_AtMostAHundredPerBlock(t *testing.T) {
	f := setup(t)
	for i := 0; i < 150; i++ {
		f.create(t, func(m *MsgCreateEscrow) { m.Amount = aeth(1) })
	}
	later := f.at(2 * time.Hour)
	f.k.ProcessExpired(later)
	require.EqualValues(t, 50, f.k.OpenCount(later, sdk.MustAccAddressFromBech32(payer)))
	f.k.ProcessExpired(later)
	require.EqualValues(t, 0, f.k.OpenCount(later, sdk.MustAccAddressFromBech32(payer)))
	require.EqualValues(t, 1_000_000_000, f.balance(payer))
}

func TestOpenEscrowsPerPayerAreCapped(t *testing.T) {
	f := setup(t)
	for i := 0; i < MaxOpenPerPayer; i++ {
		f.create(t, func(m *MsgCreateEscrow) { m.Amount = aeth(1) })
	}
	_, err := f.srv.CreateEscrow(f.ctx, f.msg())
	require.ErrorIs(t, err, ErrTooManyOpen)

	_, err = f.srv.RefundEscrow(f.ctx, &MsgRefundEscrow{Sender: payee, Id: 1})
	require.NoError(t, err)
	f.create(t) // a slot freed up
}

func TestEscrowsByAddress(t *testing.T) {
	f := setup(t)
	for i := 0; i < 5; i++ {
		f.create(t)
	}
	f.create(t, func(m *MsgCreateEscrow) { m.Arbiter = "" }) // id 6: no arbiter

	for who, want := range map[string]int{payer: 6, payee: 6, arbiter: 5, other: 0} {
		res, err := f.q.EscrowsByAddress(f.ctx, &QueryEscrowsByAddressRequest{Address: who})
		require.NoError(t, err)
		require.Len(t, res.Escrows, want, who)
		require.Zero(t, res.NextAfterId)
	}

	page1, err := f.q.EscrowsByAddress(f.ctx, &QueryEscrowsByAddressRequest{Address: payee, Limit: 4})
	require.NoError(t, err)
	require.Len(t, page1.Escrows, 4)
	require.Equal(t, uint64(4), page1.NextAfterId)
	page2, err := f.q.EscrowsByAddress(f.ctx, &QueryEscrowsByAddressRequest{Address: payee, Limit: 4, AfterId: page1.NextAfterId})
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 6}, []uint64{page2.Escrows[0].Id, page2.Escrows[1].Id})
	require.Zero(t, page2.NextAfterId)

	_, err = f.srv.ReleaseEscrow(f.ctx, &MsgReleaseEscrow{Sender: payer, Id: 3})
	require.NoError(t, err)
	res, _ := f.q.EscrowsByAddress(f.ctx, &QueryEscrowsByAddressRequest{Address: arbiter})
	require.Len(t, res.Escrows, 4, "a settled escrow leaves every index")

	_, err = f.q.EscrowsByAddress(f.ctx, &QueryEscrowsByAddressRequest{Address: "nope"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGenesis_RoundTrip(t *testing.T) {
	f := setup(t)
	f.create(t)
	f.create(t, func(m *MsgCreateEscrow) { m.OnExpiry = ON_EXPIRY_RELEASE })
	_, err := f.srv.RefundEscrow(f.ctx, &MsgRefundEscrow{Sender: payee, Id: 1})
	require.NoError(t, err)

	am := NewAppModule(f.k)
	exported := am.ExportGenesis(f.ctx, f.cdc)
	require.NoError(t, AppModuleBasic{}.ValidateGenesis(f.cdc, nil, exported))

	g := setup(t)
	g.bank.balances = f.bank.balances
	NewAppModule(g.k).InitGenesis(g.ctx, g.cdc, exported)
	e, ok := g.k.GetEscrow(g.ctx, 2)
	require.True(t, ok)
	require.Equal(t, ON_EXPIRY_RELEASE, e.OnExpiry)
	require.Equal(t, uint64(3), g.create(t), "ids carry on")
	res, _ := g.q.EscrowsByAddress(g.ctx, &QueryEscrowsByAddressRequest{Address: arbiter})
	require.Len(t, res.Escrows, 2, "indexes rebuilt")
	g.k.ProcessExpired(g.at(2 * time.Hour))
	require.EqualValues(t, 5_000_000, g.balance(payee), "the imported escrow still expires the way it was set to")

	require.NoError(t, AppModuleBasic{}.ValidateGenesis(f.cdc, nil, AppModuleBasic{}.DefaultGenesis(f.cdc)))
}

func TestGenesis_Validate(t *testing.T) {
	ok := Escrow{Id: 1, Payer: payer, Payee: payee, Amount: aeth(1), ExpiresAt: 100, OnExpiry: ON_EXPIRY_REFUND}
	for _, tc := range []struct {
		name string
		g    GenesisState
	}{
		{"next id zero", GenesisState{}},
		{"id at next id", GenesisState{NextId: 1, Escrows: []Escrow{ok}}},
		{"duplicate id", GenesisState{NextId: 2, Escrows: []Escrow{ok, ok}}},
		{"invalid escrow", GenesisState{NextId: 2, Escrows: []Escrow{func() Escrow { e := ok; e.Payee = payer; return e }()}}},
	} {
		require.Error(t, tc.g.Validate(), tc.name)
	}
	require.NoError(t, GenesisState{NextId: 2, Escrows: []Escrow{ok}}.Validate())

	many := GenesisState{NextId: MaxOpenPerPayer + 2}
	for i := 1; i <= MaxOpenPerPayer+1; i++ {
		e := ok
		e.Id = uint64(i)
		many.Escrows = append(many.Escrows, e)
	}
	require.ErrorContains(t, many.Validate(), fmt.Sprintf("more than %d", MaxOpenPerPayer))
}
