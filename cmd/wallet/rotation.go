package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// Key rotation (docs/PQ-KEY-ROTATION.md). An account keeps its address
// and changes the key that signs for it: by its current key after
// RotationDelayBlocks, or at once by its recovery guardians.

func rotationCmds() []*cobra.Command {
	return []*cobra.Command{
		newPubkeyCmd(),
		newRotationCmd(),
		newRotateKeyCmd(),
		newCancelRotationCmd(),
		newSetRecoveryCmd(),
		newGuardianSignCmd(),
		newRecoverKeyCmd(),
		newGuardianCancelCmd(),
		newSignForCmd(),
	}
}

func keyHash(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// sendMsg signs msg with keyName for account and broadcasts it.
func sendMsg(w *wallet.Wallet, client *wallet.Client, keyName, account string, msg sdk.Msg) error {
	number, sequence, err := client.GetAccountInfo(account)
	if err != nil {
		return fmt.Errorf("account %s (does it exist on chain yet?): %w", account, err)
	}
	signed, err := w.BuildAndSignMsgTx(keyName, msg, wallet.TxParams{
		ChainID: chainID, AccountNumber: number, Sequence: sequence, GasLimit: 400_000,
		Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.ZeroInt())),
	})
	if err != nil {
		return err
	}
	res, err := client.BroadcastTx(signed)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("refused (code %d): %s", res.Code, res.RawLog)
	}
	fmt.Printf("Tx hash: %s\n", res.TxHash)
	return nil
}

func withClient(f func(w *wallet.Wallet, c *wallet.Client) error) error {
	w, err := newWallet()
	if err != nil {
		return err
	}
	c, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		return err
	}
	defer c.Close()
	return f(w, c)
}

// guardianKey is a hex public key, or the name of a key in this keyring.
func guardianKey(w *wallet.Wallet, s string) ([]byte, error) {
	if bz, err := hex.DecodeString(s); err == nil && len(bz) == mldsa.PubKeySize {
		return bz, nil
	}
	acc, err := w.GetAccount(s)
	if err != nil {
		return nil, fmt.Errorf("%q is neither a %d-byte hex key nor a key in this keyring", s, mldsa.PubKeySize)
	}
	return acc.PubKey, nil
}

func newPubkeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pubkey <name>",
		Short: "Print a key's ML-DSA-44 public key in hex (for set-recovery and guardian-sign)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWallet()
			if err != nil {
				return err
			}
			acc, err := w.GetAccount(args[0])
			if err != nil {
				return err
			}
			fmt.Println(hex.EncodeToString(acc.PubKey))
			return nil
		},
	}
}

func newRotationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotation <name-or-address>",
		Short: "Show an account's key, recovery set and pending changes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			account, err := resolveAddress(args[0])
			if err != nil {
				return err
			}
			return withClient(func(_ *wallet.Wallet, c *wallet.Client) error {
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				st := q.State
				fmt.Printf("Account:        %s\n", account)
				fmt.Printf("Rotation:       %s (delay %d blocks)\n", map[bool]string{true: "active", false: "not active yet"}[q.Active], q.DelayBlocks)
				if len(q.CurrentPubkey) == 0 {
					fmt.Println("Current key:    none on chain (the account hasn't signed yet)")
				} else {
					fmt.Printf("Current key:    sha256 %s…\n", keyHash(q.CurrentPubkey))
				}
				fmt.Printf("Nonce:          %d (current key's proofs)\n", st.GetNonce())
				fmt.Printf("Recovery nonce: %d (guardian signatures)\n", st.GetRecoveryNonce())
				if r := st.GetRecovery(); r != nil {
					fmt.Printf("Recovery:       %d of %d guardians\n", r.Threshold, len(r.GuardianPubkeys))
					for _, g := range r.GuardianPubkeys {
						fmt.Printf("                sha256 %s…\n", keyHash(g))
					}
				} else {
					fmt.Println("Recovery:       none")
				}
				if p := st.GetPendingRotation(); p != nil {
					fmt.Printf("Pending key:    sha256 %s… at block %d\n", keyHash(p.NewPubkey), p.ApplyHeight)
				}
				if p := st.GetPendingRecovery(); p != nil {
					if p.Recovery == nil || len(p.Recovery.GuardianPubkeys) == 0 {
						fmt.Printf("Pending:        recovery set removed at block %d\n", p.ApplyHeight)
					} else {
						fmt.Printf("Pending:        recovery set %d of %d at block %d\n", p.Recovery.Threshold, len(p.Recovery.GuardianPubkeys), p.ApplyHeight)
					}
				}
				return nil
			})
		},
	}
}

func newRotateKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-key <name> <new-key-name>",
		Short: "Schedule the account's key to change to another key in this keyring (applies after the delay)",
		Long: "Signs with <name>'s current key. The change applies about a day later (RotationDelayBlocks); " +
			"until then the current key or the guardians can cancel it. Once it applies, run " +
			"`wallet sign-for <new-key-name> <account>` so the new key sends from the account.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				acc, err := w.GetAccount(args[0])
				if err != nil {
					return err
				}
				q, err := c.Rotation(context.Background(), acc.Address)
				if err != nil {
					return err
				}
				msg, err := w.RotateKeyMsg(chainID, acc.Address, args[1], q.State.GetNonce())
				if err != nil {
					return err
				}
				if err := sendMsg(w, c, args[0], acc.Address, msg); err != nil {
					return err
				}
				fmt.Printf("Scheduled: %s's key becomes %s's about %d blocks after this lands (`wallet rotation %s`).\n", acc.Address, args[1], q.DelayBlocks, acc.Address)
				fmt.Printf("Then: wallet sign-for %s %s\n", args[1], acc.Address)
				return nil
			})
		},
	}
}

func newCancelRotationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel-rotation <name>",
		Short: "Cancel the account's pending key and recovery-set changes, signed by its current key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				acc, err := w.GetAccount(args[0])
				if err != nil {
					return err
				}
				return sendMsg(w, c, args[0], acc.Address, &accountauth.MsgCancelPendingRotation{Account: acc.Address})
			})
		},
	}
}

func newSetRecoveryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-recovery <name> <threshold> [guardian]...",
		Short: "Schedule the account's recovery set: threshold of the guardians may replace its key at once",
		Long: "Each guardian is a hex public key (`wallet pubkey`) or a key name in this keyring. " +
			"Threshold 0 with no guardians removes the set. The change applies after the delay, like a key rotation.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			threshold, err := strconv.ParseUint(args[1], 10, 32)
			if err != nil {
				return fmt.Errorf("threshold %q: %w", args[1], err)
			}
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				acc, err := w.GetAccount(args[0])
				if err != nil {
					return err
				}
				msg := &accountauth.MsgSetRecovery{Account: acc.Address, Threshold: uint32(threshold)}
				for _, g := range args[2:] {
					pub, err := guardianKey(w, g)
					if err != nil {
						return err
					}
					msg.GuardianPubkeys = append(msg.GuardianPubkeys, pub)
				}
				return sendMsg(w, c, args[0], acc.Address, msg)
			})
		},
	}
}

// guardianSigFile is one guardian's signature, passed to whoever submits.
type guardianSigFile struct {
	Account   string `json:"account"`
	Action    string `json:"action"`
	Nonce     uint64 `json:"recovery_nonce"`
	NewPubkey string `json:"new_pubkey,omitempty"`
	Pubkey    string `json:"pubkey"`
	Signature string `json:"signature"`
}

func newGuardianSignCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "guardian-sign <guardian-name> <account> (recover <new-pubkey> | cancel)",
		Short: "Sign, as a guardian, a recovery or a cancel for an account (at its current recovery nonce)",
		Args:  cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			guardian, account, action := args[0], args[1], args[2]
			if _, err := sdk.AccAddressFromBech32(account); err != nil {
				return fmt.Errorf("account %q: %w", account, err)
			}
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				nonce := q.State.GetRecoveryNonce()
				f := guardianSigFile{Account: account, Action: action, Nonce: nonce}
				var signBytes []byte
				switch {
				case action == "recover" && len(args) == 4:
					pub, err := guardianKey(w, args[3])
					if err != nil {
						return err
					}
					f.NewPubkey = hex.EncodeToString(pub)
					signBytes = accountauth.RecoverKeySigningBytes(chainID, account, nonce, pub)
				case action == "cancel" && len(args) == 3:
					signBytes = accountauth.GuardianCancelSigningBytes(chainID, account, nonce)
				default:
					return fmt.Errorf("want `recover <new-pubkey>` or `cancel`")
				}
				sig, err := w.GuardianSign(guardian, signBytes)
				if err != nil {
					return err
				}
				f.Pubkey, f.Signature = hex.EncodeToString(sig.Pubkey), hex.EncodeToString(sig.Signature)
				bz, err := json.MarshalIndent(f, "", "  ")
				if err != nil {
					return err
				}
				if out == "" {
					fmt.Println(string(bz))
					return nil
				}
				return os.WriteFile(out, bz, 0o600)
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write the signature to this file instead of printing it")
	return cmd
}

