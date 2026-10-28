package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// Key rotation (docs/PQ-KEY-ROTATION.md): after an account rotates its
// key, the new key signs for an account whose address isn't the key's
// own. The wallet keeps that pairing in SignsForFile, next to the
// keyring, so GetAccount, ListAccounts and everything built on them
// (agentmcp, the faucet, the paywall, powminer) send from the account.
// Signing itself needs nothing else: a transaction's signer is in its
// messages, and the chain checks the signature against the key stored
// on the account.

// SignsForFile is where a keyring directory records which account each
// rotated key signs for.
const SignsForFile = "signs-for.json"

func (w *Wallet) loadSignsFor(backend, rootDir string) error {
	w.signsFor = map[string]string{}
	if backend == keyring.BackendMemory || rootDir == "" {
		return nil
	}
	w.signsForFile = filepath.Join(rootDir, SignsForFile)
	records, err := ReadSignsFor(rootDir)
	if err != nil {
		return err
	}
	w.signsFor = records
	return nil
}

// ReadSignsFor reads keyringDir's record of which account each rotated
// key signs for (key name to account address); empty if there is none.
// aetherd reads it too, so `aetherd tx ... --from <key>` sends from the
// account.
func ReadSignsFor(keyringDir string) (map[string]string, error) {
	records := map[string]string{}
	path := filepath.Join(keyringDir, SignsForFile)
	bz, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := json.Unmarshal(bz, &records); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, account := range records {
		if _, err := sdk.AccAddressFromBech32(account); err != nil {
			return nil, fmt.Errorf("%s: %s signs for %q: %w", path, name, account, err)
		}
	}
	return records, nil
}

// SignFor records that the named key signs for account (its key was
// rotated onto it); an empty account, or the key's own address, removes
// the record.
func (w *Wallet) SignFor(name, account string) error {
	record, err := w.kr.Key(name)
	if err != nil {
		return err
	}
	own, err := record.GetAddress()
	if err != nil {
		return err
	}
	if account == "" || account == own.String() {
		delete(w.signsFor, name)
	} else {
		if _, err := sdk.AccAddressFromBech32(account); err != nil {
			return fmt.Errorf("account %q: %w", account, err)
		}
		w.signsFor[name] = account
	}
	return w.saveSignsFor()
}

func (w *Wallet) saveSignsFor() error {
	if w.signsForFile == "" {
		return nil
	}
	bz, err := json.MarshalIndent(w.signsFor, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.signsForFile + ".tmp"
	if err := os.WriteFile(tmp, bz, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.signsForFile)
}

// RotateKeyMsg is MsgRotateKey for account, to the named key in this
// wallet, with that key's proof of possession at nonce (the account's
// rotation state nonce: Client.Rotation).
func (w *Wallet) RotateKeyMsg(chainID, account, newKeyName string, nonce uint64) (*accountauth.MsgRotateKey, error) {
	pub, sig, err := w.newKeyProof(chainID, account, newKeyName, nonce)
	if err != nil {
		return nil, err
	}
	return &accountauth.MsgRotateKey{Account: account, NewPubkey: pub, NewKeySignature: sig}, nil
}

// RecoverKeyMsg is MsgRecoverKey for account, to the named key in this
// wallet, with that key's proof at recoveryNonce and the guardians'
// signatures over accountauth.RecoverKeySigningBytes at the same nonce.
func (w *Wallet) RecoverKeyMsg(chainID, submitter, account, newKeyName string, recoveryNonce uint64, guardians []*accountauth.GuardianSignature) (*accountauth.MsgRecoverKey, error) {
	pub, sig, err := w.newKeyProof(chainID, account, newKeyName, recoveryNonce)
	if err != nil {
		return nil, err
	}
	return &accountauth.MsgRecoverKey{Submitter: submitter, Account: account, NewPubkey: pub, NewKeySignature: sig, GuardianSignatures: guardians}, nil
}

func (w *Wallet) newKeyProof(chainID, account, newKeyName string, nonce uint64) (pub, sig []byte, err error) {
	acc, err := w.GetAccount(newKeyName)
	if err != nil {
		return nil, nil, err
	}
	if len(acc.PubKey) != mldsa.PubKeySize {
		return nil, nil, fmt.Errorf("%s isn't an ML-DSA-44 key", newKeyName)
	}
	sig, _, err = w.SignBytes(newKeyName, accountauth.RotateKeySigningBytes(chainID, account, nonce, acc.PubKey))
	if err != nil {
		return nil, nil, err
	}
	return acc.PubKey, sig, nil
}

// GuardianSign is the named guardian key's signature over signingBytes
// (accountauth.RecoverKeySigningBytes or GuardianCancelSigningBytes).
func (w *Wallet) GuardianSign(guardianName string, signingBytes []byte) (*accountauth.GuardianSignature, error) {
	sig, pub, err := w.SignBytes(guardianName, signingBytes)
	if err != nil {
		return nil, err
	}
	return &accountauth.GuardianSignature{Pubkey: pub, Signature: sig}, nil
}

// Rotation is the account's rotation record and current key.
func (c *Client) Rotation(ctx context.Context, account string) (*accountauth.QueryRotationResponse, error) {
	return accountauth.NewQueryClient(c.conn).Rotation(ctx, &accountauth.QueryRotationRequest{Account: account})
}

// AccountPubKey is the ML-DSA-44 key stored on the account, which signs
// for it: after a rotation not the key its address derives from. Nil if
// the account has signed nothing yet or doesn't exist.
func (c *Client) AccountPubKey(ctx context.Context, address string) ([]byte, error) {
	resp, err := c.authClient.AccountInfo(ctx, &authtypes.QueryAccountInfoRequest{Address: address})
	if status.Code(err) == codes.NotFound {
		return nil, nil // no account yet, so no key
	}
	if err != nil {
		return nil, err
	}
	if resp.Info == nil || resp.Info.PubKey == nil {
		return nil, nil
	}
	var pk mldsa.PubKey
	if err := pk.Unmarshal(resp.Info.PubKey.Value); err != nil {
		return nil, fmt.Errorf("account %s: key type %s: %w", address, resp.Info.PubKey.TypeUrl, err)
	}
	return pk.Key, nil
}
