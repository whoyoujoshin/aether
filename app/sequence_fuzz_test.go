package app

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/whoyoujoshin/aether/gates"
	"github.com/whoyoujoshin/aether/x/escrow"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// FuzzMoneySequences runs a sequence of real operations, read from the
// fuzz input, on a fresh chain whose accounts hold money and are active
// validators: sends (to each other and to module accounts), escrows
// created, released and refunded, treasury-spend proposals, deposits and
// votes, block rewards, and time moving on through the app's own
// EndBlocker (escrow expiry, proposal resolution, the fee sweep). After
// every step every invariant (invariants.go) must hold, and nothing may
// panic. FuzzAetherMsgHandlers feeds single messages to an empty chain;
// this reaches the states where money moves (docs/REVIEW.md).
//
//	go test ./app -run '^$' -fuzz '^FuzzMoneySequences$' -fuzztime 10m
func FuzzMoneySequences(f *testing.F) {
	w := newMoneyWorld(f)
	for _, seed := range moneySeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, program []byte) {
		_ = w.run(t, program)
	})
}

// The seeds: each a story worth starting mutations from.
func moneySeeds() [][]byte {
	return [][]byte{
		// An escrow created, then left to expire and refund. (Escrow ids
		// start at 1; amount(b) is (b+1)*10^(b%13) base units.)
		{opCreateEscrow, 0, 1, 255, 10, 0, 3, 0, opAdvance, 200, opAdvance, 200},
		// An escrow released by its payer, then released and refunded again.
		{opCreateEscrow, 1, 2, 255, 20, 1, 9, 1, opRelease, 1, 1, opRelease, 1, 1, opRefund, 2, 1},
		// An escrow with an arbiter who refunds it.
		{opCreateEscrow, 2, 3, 1, 20, 1, 5, 0, opRefund, 1, 1, opRelease, 2, 1},
		// A treasury spend: rewards fund the treasury, a proposal gets its
		// deposit, all four validators vote yes, the voting period ends.
		{opReward, 0, 200, opReward, 1, 200, opAdvance, 1, opPropose, 0, 3, 1, 32,
			opVote, 0, 0, 1, opVote, 0, 1, 1, opVote, 0, 2, 1, opVote, 0, 3, 1, opAdvance, 100, opAdvance, 100},
		// A proposal vetoed, its deposit burned.
		{opPropose, 1, 2, 1, 32, opVote, 0, 0, 4, opVote, 0, 1, 4, opVote, 0, 2, 4, opAdvance, 100, opAdvance, 100},
		// A proposal left short of its deposit to expire.
		{opPropose, 2, 0, 1, 1, opAdvance, 255, opAdvance, 255, opAdvance, 255},
		// Money sent straight to module accounts, then a block ends.
		{opSend, 0, 4, 9, opSend, 1, 5, 9, opSend, 2, 6, 9, opSend, 3, 7, 9, opAdvance, 1},
		// Rewards to validators (escrowed) and their release much later.
		{opReward, 0, 50, opReward, 3, 50, opAdvance, 255, opAdvance, 255, opAdvance, 255},
		// An escrow of two denoms, released by the arbiter.
		{opCreateEscrow, 0, 3, 2, 40, 2, 9, 1, opRelease, 2, 1},
		// Two proposals at once, each with deposits from several people.
		{opPropose, 0, 1, 2, 5, opPropose, 1, 2, 3, 5, opDeposit, 0, 2, 32, opDeposit, 1, 3, 32, opDeposit, 1, 0, 5, opAdvance, 255, opAdvance, 255},
	}
}

const (
	opSend byte = iota
	opCreateEscrow
	opRelease
	opRefund
	opPropose
	opDeposit
	opVote
	opAdvance
	opReward
	opCount
)

const (
	seqChainID = gates.RehearsalPrefix + "seqfuzz"
	seqDenom2  = "ibc/0000000000000000000000000000000000000000000000000000000000000001"
	seqPeople  = 4
)

type moneyWorld struct {
	app    *App
	base   sdk.Context
	people []sdk.AccAddress
	// Module accounts a send may target: x/escrow, x/governance,
	// x/treasury, x/pow.
	modules []sdk.AccAddress
}