// readGuardianSigs loads guardian-sign files for account and action at
// nonce, refusing any made for something else.
func readGuardianSigs(paths []string, account, action string, nonce uint64, newPub []byte) ([]*accountauth.GuardianSignature, error) {
	var sigs []*accountauth.GuardianSignature
	for _, p := range paths {
		bz, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var f guardianSigFile
		if err := json.Unmarshal(bz, &f); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if f.Account != account || f.Action != action {
			return nil, fmt.Errorf("%s is a %s for %s, not a %s for %s", p, f.Action, f.Account, action, account)
		}
		if f.Nonce != nonce {
			return nil, fmt.Errorf("%s was signed at recovery nonce %d; the account is at %d: ask that guardian to sign again", p, f.Nonce, nonce)
		}
		if newPub != nil && f.NewPubkey != hex.EncodeToString(newPub) {
			return nil, fmt.Errorf("%s approves a different new key", p)
		}
		pub, err := hex.DecodeString(f.Pubkey)
		if err != nil {
			return nil, fmt.Errorf("%s: pubkey: %w", p, err)
		}
		sig, err := hex.DecodeString(f.Signature)
		if err != nil {
			return nil, fmt.Errorf("%s: signature: %w", p, err)
		}
		sigs = append(sigs, &accountauth.GuardianSignature{Pubkey: pub, Signature: sig})
	}
	return sigs, nil
}

func newRecoverKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recover-key <submitter-name> <account> <new-key-name> <guardian-sig.json>...",
		Short: "Replace an account's key at once with its guardians' signatures; the new key is in this keyring",
		Long: "<submitter-name> signs and pays for the transaction (any account with an on-chain key). " +
			"On success this wallet records that <new-key-name> signs for <account>.",
		Args: cobra.MinimumNArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			submitterName, account, newKey := args[0], args[1], args[2]
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				submitter, err := w.GetAccount(submitterName)
				if err != nil {
					return err
				}
				newAcc, err := w.GetAccount(newKey)
				if err != nil {
					return err
				}
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				nonce := q.State.GetRecoveryNonce()
				sigs, err := readGuardianSigs(args[3:], account, "recover", nonce, newAcc.PubKey)
				if err != nil {
					return err
				}
				msg, err := w.RecoverKeyMsg(chainID, submitter.Address, account, newKey, nonce, sigs)
				if err != nil {
					return err
				}
				if err := sendMsg(w, c, submitterName, submitter.Address, msg); err != nil {
					return err
				}
				if err := w.SignFor(newKey, account); err != nil {
					return fmt.Errorf("recovered, but recording it failed (run `wallet sign-for %s %s`): %w", newKey, account, err)
				}
				fmt.Printf("Recovered once it's in a block: %s now signs for %s.\n", newKey, account)
				return nil
			})
		},
	}
}

func newGuardianCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guardian-cancel <submitter-name> <account> <guardian-sig.json>...",
		Short: "Cancel an account's pending changes with its guardians' signatures",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			submitterName, account := args[0], args[1]
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				submitter, err := w.GetAccount(submitterName)
				if err != nil {
					return err
				}
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				sigs, err := readGuardianSigs(args[2:], account, "cancel", q.State.GetRecoveryNonce(), nil)
				if err != nil {
					return err
				}
				return sendMsg(w, c, submitterName, submitter.Address, &accountauth.MsgGuardianCancel{
					Submitter: submitter.Address, Account: account, GuardianSignatures: sigs,
				})
			})
		},
	}
}

func newSignForCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sign-for <name> <account>",
		Short: "Record that a key, rotated onto an account, signs for it (its own address removes the record)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := newWallet()
			if err != nil {
				return err
			}
			if err := w.SignFor(args[0], args[1]); err != nil {
				return err
			}
			acc, err := w.GetAccount(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s sends from %s.\n", args[0], acc.Address)
			return nil
		},
	}
}
