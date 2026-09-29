package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/x/escrow"
)

const (
	flagArbiter   = "arbiter"
	flagExpiresIn = "expires-in"
	flagExpiresAt = "expires-at"
	flagOnExpiry  = "on-expiry"
	flagTerms     = "terms"
)

func NewTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        escrow.ModuleName,
		Short:                      "Lock money for another account until it's released, refunded or expires",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(NewCreateCmd(), NewReleaseCmd(), NewRefundCmd())
	return cmd
}

// ParseAmount reads coins, also accepting whole or decimal AETH
// ("1.5aeth"), which it turns into uaeth (1 AETH = 1,000,000 uaeth).
func ParseAmount(s string) (sdk.Coins, error) {
	var out sdk.Coins
	for _, part := range strings.Split(s, ",") {
		dec, err := sdk.ParseDecCoin(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(dec.Denom, "aeth") {
			dec = sdk.NewDecCoinFromDec("uaeth", dec.Amount.MulInt64(1_000_000))
		}
		if !dec.Amount.IsInteger() {
			return nil, fmt.Errorf("%s isn't a whole number of %s", part, dec.Denom)
		}
		out = out.Add(sdk.NewCoin(dec.Denom, dec.Amount.TruncateInt()))
	}
	if out.IsZero() {
		return nil, fmt.Errorf("amount %q is zero", s)
	}
	return out, nil
}

func parseOnExpiry(s string) (escrow.OnExpiry, error) {
	switch strings.ToLower(s) {
	case "refund":
		return escrow.ON_EXPIRY_REFUND, nil
	case "release":
		return escrow.ON_EXPIRY_RELEASE, nil
	}
	return 0, fmt.Errorf("--%s must be refund or release, not %q", flagOnExpiry, s)
}

func NewCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [payee] [amount]",
		Short: "Lock [amount] from --from for [payee]",
		Long: `Lock [amount] from your account (--from) for [payee] until you release it,
the payee refunds it, the arbiter settles it, or it expires.

[amount] is coins, e.g. 5000000uaeth or 5aeth (1 AETH = 1,000,000 uaeth).

At the deadline, --on-expiry decides: "refund" returns it to you (the
payee's recourse is the arbiter), "release" pays the payee (yours is the
arbiter). Only you or the arbiter can release it; only the payee or the
arbiter can refund it.`,
		Example: `aetherd tx escrow create aether1payee... 5aeth --expires-in 72h --on-expiry refund --arbiter aether1arb... --terms "invoice #42" --from agent`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			amount, err := ParseAmount(args[1])
			if err != nil {
				return fmt.Errorf("amount: %w", err)
			}
			in, _ := cmd.Flags().GetDuration(flagExpiresIn)
			at, _ := cmd.Flags().GetInt64(flagExpiresAt)
			switch {
			case at != 0 && in != 0:
				return fmt.Errorf("pass --%s or --%s, not both", flagExpiresIn, flagExpiresAt)
			case at == 0 && in == 0:
				return fmt.Errorf("pass --%s (e.g. 72h) or --%s (unix seconds)", flagExpiresIn, flagExpiresAt)
			case in != 0:
				at = time.Now().Add(in).Unix()
			}
			onExpiryFlag, _ := cmd.Flags().GetString(flagOnExpiry)
			onExpiry, err := parseOnExpiry(onExpiryFlag)
			if err != nil {
				return err
			}
			arbiter, _ := cmd.Flags().GetString(flagArbiter)
			terms, _ := cmd.Flags().GetString(flagTerms)
			msg := &escrow.MsgCreateEscrow{
				Payer:     clientCtx.GetFromAddress().String(),
				Payee:     args[0],
				Arbiter:   arbiter,
				Amount:    amount,
				ExpiresAt: at,
				OnExpiry:  onExpiry,
				Terms:     terms,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String(flagArbiter, "", "an account that may release or refund at any time (optional)")
	cmd.Flags().Duration(flagExpiresIn, 0, "deadline from now, e.g. 72h (at least 1m, at most 8760h)")
	cmd.Flags().Int64(flagExpiresAt, 0, "deadline as unix seconds, instead of --expires-in")
	cmd.Flags().String(flagOnExpiry, "", "what happens at the deadline: refund or release (required)")
	cmd.Flags().String(flagTerms, "", "what the money is for, up to 256 bytes")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func settleCmd(use, short string, build func(sender string, id uint64) sdk.Msg) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use + " [id]",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("id: %w", err)
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), build(clientCtx.GetFromAddress().String(), id))
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func NewReleaseCmd() *cobra.Command {
	return settleCmd("release", "Pay the payee (as the payer or the arbiter)", func(sender string, id uint64) sdk.Msg {
		return &escrow.MsgReleaseEscrow{Sender: sender, Id: id}
	})
}

func NewRefundCmd() *cobra.Command {
	return settleCmd("refund", "Return the money to the payer (as the payee or the arbiter)", func(sender string, id uint64) sdk.Msg {
		return &escrow.MsgRefundEscrow{Sender: sender, Id: id}
	})
}