func newMoneyWorld(tb testing.TB) *moneyWorld {
	tb.Helper()
	app := newChainApp(tb, dbm.NewMemDB(), seqChainID)
	genesisOn(tb, app, seqChainID)
	finalizeAndCommit(tb, app, 1)
	finalizeAndCommit(tb, app, 2)
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: seqChainID, Height: 3, Time: testGenesisTime.Add(3 * time.Minute)})

	w := &moneyWorld{app: app}
	for i := 0; i < seqPeople; i++ {
		addr := sdk.AccAddress(fmt.Sprintf("seqfuzz_person_%05d", i))
		w.people = append(w.people, addr)
		coins := sdk.NewCoins(sdk.NewInt64Coin("uaeth", 1_000_000_000_000), sdk.NewInt64Coin(seqDenom2, 1_000_000_000))
		if err := app.BankKeeper.MintCoins(ctx, pow.ModuleName, coins); err != nil {
			tb.Fatal(err)
		}
		if err := app.BankKeeper.SendCoinsFromModuleToAccount(ctx, pow.ModuleName, addr, coins); err != nil {
			tb.Fatal(err)
		}
		app.PowKeeper.SetActiveValidator(ctx, addr)
	}
	for _, m := range []string{escrow.ModuleName, governance.ModuleName, treasury.ModuleName, pow.ModuleName} {
		w.modules = append(w.modules, authtypes.NewModuleAddress(m))
	}
	if err := app.CheckInvariants(ctx); err != nil {
		tb.Fatalf("invariants broken before any step: %v", err)
	}
	w.base = ctx
	return w
}

// reader reads a program; past its end it reads zeros.
type reader struct {
	b []byte
	i int
}

func (r *reader) byte() byte {
	if r.i >= len(r.b) {
		r.i++
		return 0
	}
	r.i++
	return r.b[r.i-1]
}
func (r *reader) done() bool { return r.i >= len(r.b) }

func (w *moneyWorld) person(b byte) sdk.AccAddress { return w.people[int(b)%seqPeople] }

// target is a person or, for b >= seqPeople, a module account.
func (w *moneyWorld) target(b byte) sdk.AccAddress {
	n := int(b) % (seqPeople + len(w.modules))
	if n < seqPeople {
		return w.people[n]
	}
	return w.modules[n-seqPeople]
}

// amount spans 1 uaeth to beyond anyone's balance: b * 10^(b%13).
func amount(b byte) sdkmath.Int {
	return sdkmath.NewInt(int64(b) + 1).Mul(sdkmath.NewIntWithDecimal(1, int(b)%13))
}

func (w *moneyWorld) run(t *testing.T, program []byte) []string {
	ctx, _ := w.base.CacheContext()
	r := &reader{b: program}
	var trace []string
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("panic after %s: %v", strings.Join(trace, "; "), p)
		}
	}()
	router := w.app.MsgServiceRouter()
	exec := func(msg sdk.Msg) string {
		h := router.Handler(msg)
		if h == nil {
			t.Fatalf("no handler for %T", msg)
		}
		cache, write := ctx.CacheContext()
		if _, err := h(cache, msg); err != nil {
			return "refused"
		}
		write()
		return "ok"
	}
	for steps := 0; !r.done() && steps < 64; steps++ {
		op := r.byte() % opCount
		var step string
		switch op {
		case opSend:
			from, to, amt := w.person(r.byte()), w.target(r.byte()), amount(r.byte())
			denom := "uaeth"
			if amt.Int64()%3 == 0 {
				denom = seqDenom2
			}
			step = "send " + exec(&banktypes.MsgSend{FromAddress: from.String(), ToAddress: to.String(), Amount: sdk.NewCoins(sdk.NewCoin(denom, amt))})
		case opCreateEscrow:
			payer, payee := w.person(r.byte()), w.person(r.byte())
			arb := r.byte()
			amt := amount(r.byte())
			coins := sdk.NewCoins(sdk.NewCoin("uaeth", amt))
			if k := r.byte(); k%3 == 2 {
				coins = coins.Add(sdk.NewCoin(seqDenom2, amount(k)))
			}
			minutes := int64(r.byte())
			onExpiry := escrow.ON_EXPIRY_REFUND
			if r.byte()%2 == 1 {
				onExpiry = escrow.ON_EXPIRY_RELEASE
			}
			msg := &escrow.MsgCreateEscrow{
				Payer: payer.String(), Payee: payee.String(), Amount: coins, OnExpiry: onExpiry,
				ExpiresAt: ctx.BlockTime().Add(escrow.MinDuration + time.Duration(minutes)*time.Minute).Unix(),
			}
			if arb < 255 {
				msg.Arbiter = w.person(arb).String()
			}
			step = "escrow " + exec(msg)
		case opRelease, opRefund:
			sender, id := w.person(r.byte()), uint64(r.byte()%8)
			if op == opRelease {
				step = "release " + exec(&escrow.MsgReleaseEscrow{Sender: sender.String(), Id: id})
			} else {
				step = "refund " + exec(&escrow.MsgRefundEscrow{Sender: sender.String(), Id: id})
			}
		case opPropose:
			proposer, recipient := w.person(r.byte()), w.target(r.byte())
			spend := amount(r.byte()).String()
			if r.i%2 == 0 {
				spend += seqDenom2 // a spend of the treasury's other denom
			}
			deposit := amount(r.byte()).String()
			step = "propose " + exec(&governance.MsgSubmitProposal{Proposer: proposer.String(), Recipient: recipient.String(), Amount: spend, Deposit: deposit})
		case opDeposit:
			id := uint64(r.byte()%4) + 1
			depositor, amt := w.person(r.byte()), amount(r.byte())
			step = "deposit " + exec(&governance.MsgDeposit{ProposalId: id, Depositor: depositor.String(), Amount: amt.String()})
		case opVote:
			id := uint64(r.byte()%4) + 1
			voter, option := w.person(r.byte()), governance.VoteOption(r.byte()%5)
			step = "vote " + exec(&governance.MsgVote{ProposalId: id, Voter: voter.String(), Option: option})
		case opAdvance:
			// Up to about three weeks at once, past the voting and deposit periods.
			hours := time.Duration(r.byte())*2 + 1
			ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(hours * time.Hour))
			if _, err := w.app.EndBlocker(ctx); err != nil {
				t.Fatalf("EndBlocker after %s: %v", strings.Join(trace, "; "), err)
			}
			step = "advance " + strconv.Itoa(int(hours)) + "h"
		case opReward:
			miner, amt := w.person(r.byte()), amount(r.byte())
			if err := w.app.PowKeeper.DistributeReward(ctx, miner, amt); err != nil {
				step = "reward refused"
			} else {
				step = "reward ok"
			}
		}
		trace = append(trace, step)
		if err := w.app.CheckInvariants(ctx); err != nil {
			t.Fatalf("invariant broken after %s: %v", strings.Join(trace, "; "), err)
		}
	}
	return trace
}

