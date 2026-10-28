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
		newGuardianSetRecoveryCmd(),
		newWatchtowerSignCmd(),
		newWatchtowerCancelCmd(),
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
					for _, wt := range r.WatchtowerPubkeys {
						fmt.Printf("Watchtower:     sha256 %s… (cancel-only)\n", keyHash(wt))
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
	var watchtowers []string
	cmd := &cobra.Command{
		Use:   "set-recovery <name> <threshold> [guardian]... [--watchtower <key>]...",
		Short: "Schedule the account's recovery set: threshold of the guardians may replace its key at once",
		Long: "Each guardian is a hex public key (`wallet pubkey`) or a key name in this keyring. " +
			"Threshold 0 with no guardians removes the set. The change applies after the delay, like a key rotation. " +
			"A --watchtower key (at most 3, only with guardians) may cancel changes the current key schedules, and nothing else; " +
			"the guardians can remove it at once (guardian-set-recovery).",
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
				if msg.WatchtowerPubkeys, err = keyList(w, watchtowers); err != nil {
					return err
				}
				return sendMsg(w, c, args[0], acc.Address, msg)
			})
		},
	}
	cmd.Flags().StringArrayVar(&watchtowers, "watchtower", nil, "a cancel-only watchtower key (hex or key name); repeat for more")
	return cmd
}

func keyList(w *wallet.Wallet, keys []string) ([][]byte, error) {
	var out [][]byte
	for _, k := range keys {
		pub, err := guardianKey(w, k)
		if err != nil {
			return nil, err
		}
		out = append(out, pub)
	}
	return out, nil
}

func hexList(keys [][]byte) []string {
	var out []string
	for _, k := range keys {
		out = append(out, hex.EncodeToString(k))
	}
	return out
}

// guardianSigFile is one guardian's signature, passed to whoever submits.
type guardianSigFile struct {
	Account   string `json:"account"`
	Action    string `json:"action"`
	Nonce     uint64 `json:"recovery_nonce"`
	NewPubkey string `json:"new_pubkey,omitempty"`
	// The new recovery set, for set-recovery.
	Threshold   uint32   `json:"threshold,omitempty"`
	Guardians   []string `json:"guardians,omitempty"`
	Watchtowers []string `json:"watchtowers,omitempty"`
	Pubkey      string   `json:"pubkey"`
	Signature   string   `json:"signature"`
}

func newGuardianSignCmd() *cobra.Command {
	var out string
	var watchtowers []string
	cmd := &cobra.Command{
		Use:   "guardian-sign <guardian-name> <account> (recover <new-pubkey> | cancel | set-recovery <threshold> [guardian]... [--watchtower <key>]...)",
		Short: "Sign, as a guardian, a recovery, a cancel or a new recovery set for an account (at its current recovery nonce)",
		Args:  cobra.MinimumNArgs(3),
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
				case action == "set-recovery" && len(args) >= 4:
					threshold, err := strconv.ParseUint(args[3], 10, 32)
					if err != nil {
						return fmt.Errorf("threshold %q: %w", args[3], err)
					}
					set := accountauth.RecoverySet{Threshold: uint32(threshold)}
					if set.GuardianPubkeys, err = keyList(w, args[4:]); err != nil {
						return err
					}
					if set.WatchtowerPubkeys, err = keyList(w, watchtowers); err != nil {
						return err
					}
					f.Threshold, f.Guardians, f.Watchtowers = set.Threshold, hexList(set.GuardianPubkeys), hexList(set.WatchtowerPubkeys)
					signBytes = accountauth.GuardianSetRecoverySigningBytes(chainID, account, nonce, set)
				default:
					return fmt.Errorf("want `recover <new-pubkey>`, `cancel` or `set-recovery <threshold> [guardian]...`")
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
	cmd.Flags().StringArrayVar(&watchtowers, "watchtower", nil, "set-recovery: a watchtower key in the new set (hex or key name)")
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

func newGuardianSetRecoveryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guardian-set-recovery <submitter-name> <account> <guardian-sig.json>...",
		Short: "Replace an account's recovery set at once with its guardians' signatures (removes a watchtower)",
		Long: "Each file is a `guardian-sign ... set-recovery` for the same new set. A watchtower can't cancel this: " +
			"it is how the guardians remove one that misbehaves.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			submitterName, account := args[0], args[1]
			var first guardianSigFile
			for i, p := range args[2:] {
				bz, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				var f guardianSigFile
				if err := json.Unmarshal(bz, &f); err != nil {
					return fmt.Errorf("%s: %w", p, err)
				}
				if i == 0 {
					first = f
				} else if f.Threshold != first.Threshold || fmt.Sprint(f.Guardians) != fmt.Sprint(first.Guardians) || fmt.Sprint(f.Watchtowers) != fmt.Sprint(first.Watchtowers) {
					return fmt.Errorf("%s approves a different set than %s", p, args[2])
				}
			}
			set := accountauth.RecoverySet{Threshold: first.Threshold}
			for _, l := range []struct {
				in  []string
				out *[][]byte
			}{{first.Guardians, &set.GuardianPubkeys}, {first.Watchtowers, &set.WatchtowerPubkeys}} {
				for _, h := range l.in {
					k, err := hex.DecodeString(h)
					if err != nil {
						return fmt.Errorf("%s: key %q: %w", args[2], h, err)
					}
					*l.out = append(*l.out, k)
				}
			}
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				submitter, err := w.GetAccount(submitterName)
				if err != nil {
					return err
				}
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				sigs, err := readGuardianSigs(args[2:], account, "set-recovery", q.State.GetRecoveryNonce(), nil)
				if err != nil {
					return err
				}
				return sendMsg(w, c, submitterName, submitter.Address, &accountauth.MsgGuardianSetRecovery{
					Submitter: submitter.Address, Account: account, GuardianPubkeys: set.GuardianPubkeys, Threshold: set.Threshold,
					WatchtowerPubkeys: set.WatchtowerPubkeys, GuardianSignatures: sigs,
				})
			})
		},
	}
}

