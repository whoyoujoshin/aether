package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/inflowcap"
)

const (
	flagSet          = "set"
	flagRemove       = "remove"
	flagCounterparty = "counterparty-chain-id"
)

func NewTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        inflowcap.ModuleName,
		Short:                      "IBC inflow cap transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(NewDraftUpdateParamsCmd())
	return cmd
}

// NewDraftUpdateParamsCmd writes a MsgUpdateParams for a governance
// param-change proposal: the live list with the flagged changes, so a
// change to one entry never drops another (the message replaces the
// whole list).
//
// A channel the list doesn't name yet is checked on the chain first:
// IBC channels are permissionless and numbered first come, first served,
// so an entry is only as good as the channel it names. It must be an
// open transfer channel whose light client follows the chain ID given
// with --counterparty-chain-id. A light client of a look-alike chain can
// carry the same chain ID, so the command also prints the client and
// the other end for voters to check against the counterparty itself.
func NewDraftUpdateParamsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft-update-params [output-file]",
		Short: "Write a MsgUpdateParams JSON file from the live allowlist with entries set or removed",
		Long: `Write a MsgUpdateParams JSON file ready to hand to
"tx governance submit-param-change-proposal". It starts from the live
allowlist and applies the flags:

  --set <channel>,<denom>,<cap>   list a token, or change its cap
  --remove <channel>,<denom>      stop listing a token (refused from then on)

<denom> is the token as the other chain sends it (Injective's USDC is
erc20:0x...), and <cap> is in its smallest unit (5,000 USDC at 6
decimals is 5000000000). Listing over a channel the list doesn't name
yet needs --counterparty-chain-id <channel>=<chain-id>; every channel
given one is checked on the chain first, listed or not. For example, on aether-1 once the
Injective path is open and checked:

  aetherd tx inflowcap draft-update-params usdc.json \
    --set channel-0,erc20:0xa00C59fF5a080D2b954d0c75e46E22a0c371235a,5000000000 \
    --counterparty-chain-id channel-0=injective-1

then:

  aetherd tx governance submit-param-change-proposal usdc.json <deposit> --from <key> ...`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			sets, _ := cmd.Flags().GetStringArray(flagSet)
			removes, _ := cmd.Flags().GetStringArray(flagRemove)
			cparty, _ := cmd.Flags().GetStringToString(flagCounterparty)
			if len(sets) == 0 && len(removes) == 0 {
				return fmt.Errorf("nothing to change: pass --%s or --%s", flagSet, flagRemove)
			}

			live, err := inflowcap.NewQueryClient(clientCtx).Params(cmd.Context(), &inflowcap.QueryParamsRequest{})
			if err != nil {
				return fmt.Errorf("could not fetch the live allowlist: %w", err)
			}
			params, added, err := Apply(live.Params, sets, removes)
			if err != nil {
				return err
			}
			for _, ch := range added {
				if _, ok := cparty[ch]; !ok {
					return fmt.Errorf("%s isn't listed yet: pass --%s %s=<chain-id> so the channel is checked before it's listed", ch, flagCounterparty, ch)
				}
			}
			// Every channel given a chain ID is checked, listed or not.
			for _, ch := range sortedKeys(cparty) {
				info, err := CheckChannel(cmd.Context(), channeltypes.NewQueryClient(clientCtx), clientCtx, ch, cparty[ch])
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), info)
			}

			msg := &inflowcap.MsgUpdateParams{
				Authority: authtypes.NewModuleAddress(governance.ModuleName).String(),
				Params:    params,
			}
			bz, err := clientCtx.Codec.MarshalInterfaceJSON(msg)
			if err != nil {
				return err
			}
			if err := os.WriteFile(args[0], bz, 0o644); err != nil {
				return fmt.Errorf("could not write %s: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d entries). Submit it with:\n  aetherd tx governance submit-param-change-proposal %s <deposit> --from <key> --chain-id <chain-id>\n",
				args[0], len(params.Entries), args[0])
			return nil
		},
	}
	cmd.Flags().StringArray(flagSet, nil, "<channel>,<denom>,<cap>: list a token, or change its cap (repeatable)")
	cmd.Flags().StringArray(flagRemove, nil, "<channel>,<denom>: stop listing a token (repeatable)")
	cmd.Flags().StringToString(flagCounterparty, nil, "<channel>=<chain-id>: for each channel not yet listed, the chain its light client must follow")
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Apply is the list with each set (channel,denom,cap) and remove
// (channel,denom) applied in order, checked as the chain checks it, and
// the channels it names that live didn't.
func Apply(live inflowcap.Params, sets, removes []string) (inflowcap.Params, []string, error) {
	entries := append([]inflowcap.Entry(nil), live.Entries...)
	known := map[string]bool{}
	for _, e := range entries {
		known[e.ChannelId] = true
	}
	find := func(ch, denom string) int {
		for i, e := range entries {
			if e.ChannelId == ch && e.VoucherDenom() == inflowcap.VoucherDenom(ch, denom) {
				return i
			}
		}
		return -1
	}
	for _, s := range sets {
		parts := strings.Split(s, ",")
		if len(parts) != 3 {
			return inflowcap.Params{}, nil, fmt.Errorf("--%s %q: want <channel>,<denom>,<cap>", flagSet, s)
		}
		capAmount, ok := sdkmath.NewIntFromString(parts[2])
		if !ok {
			return inflowcap.Params{}, nil, fmt.Errorf("--%s %q: cap %q isn't a whole number of the token's smallest unit", flagSet, s, parts[2])
		}
		e := inflowcap.Entry{ChannelId: parts[0], Denom: parts[1], Cap: capAmount}
		if i := find(e.ChannelId, e.Denom); i >= 0 {
			entries[i] = e
		} else {
			entries = append(entries, e)
		}
	}
	for _, r := range removes {
		parts := strings.Split(r, ",")
		if len(parts) != 2 {
			return inflowcap.Params{}, nil, fmt.Errorf("--%s %q: want <channel>,<denom>", flagRemove, r)
		}
		i := find(parts[0], parts[1])
		if i < 0 {
			return inflowcap.Params{}, nil, fmt.Errorf("--%s %q: not listed", flagRemove, r)
		}
		entries = append(entries[:i], entries[i+1:]...)
	}
	p := inflowcap.Params{Entries: entries}
	if err := p.Validate(); err != nil {
		return inflowcap.Params{}, nil, err
	}
	var added []string
	seen := map[string]bool{}
	for _, e := range entries {
		if !known[e.ChannelId] && !seen[e.ChannelId] {
			added = append(added, e.ChannelId)
			seen[e.ChannelId] = true
		}
	}
	return p, added, nil
}

