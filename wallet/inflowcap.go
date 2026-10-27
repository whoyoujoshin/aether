package wallet

import (
	"context"
	"fmt"
	"strings"

	"cosmossdk.io/math"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/x/inflowcap"
)

// InflowCap is one token x/inflowcap lets onto Aether over one channel
// (docs/IBC-INFLOW-CAP.md): a transfer that would take Supply over Cap
// is refused, and the sending chain refunds it.
type InflowCap struct {
	Channel      string   // Aether's end, e.g. channel-2
	Denom        string   // the token as the other chain sends it
	VoucherDenom string   // ibc/... on Aether
	Cap          math.Int // base units
	Supply       math.Int
	Room         math.Int // Cap - Supply - sends in flight, or 0
}

// InflowCaps is the chain's allowlist. Active is false where the node
// has no x/inflowcap (the testnet before its activation height): every
// token comes in, uncapped.
type InflowCaps struct {
	Active bool
	Caps   []InflowCap
}

// Room is how much more of voucher may come onto Aether: limited is
// false when nothing caps it (not active), and listed is false when it
// isn't on the allowlist, so every transfer of it is refused.
func (ic InflowCaps) Room(voucher string) (room math.Int, limited, listed bool) {
	if !ic.Active {
		return math.Int{}, false, true
	}
	for _, c := range ic.Caps {
		if c.VoucherDenom == voucher {
			return c.Room, true, true
		}
	}
	return math.ZeroInt(), true, false
}

// InflowCaps reads x/inflowcap's caps with each voucher's supply now.
func (c *Client) InflowCaps(ctx context.Context) (InflowCaps, error) {
	res, err := inflowcap.NewQueryClient(c.conn).Caps(ctx, &inflowcap.QueryCapsRequest{})
	if err != nil {
		if notServed(err) {
			return InflowCaps{}, nil
		}
		return InflowCaps{}, fmt.Errorf("inflow caps: %w", err)
	}
	out := InflowCaps{Active: true, Caps: make([]InflowCap, 0, len(res.Caps))}
	for _, s := range res.Caps {
		out.Caps = append(out.Caps, InflowCap{
			Channel: s.Entry.ChannelId, Denom: s.Entry.Denom, VoucherDenom: s.VoucherDenom,
			Cap: s.Entry.Cap, Supply: s.Supply, Room: s.Room,
		})
	}
	return out, nil
}

// notServed reports a query for a module the node doesn't run: no such
// gRPC service, or (through the node's query router) no such path.
func notServed(err error) bool {
	if status.Code(err) == codes.Unimplemented {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown service") || strings.Contains(msg, "unknown query path")
}
