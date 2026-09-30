package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

// Accounts this wallet holds, and getting one into it: create a new key,
// recover one from its phrase, or bring one over from the keyring aetherd
// and the CLI tools use (--legacy-keyring-dir), so the desktop app lists
// only what its user chose instead of every key ever made on the machine.

// legacyKeyringDir, when set and different from keyringDir, is offered
// as a source of existing keys.
var legacyKeyringDir string

type accountDTO struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func toAccountDTOs(in []wallet.Account) []accountDTO {
	out := make([]accountDTO, 0, len(in))
	for _, a := range in {
		out = append(out, accountDTO{Name: a.Name, Address: a.Address})
	}
	return out
}

func decodeJSON(r *http.Request, v interface{}) error {
	if r.Method != http.MethodPost {
		return fmt.Errorf("use POST")
	}
	return json.NewDecoder(r.Body).Decode(v)
}

// POST /api/accounts/create {"name"} -> the account and its recovery
// phrase, shown this once.
func handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !wallet.ValidAccountName(req.Name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name must be 1-32 letters, digits, - or _"))
		return
	}
	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	acc, mnemonic, err := wal.CreateAccount(req.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": acc.Name, "address": acc.Address, "mnemonic": mnemonic})
}

// POST /api/accounts/recover {"name","mnemonic"}
func handleRecoverAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Mnemonic string `json:"mnemonic"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !wallet.ValidAccountName(req.Name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name must be 1-32 letters, digits, - or _"))
		return
	}
	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	acc, err := wal.ImportAccount(req.Name, strings.Join(strings.Fields(req.Mnemonic), " "))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, accountDTO{Name: acc.Name, Address: acc.Address})
}

func legacyAvailable() bool {
	if legacyKeyringDir == "" || keyringBackend != "test" {
		return false
	}
	a, errA := filepath.Abs(legacyKeyringDir)
	b, errB := filepath.Abs(keyringDir)
	return errA == nil && errB == nil && a != b
}

func walletCodec() codec.Codec {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}

// GET /api/legacy-accounts -> keys in the CLI keyring that could be
// brought over, or [] when there's none to offer.
func handleLegacyAccounts(w http.ResponseWriter, r *http.Request) {
	if !legacyAvailable() {
		writeJSON(w, http.StatusOK, []accountDTO{})
		return
	}
	legacy, err := wallet.NewWallet("aetherd", "test", legacyKeyringDir, walletCodec())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	accounts, err := legacy.ListAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, toAccountDTOs(accounts))
}

// POST /api/accounts/bring-over {"name"} copies that one key in; the
// CLI keyring keeps its copy.
func handleBringOverAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !legacyAvailable() {
		writeError(w, http.StatusBadRequest, errors.New("no other keyring to bring an account from"))
		return
	}
	acc, err := wallet.CopyTestKey(legacyKeyringDir, keyringDir, req.Name, walletCodec())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, accountDTO{Name: acc.Name, Address: acc.Address})
}

// GET /api/balance?address= -> any address's uaeth balance, for accounts
// this wallet watches but doesn't hold (agents it has funded).
func handleBalance(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("address")
	if _, err := sdk.AccAddressFromBech32(address); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid address: %w", err))
		return
	}
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer client.Close()
	balance, err := client.GetBalance(address)
	if err != nil {
		writeError(w, http.StatusBadGateway, nodeError(err))
		return
	}
	writeJSON(w, http.StatusOK, balanceView(address, balance))
}

// assetBalance is one known asset's balance, in its base units.
type assetBalance struct {
	wallet.Asset
	Amount string `json:"amount"`
}

// balanceView is an address's balance: balance in uaeth (as it always
// was), each asset this wallet knows (AETH first, zero included), and
// any other token by its bare denom. A token that only looks like USDC
// (it arrived some other way) is listed there, never as USDC.
func balanceView(address string, coins sdk.Coins) map[string]any {
	var known []assetBalance
	for _, a := range assets.List() {
		known = append(known, assetBalance{Asset: a, Amount: coins.AmountOf(a.Denom).String()})
	}
	others := sdk.Coins{}
	for _, c := range coins {
		if _, ok := assets.ByDenom(c.Denom); !ok {
			others = append(others, c)
		}
	}
	return map[string]any{"address": address, "balance": coins.AmountOf(wallet.BaseDenom).String(), "assets": known, "others": others}
}
