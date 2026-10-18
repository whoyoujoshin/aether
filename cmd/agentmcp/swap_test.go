package main

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
)

// swapFake is fakeChain plus the far side of the swap: each transfer to
// the contract is "swapped" for poolOut uosmo, and pays it back to the
// sender, if that clears the memo's min_output, as contracts/aether-swap
// does; otherwise it is acknowledged with an error, as Osmosis would.
type swapFake struct {
	*fakeChain
	mu        sync.Mutex
	poolOut   int64 // what the fake pool pays for any transfer
	inFlight  bool  // leave packets unacknowledged
	nextSeq   uint64
	sent      map[string]uint64 // tx hash -> packet sequence
	outcomes  map[uint64]*wallet.PacketOutcome
	transfers []*transfertypes.MsgTransfer
}

func setupSwap(t *testing.T) (*swapFake, string) {
	t.Helper()
	f := setupAgent(t)
	f.autoInclude = true
	f.balances = map[string]sdk.Coins{}
	s := &swapFake{fakeChain: f, poolOut: 394472, nextSeq: 7, sent: map[string]uint64{}, outcomes: map[uint64]*wallet.PacketOutcome{}}
	dialChain = func() (chain, error) { return s, nil }
	o := newFakeOsmosis(t)
	useOsmosis(t, o.URL)
	prev := osmosisSwapContract
	osmosisSwapContract = ""
	t.Cleanup(func() { osmosisSwapContract = prev })
	w, err := newWallet()
	require.NoError(t, err)
	acc, err := getOrCreateAgentAccount(w)
	require.NoError(t, err)
	return s, acc.Address
}

func (s *swapFake) broadcast(signed wallet.SignedTx) (wallet.BroadcastResult, error) {
	res, err := s.fakeChain.broadcast(signed)
	if err != nil || res.Code != 0 {
		return res, err
	}
	tx, derr := app.MakeEncodingConfig().TxConfig.TxDecoder()(signed.Bytes)
	if derr != nil {
		return res, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range tx.GetMsgs() {
		m, ok := m.(*transfertypes.MsgTransfer)
		if !ok {
			continue
		}
		s.transfers = append(s.transfers, m)
		// The chain shows the transfer into the channel's escrow account.
		s.fakeChain.mu.Lock()
		if d := s.fakeChain.blocks[res.TxHash]; d != nil {
			d.From, d.Transfers = m.Sender, append(d.Transfers, wallet.Transfer{From: m.Sender, To: "escrow", Amount: m.Token.String()})
		}
		s.fakeChain.mu.Unlock()
		seq := s.nextSeq
		s.nextSeq++
		s.sent[res.TxHash] = seq
		if s.inFlight {
			s.outcomes[seq] = &wallet.PacketOutcome{Status: wallet.PacketInFlight}
			continue
		}
		s.settle(m, seq)
	}
	return res, err
}

func (s *swapFake) settle(m *transfertypes.MsgTransfer, seq uint64) {
	var memo struct {
		Wasm struct {
			Contract string `json:"contract"`
			Msg      struct {
				SwapAndReturn struct {
					MinOutput string `json:"min_output"`
					Recipient string `json:"recipient"`
				} `json:"swap_and_return"`
			} `json:"msg"`
		} `json:"wasm"`
	}
	_ = json.Unmarshal([]byte(m.Memo), &memo)
	min, _ := strconv.ParseInt(memo.Wasm.Msg.SwapAndReturn.MinOutput, 10, 64)
	ack := "ACK" + strconv.FormatUint(seq, 10)
	if s.poolOut < min || memo.Wasm.Contract != m.Receiver {
		s.outcomes[seq] = &wallet.PacketOutcome{Status: wallet.PacketFailed, TxHash: ack,
			Error: "ABCI code: 6: error handling packet: see events for details"}
		return
	}
	s.outcomes[seq] = &wallet.PacketOutcome{Status: wallet.PacketAcked, TxHash: ack}
	s.fakeChain.mu.Lock()
	to := memo.Wasm.Msg.SwapAndReturn.Recipient
	s.fakeChain.balances[to] = s.fakeChain.balances[to].Add(sdk.NewInt64Coin(osmoOnAether, s.poolOut))
	s.fakeChain.mu.Unlock()
}

func (s *swapFake) sentPacketSequence(hash, ch string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch != testnetOsmosisChannel {
		return 0, newError(codeInternal, "wrong channel "+ch)
	}
	return s.sent[hash], nil
}

func (s *swapFake) sentPacketOutcome(ch string, seq uint64) (*wallet.PacketOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outcomes[seq], nil
}

func swap(t *testing.T, in swapAethInput) (swapAethOutput, error) {
	t.Helper()
	if in.WaitSeconds == 0 {
		in.WaitSeconds = 5
	}
	_, out, err := toolSwapAeth(context.Background(), nil, in)
	return out, err
}

func TestSwapAeth_SwapsAndReceivesOSMO(t *testing.T) {
	s, agent := setupSwap(t)

	out, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "swap-1"})
	require.NoError(t, err)
	require.Equal(t, statusSwapped, out.Status, out.Message)
	require.Equal(t, "394472", out.Received.Uosmo)
	require.Equal(t, "390527", out.MinOutput.Uosmo) // the quote's 394,472 less 1%
	require.Equal(t, uint64(7), out.PacketSequence)
	require.Equal(t, "ACK7", out.AckTxHash)
	require.NotNil(t, out.Quote)
	require.Equal(t, "1.38", out.Quote.CostPercent)

	require.Len(t, s.transfers, 1)
	m := s.transfers[0]
	require.Equal(t, "transfer", m.SourcePort)
	require.Equal(t, "channel-3", m.SourceChannel)
	require.Equal(t, "1000000uaeth", m.Token.String())
	require.Equal(t, agent, m.Sender)
	// ibc-hooks runs the memo only when the receiver is the contract.
	require.Equal(t, testnetSwapContract, m.Receiver)
	require.JSONEq(t, `{"wasm":{"contract":"`+testnetSwapContract+`","msg":{"swap_and_return":{"min_output":"390527","recipient":"`+agent+`"}}}}`, m.Memo)
	require.NotZero(t, m.TimeoutTimestamp)
	require.EqualValues(t, 1_000_000, spent(t))

	// The same key follows the same swap: nothing is sent again.
	again, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "swap-1"})
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, statusSwapped, again.Status)
	require.Equal(t, out.TxHash, again.TxHash)
	require.Len(t, s.transfers, 1)

	_, err = swap(t, swapAethInput{Amount: "0.5 AETH", IdempotencyKey: "swap-1"})
	requireCode(t, err, codeIdempotencyConflict)
}