// CheckChannel checks channel is an open transfer channel whose light
// client follows chainID, and describes it for voters.
func CheckChannel(ctx context.Context, q channeltypes.QueryClient, clientCtx client.Context, channel, chainID string) (string, error) {
	ch, err := q.Channel(ctx, &channeltypes.QueryChannelRequest{PortId: inflowcap.PortID, ChannelId: channel})
	if err != nil {
		return "", fmt.Errorf("%s: no transfer channel by that ID: %w", channel, err)
	}
	if ch.Channel == nil || ch.Channel.State != channeltypes.OPEN {
		return "", fmt.Errorf("%s isn't open", channel)
	}
	cs, err := q.ChannelClientState(ctx, &channeltypes.QueryChannelClientStateRequest{PortId: inflowcap.PortID, ChannelId: channel})
	if err != nil {
		return "", fmt.Errorf("%s: its client: %w", channel, err)
	}
	if cs.IdentifiedClientState == nil {
		return "", fmt.Errorf("%s: no client state", channel)
	}
	var state ibcexported.ClientState
	if err := clientCtx.InterfaceRegistry.UnpackAny(cs.IdentifiedClientState.ClientState, &state); err != nil {
		return "", fmt.Errorf("%s: its client state: %w", channel, err)
	}
	tm, ok := state.(*ibctm.ClientState)
	if !ok {
		return "", fmt.Errorf("%s: its client is a %s, not 07-tendermint", channel, state.ClientType())
	}
	if tm.ChainId != chainID {
		return "", fmt.Errorf("%s: its client %s follows %q, not %q", channel, cs.IdentifiedClientState.ClientId, tm.ChainId, chainID)
	}
	return fmt.Sprintf("%s: open, connection %s, client %s of %s (latest height %s); the other end is %s/%s. Check that end on %s itself before voting.",
		channel, strings.Join(ch.Channel.ConnectionHops, ","), cs.IdentifiedClientState.ClientId, tm.ChainId, tm.LatestHeight,
		ch.Channel.Counterparty.PortId, ch.Channel.Counterparty.ChannelId, tm.ChainId), nil
}