// The seeds do what they say: money moves, proposals resolve and escrows
// settle, not just refusals (a fuzz corpus of refusals would test little).
func TestMoneySequences_SeedsReachRealStates(t *testing.T) {
	w := newMoneyWorld(t)
	for i, seed := range moneySeeds() {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			trace := w.run(t, seed)
			t.Log(strings.Join(trace, "; "))
			if !strings.Contains(strings.Join(trace, ";"), " ok") {
				t.Fatalf("seed %d did nothing but get refused: %v", i, trace)
			}
		})
	}

	// The treasury-spend story: the spend is paid out.
	ctx, _ := w.base.CacheContext()
	recipient := w.people[3]
	before := w.app.BankKeeper.GetBalance(ctx, recipient, "uaeth").Amount
	for _, a := range []sdkmath.Int{amount(200), amount(200)} {
		if err := w.app.PowKeeper.DistributeReward(ctx, w.people[0], a); err != nil {
			t.Fatal(err)
		}
	}
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(time.Hour))
	if _, err := w.app.EndBlocker(ctx); err != nil {
		t.Fatal(err)
	}
	ledger := w.app.TreasuryKeeper.GetTreasuryBalance(ctx)
	if !ledger.IsPositive() {
		t.Fatalf("rewards should fund the treasury, ledger %s", ledger)
	}
	h := w.app.MsgServiceRouter()
	spend := sdkmath.NewInt(1000)
	run := func(msg sdk.Msg) {
		t.Helper()
		if _, err := h.Handler(msg)(ctx, msg); err != nil {
			t.Fatalf("%T: %v", msg, err)
		}
	}
	run(&governance.MsgSubmitProposal{Proposer: w.people[0].String(), Recipient: recipient.String(), Amount: spend.String(), Deposit: "25000000"})
	for _, p := range w.people {
		run(&governance.MsgVote{ProposalId: 1, Voter: p.String(), Option: governance.VoteOption_VOTE_OPTION_YES})
	}
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(8 * 24 * time.Hour))
	if _, err := w.app.EndBlocker(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := w.app.GovernanceKeeper.GetProposal(ctx, 1)
	if p.Status != governance.ProposalStatus_PROPOSAL_STATUS_PASSED {
		t.Fatalf("proposal status %s", p.Status)
	}
	after := w.app.BankKeeper.GetBalance(ctx, recipient, "uaeth").Amount
	if !after.Sub(before).Equal(spend) {
		t.Fatalf("recipient got %s, want %s", after.Sub(before), spend)
	}
	if err := w.app.CheckInvariants(ctx); err != nil {
		t.Fatal(err)
	}
}
