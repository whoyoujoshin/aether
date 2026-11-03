// Command rcgenesis builds the genesis file of a fresh Aether chain
// (aether-rc-* rehearsals and aether-1) from the operators who will run
// it (docs/OPERATOR.md).
//
// Each operator sends the coordinator three things: a name, the
// consensus public key of the node that will sign blocks (from `aetherd
// comet show-validator`), and the aether1 address of the miner account
// that will hold its seat. rcgenesis takes the defaults `aetherd init`
// writes for every module and adds:
//
//   - every operator's consensus key as a genesis validator, all with the
//     same power, so the chain makes blocks from the first one;
//   - every operator's miner account, with a balance of zero. It exists,
//     so it can sign from block 1: a lone mining submission needs no fee
//     (app/mining_fee.go), and its first reward pays for registering the
//     consensus key to it (`aetherd tx pow register-validator-pubkey`),
//     which takes the genesis seat over.
//
// Nobody is given a balance: every AETH is mined (the fair launch,
// docs/MAINNET.md). rcgenesis refuses a base file that gives anyone one,
// a chain ID that isn't fresh (where the testnet's activation heights
// would apply), and a key or miner listed twice. It prints the file's
// sha256, which every operator checks before starting.
//
//	aetherd init <moniker> --chain-id aether-rc-1 --home /tmp/base
//	rcgenesis --base /tmp/base/config/genesis.json --operators operators.json \
//	  --chain-id aether-rc-1 --genesis-time 2026-12-01T16:00:00Z --out genesis.json
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/whoyoujoshin/aether/gates"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// Operator is one entry of the operators file.
type Operator struct {
	Name string `json:"name"`
	// ConsensusPubKey is the node's ed25519 consensus key, base64: the
	// "key" of `aetherd comet show-validator`. That command's whole JSON
	// object is accepted too.
	ConsensusPubKey json.RawMessage `json:"consensus_pubkey"`
	Miner           string          `json:"miner"`
}

// genesisPower is each genesis validator's power: the power every seat
// had before the seat rule. The rule replaces it at the first epoch
// boundary with power in proportion to seat work.
const genesisPower = pow.ValidatorVotingPower

// The escrow lock and the evidence window, in blocks. The testnet's
// defaults assumed a block a minute (BondCooldown's 4,320 blocks were to
// be 3 days; at ~5.7 s blocks they are about 7 hours), which let a
// validator that double-signed take its escrow out before evidence
// against it, valid for 48 hours, had to arrive. A fresh chain locks the
// escrow for 43,200 blocks (3 days at 6 s blocks, 60 hours even at the
// 5 s floor timeout_commit allows) and lets evidence age out after
// 28,800 blocks (48 hours at 6 s); CometBFT drops evidence only once it
// is older than both that and max_age_duration (48 hours), so the window
// stays 48 hours and the lock outlasts it.
const (
	freshBondCooldown       = 43_200
	freshEvidenceAgeBlocks  = 28_800
	freshEvidenceAgeSeconds = 48 * 60 * 60
)

func main() {
	base := flag.String("base", "", "genesis.json written by `aetherd init --chain-id <chain-id>` (required)")
	opsPath := flag.String("operators", "", "operators JSON file: [{\"name\",\"consensus_pubkey\",\"miner\"}, ...] (required)")
	chainID := flag.String("chain-id", "", "a fresh chain ID: aether-1 or aether-rc-* (required)")
	genesisTime := flag.String("genesis-time", "", "when block 1 may be made, RFC 3339 in UTC, e.g. 2026-12-01T16:00:00Z (required)")
	out := flag.String("out", "genesis.json", "where to write the genesis file")
	flag.Parse()

	if err := run(*base, *opsPath, *chainID, *genesisTime, *out); err != nil {
		fmt.Fprintln(os.Stderr, "rcgenesis:", err)
		os.Exit(1)
	}
}

func run(basePath, opsPath, chainID, genesisTime, out string) error {
	if basePath == "" || opsPath == "" || chainID == "" || genesisTime == "" {
		return errors.New("--base, --operators, --chain-id and --genesis-time are required")
	}
	baseBz, err := os.ReadFile(basePath)
	if err != nil {
		return err
	}
	opsBz, err := os.ReadFile(opsPath)
	if err != nil {
		return err
	}
	var ops []Operator
	if err := json.Unmarshal(opsBz, &ops); err != nil {
		return fmt.Errorf("%s: %w", opsPath, err)
	}
	genesis, err := build(baseBz, ops, chainID, genesisTime)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, genesis, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(genesis)
	fmt.Printf("wrote %s: %s, %d operators, genesis time %s\n", out, chainID, len(ops), genesisTime)
	fmt.Printf("sha256 %s\n", hex.EncodeToString(sum[:]))
	fmt.Println("every operator checks this hash on their copy before starting their node")
	return nil
}