func TestSwapAeth_RefundedBelowMinOutput(t *testing.T) {
	s, _ := setupSwap(t)
	s.poolOut = 380000 // the pool moved after the quote

	out, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "swap-low"})
	require.NoError(t, err)
	require.Equal(t, statusSwapRefunded, out.Status)
	require.Contains(t, out.Error, "error handling packet")
	require.Nil(t, out.Received)
	// The AETH came back, so it no longer counts against the budget.
	require.EqualValues(t, 0, spent(t))
}

func TestSwapAeth_InFlightThenFollowedUp(t *testing.T) {
	s, _ := setupSwap(t)
	s.inFlight = true

	out, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "swap-slow", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusSwapInFlight, out.Status)
	require.Equal(t, uint64(7), out.PacketSequence)

	// Osmosis acknowledges it later; calling again with the key finds out.
	s.mu.Lock()
	s.settle(s.transfers[0], 7)
	s.mu.Unlock()
	out, err = swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "swap-slow"})
	require.NoError(t, err)
	require.Equal(t, statusSwapped, out.Status)
	require.Equal(t, "394472", out.Received.Uosmo)
	require.Len(t, s.transfers, 1)
}

func TestSwapAeth_Refusals(t *testing.T) {
	t.Run("cost too high", func(t *testing.T) {
		s, _ := setupSwap(t)
		o := newFakeOsmosis(t)
		o.estimate = `{"token_out_amount":"380000"}` // 5% below spot
		useOsmosis(t, o.URL)
		out, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "k"})
		requireCode(t, err, codeSwapCostTooHigh)
		require.Equal(t, "5.00", out.Quote.CostPercent)
		require.Empty(t, s.transfers)

		// The agent may accept it knowingly.
		out, err = swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "k2", MaxCostPercent: 6})
		require.NoError(t, err)
		require.Len(t, s.transfers, 1)
		_ = out
	})

	t.Run("over the per-payment limit", func(t *testing.T) {
		s, _ := setupSwap(t)
		_, err := swap(t, swapAethInput{Amount: "2 AETH", IdempotencyKey: "k"})
		requireCode(t, err, codePerTxLimit)
		require.Empty(t, s.transfers)
	})

	t.Run("no quote and no minOutput", func(t *testing.T) {
		s, agent := setupSwap(t)
		useOsmosis(t, "off")
		_, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "k"})
		requireCode(t, err, codeSwapNotConfigured)
		require.Empty(t, s.transfers)

		// With an explicit minOutput it doesn't need one.
		out, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "k3", MinOutput: "390000uosmo"})
		require.NoError(t, err)
		require.Equal(t, statusSwapped, out.Status)
		require.Nil(t, out.Quote)
		require.JSONEq(t, `{"wasm":{"contract":"`+testnetSwapContract+`","msg":{"swap_and_return":{"min_output":"390000","recipient":"`+agent+`"}}}}`, s.transfers[0].Memo)
	})

	t.Run("bad input", func(t *testing.T) {
		s, _ := setupSwap(t)
		for _, c := range []struct {
			in   swapAethInput
			code string
		}{
			{swapAethInput{Amount: "1 AETH"}, codeInvalidArgument}, // no key
			{swapAethInput{Amount: "5 USDC", IdempotencyKey: "k"}, codeInvalidAmount},
			{swapAethInput{Amount: "1", IdempotencyKey: "k"}, codeInvalidAmount},
			{swapAethInput{Amount: "1 AETH", IdempotencyKey: "k", MinOutput: "-5"}, codeInvalidAmount},
			{swapAethInput{Amount: "1 AETH", IdempotencyKey: "k", MaxCostPercent: -1}, codeInvalidArgument},
		} {
			_, err := swap(t, c.in)
			requireCode(t, err, c.code)
		}
		require.Empty(t, s.transfers)
	})

	t.Run("grant mode", func(t *testing.T) {
		s, _ := setupSwap(t)
		granter = sdk.AccAddress("the_granter_________").String()
		t.Cleanup(func() { granter = "" })
		_, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "k"})
		requireCode(t, err, codeInvalidArgument)
		require.Empty(t, s.transfers)
	})

	t.Run("off", func(t *testing.T) {
		s, _ := setupSwap(t)
		osmosisSwapContract = "off"
		_, err := swap(t, swapAethInput{Amount: "1 AETH", IdempotencyKey: "k"})
		requireCode(t, err, codeSwapNotConfigured)
		require.Empty(t, s.transfers)
	})
}

