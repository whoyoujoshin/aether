package cli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txsigning "github.com/cosmos/cosmos-sdk/types/tx/signing"

	"github.com/whoyoujoshin/aether/x/accountauth"
)

func NewTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        accountauth.ModuleName,
		Short:                      "Pluggable account-abstraction transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		NewRegisterSessionKeyCmd(),
		NewRegisterGuardianThresholdCmd(),
		NewRevokeAuthenticatorCmd(),
		NewExecAuthenticatedCmd(),
		NewSignGuardianExecCmd(),
	)

	return cmd
}

func NewRegisterSessionKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register-session-key [pubkey-hex] [expires-at-unix] [spend-limit-uaeth] [allowed-msg-type...]",
		Short: "Register a session key that can act on your account until it expires",
		Long: `Register a session key: a secondary ML-DSA-44 keypair that can execute a
bounded set of messages on your behalf without ever handling your
account's own primary key.

[pubkey-hex] is the session key's raw ML-DSA-44 public key, hex-encoded
(e.g. from "aetherd keys show <name> -p" or a fresh keypair generated
off-chain for this purpose).

[expires-at-unix] is a Unix timestamp; the session key stops working at
or after this time.

[spend-limit-uaeth] is the total uaeth this key may ever move across
every bank send it executes, lifetime (a plain integer, not
denom-suffixed).

[allowed-msg-type...] is one or more Msg type URLs this key may execute,
e.g. /cosmos.bank.v1beta1.MsgSend -- any other message type is rejected.

Signed by --from, your account's own primary key.`,
		Args: cobra.MinimumNArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			pubkey, err := hex.DecodeString(args[0])
			if err != nil {
				return fmt.Errorf("invalid pubkey-hex: %w", err)
			}
			expiresAtUnix, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid expires-at-unix: %w", err)
			}

			msg := &accountauth.MsgRegisterAuthenticator{
				Account: clientCtx.GetFromAddress().String(),
				Authenticator: &accountauth.Authenticator{
					Kind: &accountauth.Authenticator_SessionKey{SessionKey: &accountauth.SessionKey{
						Pubkey:          pubkey,
						ExpiresAtUnix:   expiresAtUnix,
						SpendLimitUaeth: args[2],
						AllowedMsgTypes: args[3:],
					}},
				},
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func NewRegisterGuardianThresholdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register-guardian-threshold [threshold] [guardian-pubkey-hex...]",
		Short: "Register M-of-N guardians who can jointly authorize actions on your account",
		Long: `Register a guardian threshold: [threshold] of the listed guardians'
ML-DSA-44 signatures, jointly, can authorize a message on your account's
behalf -- e.g. social recovery, without any guardian needing a funded
account of their own.

[threshold] must be between 1 and the number of guardian pubkeys given.

Signed by --from, your account's own primary key.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			threshold, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return fmt.Errorf("invalid threshold: %w", err)
			}

			pubkeys := make([][]byte, len(args[1:]))
			for i, h := range args[1:] {
				pk, err := hex.DecodeString(h)
				if err != nil {
					return fmt.Errorf("invalid guardian-pubkey-hex at position %d: %w", i, err)
				}
				pubkeys[i] = pk
			}

			msg := &accountauth.MsgRegisterAuthenticator{
				Account: clientCtx.GetFromAddress().String(),
				Authenticator: &accountauth.Authenticator{
					Kind: &accountauth.Authenticator_GuardianThreshold{GuardianThreshold: &accountauth.GuardianThreshold{
						GuardianPubkeys: pubkeys,
						Threshold:       uint32(threshold),
					}},
				},
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func NewRevokeAuthenticatorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke [id]",
		Short: "Revoke a previously-registered authenticator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}

			msg := &accountauth.MsgRevokeAuthenticator{
				Account: clientCtx.GetFromAddress().String(),
				Id:      id,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// parseMsgsJSONFile reads a JSON array of registered sdk.Msg values
// (each including its own "@type" field, the same proto-JSON convention
// x/governance's submit-param-change-proposal uses for a single
// message) and packs each into an Any, in file order.
func parseMsgsJSONFile(clientCtx client.Context, path string) ([]*codectypes.Any, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read msgs-json-file %q: %w", path, err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(contents, &raw); err != nil {
		return nil, fmt.Errorf("msgs-json-file must be a JSON array of messages: %w", err)
	}

	anys := make([]*codectypes.Any, len(raw))
	for i, r := range raw {
		var sdkMsg sdk.Msg
		if err := clientCtx.Codec.UnmarshalInterfaceJSON(r, &sdkMsg); err != nil {
			return nil, fmt.Errorf("message %d is not a registered sdk.Msg: %w", i, err)
		}
		any, err := codectypes.NewAnyWithValue(sdkMsg)
		if err != nil {
			return nil, fmt.Errorf("could not pack message %d: %w", i, err)
		}
		anys[i] = any
	}
	return anys, nil
}

func NewExecAuthenticatedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec-authenticated [account] [authenticator-id] [msgs-json-file]",
		Short: "Execute messages on account's behalf via a registered authenticator",
		Long: `Execute one or more messages on [account]'s behalf, authorized by the
authenticator [authenticator-id] it registered.

[msgs-json-file] must be a JSON array of messages, each in proto JSON
form including its own "@type" field, e.g.:

  [{"@type":"/cosmos.bank.v1beta1.MsgSend","from_address":"<account>","to_address":"...","amount":[{"denom":"uaeth","amount":"100"}]}]

Every message must itself be signed (its own signer field) as [account]
-- that's what the authenticator is authorizing, not who submits this
tx.

For a session-key authenticator: sign this tx with --from set to the
session key's own local keyring entry; its own tx signature IS the
proof, checked the normal way.

For a guardian-threshold authenticator: --from may be anyone (e.g. a
relayer with no stake in the outcome); attach each guardian's signature
with --guardian-sig pubkey-hex:signature-hex (repeatable), produced
by "sign-guardian-exec".`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			authenticatorID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid authenticator-id: %w", err)
			}

			msgs, err := parseMsgsJSONFile(clientCtx, args[2])
			if err != nil {
				return err
			}

			guardianSigStrs, err := cmd.Flags().GetStringArray("guardian-sig")
			if err != nil {
				return err
			}
			guardianSigs := make([]*accountauth.GuardianSignature, len(guardianSigStrs))
			for i, s := range guardianSigStrs {
				pubHex, sigHex, ok := strings.Cut(s, ":")
				if !ok {
					return fmt.Errorf("--guardian-sig %q must be pubkey-hex:signature-hex", s)
				}
				pub, err := hex.DecodeString(pubHex)
				if err != nil {
					return fmt.Errorf("--guardian-sig %q: invalid pubkey hex: %w", s, err)
				}
				sig, err := hex.DecodeString(sigHex)
				if err != nil {
					return fmt.Errorf("--guardian-sig %q: invalid signature hex: %w", s, err)
				}
				guardianSigs[i] = &accountauth.GuardianSignature{Pubkey: pub, Signature: sig}
			}

			msg := &accountauth.MsgExecAuthenticated{
				Signer:             clientCtx.GetFromAddress().String(),
				Account:            args[0],
				AuthenticatorId:    authenticatorID,
				Msgs:               msgs,
				GuardianSignatures: guardianSigs,
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().StringArray("guardian-sig", nil, "a guardian signature as pubkey-hex:signature-hex (repeatable; only for a guardian-threshold authenticator)")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// NewSignGuardianExecCmd lets one guardian produce their own signature
// over a pending exec offline (or by any relayer collecting them),
// without needing to submit anything themselves. It queries the
// authenticator's current on-chain next_sequence -- the same
// replay-protection value the exec itself will be checked against --
// rather than trusting a caller-supplied one, so a stale local copy
// can't silently produce a signature that fails once the real exec
// lands.
func NewSignGuardianExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign-guardian-exec [account] [authenticator-id] [msgs-json-file]",
		Short: "Produce this guardian's signature for a pending exec, ready to paste into --guardian-sig",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			authenticatorID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid authenticator-id: %w", err)
			}

			queryClient := accountauth.NewQueryClient(clientCtx)
			res, err := queryClient.Authenticators(context.Background(), &accountauth.QueryAuthenticatorsRequest{Account: args[0]})
			if err != nil {
				return fmt.Errorf("could not fetch account's authenticators: %w", err)
			}
			var gt *accountauth.GuardianThreshold
			for _, a := range res.Authenticators {
				if a.Id == authenticatorID {
					g, ok := a.Kind.(*accountauth.Authenticator_GuardianThreshold)
					if !ok {
						return fmt.Errorf("authenticator %d is not a guardian threshold", authenticatorID)
					}
					gt = g.GuardianThreshold
					break
				}
			}
			if gt == nil {
				return fmt.Errorf("account %s has no authenticator %d", args[0], authenticatorID)
			}

			msgs, err := parseMsgsJSONFile(clientCtx, args[2])
			if err != nil {
				return err
			}

			signingBytes := accountauth.GuardianExecSigningBytes(clientCtx.ChainID, args[0], authenticatorID, gt.NextSequence, msgs)

			uid := clientCtx.GetFromName()
			sig, pubKey, err := clientCtx.Keyring.Sign(uid, signingBytes, txsigning.SignMode_SIGN_MODE_DIRECT)
			if err != nil {
				return fmt.Errorf("could not sign with local key %q: %w", uid, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s:%s\n", hex.EncodeToString(pubKey.Bytes()), hex.EncodeToString(sig))
			return nil
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