// build returns the genesis file for ops on chainID, from base.
func build(base []byte, ops []Operator, chainID, genesisTime string) ([]byte, error) {
	if !gates.Fresh(chainID) {
		return nil, fmt.Errorf("chain ID %q isn't a fresh chain (%s or %s*): the testnet's activation heights would apply to it", chainID, gates.MainnetChainID, gates.RehearsalPrefix)
	}
	t, err := time.Parse(time.RFC3339, genesisTime)
	if err != nil {
		return nil, fmt.Errorf("--genesis-time: %w", err)
	}
	if len(ops) == 0 {
		return nil, errors.New("the operators file lists nobody")
	}

	var g map[string]any
	dec := json.NewDecoder(bytes.NewReader(base))
	dec.UseNumber()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("base genesis: %w", err)
	}
	appState, ok := g["app_state"].(map[string]any)
	if !ok {
		return nil, errors.New("base genesis has no app_state: give the file `aetherd init` wrote")
	}
	for _, m := range []string{"auth", "bank", "pow"} {
		if _, ok := appState[m].(map[string]any); !ok {
			return nil, fmt.Errorf("base genesis has no %s section: give the file `aetherd init` wrote", m)
		}
	}
	auth, bank := appState["auth"].(map[string]any), appState["bank"].(map[string]any)
	powParams, ok := appState["pow"].(map[string]any)["params"].(map[string]any)
	if !ok {
		return nil, errors.New("base genesis has no pow params: give the file `aetherd init` wrote")
	}
	if n := lenOf(auth["accounts"]); n > 0 {
		return nil, fmt.Errorf("base genesis already lists %d accounts: start from a fresh `aetherd init`", n)
	}
	if lenOf(bank["balances"]) > 0 || lenOf(bank["supply"]) > 0 {
		return nil, errors.New("base genesis gives someone a balance: every AETH is mined (the fair launch)")
	}

	seenName, seenKey, seenMiner := map[string]bool{}, map[string]bool{}, map[string]bool{}
	validators := make([]any, 0, len(ops))
	accounts := make([]any, 0, len(ops))
	for i, op := range ops {
		where := fmt.Sprintf("operator %d (%s)", i+1, op.Name)
		if strings.TrimSpace(op.Name) == "" {
			return nil, fmt.Errorf("operator %d has no name", i+1)
		}
		if seenName[op.Name] {
			return nil, fmt.Errorf("%s: the name is listed twice", where)
		}
		seenName[op.Name] = true

		key, err := consensusKey(op.ConsensusPubKey)
		if err != nil {
			return nil, fmt.Errorf("%s: consensus_pubkey: %w", where, err)
		}
		if seenKey[key] {
			return nil, fmt.Errorf("%s: this consensus key is listed twice; one key signs for one seat", where)
		}
		seenKey[key] = true

		hrp, raw, err := bech32.DecodeAndConvert(op.Miner)
		if err != nil || hrp != "aether" || len(raw) == 0 {
			return nil, fmt.Errorf("%s: miner %q isn't an aether1 address", where, op.Miner)
		}
		miner := sdk.MustBech32ifyAddressBytes("aether", raw)
		if seenMiner[miner] {
			return nil, fmt.Errorf("%s: miner %s is listed twice; one miner holds one seat", where, miner)
		}
		seenMiner[miner] = true

		validators = append(validators, map[string]any{
			"pub_key": map[string]any{"type": "tendermint/PubKeyEd25519", "value": key},
			"power":   fmt.Sprint(genesisPower),
			"name":    op.Name,
		})
		accounts = append(accounts, map[string]any{
			"@type":          "/cosmos.auth.v1beta1.BaseAccount",
			"address":        miner,
			"pub_key":        nil,
			"account_number": "0", // numbered below, in address order
			"sequence":       "0",
		})
	}
	// Validators in name order, so the file doesn't depend on the order
	// operators were listed in.
	sort.Slice(validators, func(i, j int) bool {
		return validators[i].(map[string]any)["name"].(string) < validators[j].(map[string]any)["name"].(string)
	})
	// Accounts in address order, numbered from 0: the order InitGenesis
	// would give them anyway, so two runs on the same input agree.
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].(map[string]any)["address"].(string) < accounts[j].(map[string]any)["address"].(string)
	})
	for i, a := range accounts {
		a.(map[string]any)["account_number"] = fmt.Sprint(i)
	}

	g["chain_id"] = chainID
	g["genesis_time"] = t.UTC().Format(time.RFC3339)
	g["initial_height"] = json.Number("1")
	consensus, _ := g["consensus"].(map[string]any)
	if consensus == nil {
		consensus = map[string]any{}
		g["consensus"] = consensus
	}
	consensus["validators"] = validators
	params, _ := consensus["params"].(map[string]any)
	evidence, _ := params["evidence"].(map[string]any)
	if evidence == nil {
		return nil, errors.New("base genesis has no consensus evidence params: give the file `aetherd init` wrote")
	}
	evidence["max_age_num_blocks"] = fmt.Sprint(freshEvidenceAgeBlocks)
	evidence["max_age_duration"] = fmt.Sprint(int64(freshEvidenceAgeSeconds) * int64(time.Second))
	powParams["bond_cooldown"] = json.Number(fmt.Sprint(freshBondCooldown))
	auth["accounts"] = accounts
	bank["balances"] = []any{}
	bank["supply"] = []any{}

	outBz, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(outBz, '\n'), nil
}

// consensusKey reads a consensus public key given as base64 or as the
// JSON `aetherd comet show-validator` prints, and returns it as base64
// of 32 bytes.
func consensusKey(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var obj struct {
			Type string `json:"@type"`
			Key  string `json:"key"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil || obj.Key == "" {
			return "", errors.New("give the base64 key, or the JSON `aetherd comet show-validator` prints")
		}
		if obj.Type != "" && obj.Type != "/cosmos.crypto.ed25519.PubKey" {
			return "", fmt.Errorf("a %s key: consensus keys are ed25519", obj.Type)
		}
		s = obj.Key
	}
	bz, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("not base64: %w", err)
	}
	if len(bz) != 32 {
		return "", fmt.Errorf("%d bytes; an ed25519 key is 32", len(bz))
	}
	return base64.StdEncoding.EncodeToString(bz), nil
}

func lenOf(v any) int {
	if a, ok := v.([]any); ok {
		return len(a)
	}
	return 0
}