func TestSwapMemo(t *testing.T) {
	require.Equal(t, `{"wasm":{"contract":"osmo1c","msg":{"swap_and_return":{"min_output":"5","recipient":"aether1r"}}}}`, swapMemo("osmo1c", "5", "aether1r"))
	_ = math.ZeroInt()
}

func setupSandboxSwap(t *testing.T) (*swapFake, string, string) {
	t.Helper()
	sb, f := newTestSandbox(t)
	f.autoInclude = true
	f.balances = map[string]sdk.Coins{}
	s := &swapFake{fakeChain: f, poolOut: 394472, nextSeq: 7, sent: map[string]uint64{}, outcomes: map[uint64]*wallet.PacketOutcome{}}
	dialChain = func() (chain, error) { return s, nil }
	o := newFakeOsmosis(t)
	useOsmosis(t, o.URL)
	h, entropy, err := sb.newHandle()
	require.NoError(t, err)
	_, address, err := walletFromEntropy(entropy)
	require.NoError(t, err)
	return s, h, address
}

func sandboxSwap(t *testing.T, in swapFromTestWalletInput) (swapAethOutput, error) {
	t.Helper()
	if in.WaitSeconds == 0 {
		in.WaitSeconds = 5
	}
	_, out, err := toolSwapFromTestWallet(context.Background(), nil, in)
	return out, err
}

func TestSwapFromTestWallet(t *testing.T) {
	s, token, address := setupSandboxSwap(t)

	out, err := sandboxSwap(t, swapFromTestWalletInput{WalletToken: token, Amount: "1 AETH"})
	require.NoError(t, err)
	require.Equal(t, statusSwapped, out.Status, out.Message)
	require.Equal(t, "394472", out.Received.Uosmo)
	require.Equal(t, "390527", out.MinOutput.Uosmo)
	require.Len(t, s.transfers, 1)
	require.Equal(t, address, s.transfers[0].Sender)
	require.Equal(t, testnetSwapContract, s.transfers[0].Receiver)
	require.JSONEq(t, `{"wasm":{"contract":"`+testnetSwapContract+`","msg":{"swap_and_return":{"min_output":"390527","recipient":"`+address+`"}}}}`, s.transfers[0].Memo)

	// Refunded when the pool can't clear minOutput.
	s.poolOut = 380000
	out, err = sandboxSwap(t, swapFromTestWalletInput{WalletToken: token, Amount: "1 AETH"})
	require.NoError(t, err)
	require.Equal(t, statusSwapRefunded, out.Status)
	require.NotEmpty(t, out.Error)
}

func TestSwapFromTestWallet_FollowUp(t *testing.T) {
	s, token, _ := setupSandboxSwap(t)
	s.inFlight = true
	out, err := sandboxSwap(t, swapFromTestWalletInput{WalletToken: token, Amount: "1 AETH", WaitSeconds: 1})
	require.NoError(t, err)
	require.Equal(t, statusSwapInFlight, out.Status)

	s.mu.Lock()
	s.settle(s.transfers[0], out.PacketSequence)
	s.mu.Unlock()
	again, err := sandboxSwap(t, swapFromTestWalletInput{WalletToken: token, TxHash: out.TxHash})
	require.NoError(t, err)
	require.Equal(t, statusSwapped, again.Status)
	require.Equal(t, "394472", again.Received.Uosmo)
	require.Len(t, s.transfers, 1, "following up sends nothing")

	// Only the wallet that sent it may follow it up, and not with an amount too.
	other, _, err := sbx.newHandle()
	require.NoError(t, err)
	_, err = sandboxSwap(t, swapFromTestWalletInput{WalletToken: other, TxHash: out.TxHash})
	requireCode(t, err, codeInvalidArgument)
	_, err = sandboxSwap(t, swapFromTestWalletInput{WalletToken: token, TxHash: out.TxHash, Amount: "1 AETH"})
	requireCode(t, err, codeInvalidArgument)
}
