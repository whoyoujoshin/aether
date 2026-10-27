package main

import (
	"context"
	"fmt"

	"cosmossdk.io/math"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/wallet"
)

// get_inflow_caps: the tokens other chains may send onto Aether, and how
// much room each has left (x/inflowcap, docs/IBC-INFLOW-CAP.md). A
// transfer over the room isn't queued: it's refused, and the sending
// chain refunds it once the refusal is relayed back.

// inflowChain is what reads the caps; grpcChain implements it.
type inflowChain interface {
	inflowCaps() (wallet.InflowCaps, error)
}

func (g grpcChain) inflowCaps() (wallet.InflowCaps, error) {
	ctx, cancel := escrowCtx()
	defer cancel()
	return g.c.InflowCaps(ctx)
}

const getInflowCapsDescription = "List the tokens other chains may send onto Aether over IBC, each with its channel, the cap on how much of it may be on Aether, " +
	"how much is there now, how much is in flight back out and the room left. A transfer of any other token, or over the room, is refused and the sending chain refunds it: " +
	"check the room before asking anyone to send USDC (or anything else) onto Aether. Read-only. active=false means this chain has no cap yet."

type getInflowCapsInput struct{}

type inflowCapDTO struct {
	Channel      string `json:"channel" jsonschema:"Aether's end of the channel the token must come over"`
	Denom        string `json:"denom" jsonschema:"the token as the other chain sends it"`
	VoucherDenom string `json:"voucherDenom" jsonschema:"what it arrives as on Aether"`
	Symbol       string `json:"symbol,omitempty" jsonschema:"USDC or OSMO when this server knows the token"`
	Cap          string `json:"cap" jsonschema:"the most of it there may be on Aether, in base units"`
	Supply       string `json:"supply" jsonschema:"how much is on Aether now, in base units"`
	InFlight     string `json:"inFlight" jsonschema:"sent back from Aether and not yet settled, in base units; it holds room until its packet is acknowledged or times out"`
	Room         string `json:"room" jsonschema:"how much more may come in now, in base units; a transfer over this is refused and refunded on the sending chain"`
	RoomDisplay  string `json:"roomDisplay,omitempty" jsonschema:"room in whole units, e.g. 4990 USDC"`
}

type getInflowCapsOutput struct {
	Active  bool           `json:"active" jsonschema:"false: this chain has no inflow cap yet and every token comes in uncapped"`
	Caps    []inflowCapDTO `json:"caps"`
	Message string         `json:"message"`
}

func toolGetInflowCaps(ctx context.Context, _ *mcp.CallToolRequest, _ getInflowCapsInput) (*mcp.CallToolResult, getInflowCapsOutput, error) {
	out, err := getInflowCaps()
	return nil, out, err
}

func getInflowCaps() (getInflowCapsOutput, error) {
	out := getInflowCapsOutput{Caps: []inflowCapDTO{}}
	ic, err := readInflowCaps()
	if err != nil {
		return out, err
	}
	out.Active = ic.Active
	if !ic.Active {
		out.Message = "This chain has no inflow cap yet: tokens from other chains come in uncapped."
		return out, nil
	}
	for _, c := range ic.Caps {
		d := inflowCapDTO{Channel: c.Channel, Denom: c.Denom, VoucherDenom: c.VoucherDenom,
			Cap: c.Cap.String(), Supply: c.Supply.String(), InFlight: c.InFlight.String(), Room: c.Room.String()}
		if sym := inflowSymbol(c.VoucherDenom); sym != "" {
			d.Symbol, d.RoomDisplay = sym, formatMicro(c.Room.BigInt())+" "+sym
		}
		out.Caps = append(out.Caps, d)
	}
	out.Message = "Only these tokens may come onto Aether, each over its channel and up to its room. Any other token from another chain, " +
		"or a transfer over the room, is refused, and the sending chain refunds it once the refusal is relayed back (seconds to minutes). " +
		"Check the room just before asking anyone to send: others' transfers use it too."
	return out, nil
}

func readInflowCaps() (wallet.InflowCaps, error) {
	c, err := dialChain()
	if err != nil {
		return wallet.InflowCaps{}, err
	}
	defer c.close()
	ic, ok := c.(inflowChain)
	if !ok {
		return wallet.InflowCaps{}, newError(codeInternal, "this chain connection can't read inflow caps")
	}
	caps, err := ic.inflowCaps()
	if err != nil {
		return wallet.InflowCaps{}, newError(codeNodeUnreachable, err.Error())
	}
	return caps, nil
}

// inflowSymbol names the tokens this server knows: its USDC, and OSMO
// from the swap pool. Both have 6 decimals.
func inflowSymbol(voucher string) string {
	if voucher == osmoOnAether {
		return "OSMO"
	}
	if a, ok := assets.ByDenom(voucher); ok && a.Decimals == 6 {
		return a.Symbol
	}
	return ""
}

// checkSwapReturnRoom refuses a swap whose OSMO couldn't come back onto
// Aether: OSMO not listed, or more of it than the room left. Otherwise
// the return transfer is refused on arrival and the OSMO waits in the
// swap contract as owed (contracts/aether-swap) until someone re-sends
// it. expected is the quote's output, or minOutput without one.
func checkSwapReturnRoom(c chain, expected math.Int) error {
	ic, ok := c.(inflowChain)
	if !ok {
		return nil
	}
	caps, err := ic.inflowCaps()
	if err != nil {
		return newError(codeNodeUnreachable, err.Error())
	}
	room, limited, listed := caps.Room(osmoOnAether)
	switch {
	case !limited:
		return nil
	case !listed:
		return newError(codeInflowCapped, "OSMO isn't on Aether's inflow allowlist (get_inflow_caps), so the swapped OSMO couldn't come back onto Aether: nothing was sent")
	case expected.GT(room):
		return newError(codeInflowCapped, fmt.Sprintf("this swap would bring back about %s OSMO, but Aether's inflow cap leaves room for %s OSMO now (get_inflow_caps): swap less. Nothing was sent",
			formatMicro(expected.BigInt()), formatMicro(room.BigInt())))
	}
	return nil
}

// swapExpected is what a swap should bring back: the quote's estimate,
// or minOut without a quote.
func swapExpected(quote *quoteSwapOutput, minOut math.Int) math.Int {
	if quote != nil {
		if v, ok := math.NewIntFromString(quote.Out.Uosmo); ok {
			return v
		}
	}
	return minOut
}