// watchtowerSigFile is a watchtower's cancel, at the account's nonce.
type watchtowerSigFile struct {
	Account   string `json:"account"`
	Action    string `json:"action"` // "watchtower-cancel"
	Nonce     uint64 `json:"nonce"`
	Pubkey    string `json:"pubkey"`
	Signature string `json:"signature"`
}

func newWatchtowerSignCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "watchtower-sign <watchtower-name> <account>",
		Short: "Sign, as a watchtower, a cancel of the account's pending changes (at its current nonce)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, account := args[0], args[1]
			if _, err := sdk.AccAddressFromBech32(account); err != nil {
				return fmt.Errorf("account %q: %w", account, err)
			}
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				nonce := q.State.GetNonce()
				sig, pub, err := w.SignBytes(name, accountauth.WatchtowerCancelSigningBytes(chainID, account, nonce))
				if err != nil {
					return err
				}
				bz, err := json.MarshalIndent(watchtowerSigFile{Account: account, Action: "watchtower-cancel", Nonce: nonce,
					Pubkey: hex.EncodeToString(pub), Signature: hex.EncodeToString(sig)}, "", "  ")
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

func newWatchtowerCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watchtower-cancel <submitter-name> <account> <watchtower-sig.json>",
		Short: "Cancel an account's pending changes with one watchtower's signature",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			submitterName, account := args[0], args[1]
			bz, err := os.ReadFile(args[2])
			if err != nil {
				return err
			}
			var f watchtowerSigFile
			if err := json.Unmarshal(bz, &f); err != nil {
				return fmt.Errorf("%s: %w", args[2], err)
			}
			if f.Account != account || f.Action != "watchtower-cancel" {
				return fmt.Errorf("%s is a %s for %s, not a watchtower-cancel for %s", args[2], f.Action, f.Account, account)
			}
			pub, err := hex.DecodeString(f.Pubkey)
			if err != nil {
				return fmt.Errorf("%s: pubkey: %w", args[2], err)
			}
			sig, err := hex.DecodeString(f.Signature)
			if err != nil {
				return fmt.Errorf("%s: signature: %w", args[2], err)
			}
			return withClient(func(w *wallet.Wallet, c *wallet.Client) error {
				submitter, err := w.GetAccount(submitterName)
				if err != nil {
					return err
				}
				q, err := c.Rotation(context.Background(), account)
				if err != nil {
					return err
				}
				if f.Nonce != q.State.GetNonce() {
					return fmt.Errorf("%s was signed at nonce %d; the account is at %d: the watchtower must sign again", args[2], f.Nonce, q.State.GetNonce())
				}
				return sendMsg(w, c, submitterName, submitter.Address, &accountauth.MsgWatchtowerCancel{
					Submitter: submitter.Address, Account: account, WatchtowerPubkey: pub, Signature: sig,
				})
			})
		},
	}
}
