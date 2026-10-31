package vectors

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/miner"
	"github.com/whoyoujoshin/aether/wallet"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// clients/testdata/pow.json: native mining (package miner, x/pow's
// keeper) as the Go implementation does it, for the browser miner and the
// clients: header bytes, scrypt hashes, targets, which hashes meet which
// difficulty, a header Search found, and a signed MsgSubmitPoW. Regenerate
// with go test ./clients/vectors -update-vectors.

const powChainID = "aether-rc-vectors"

type powHeaderVector struct {
	Name       string          `json:"name"`
	Height     uint64          `json:"height"`
	Timestamp  int64           `json:"timestamp"`
	PrevHash   string          `json:"prevHash"`
	MerkleRoot string          `json:"merkleRoot"`
	Nonce      string          `json:"nonce"` // decimal: may pass 2^53
	Difficulty string          `json:"difficulty"`
	Miner      string          `json:"miner"`
	Bytes      string          `json:"bytes"`  // what's hashed
	Scrypt     string          `json:"scrypt"` // scrypt(bytes, bytes, 1024, 1, 1, 32)
	ID         string          `json:"id"`     // sha256(bytes): what the chain keeps of an accepted header
	Meets      map[string]bool `json:"meets"`  // difficulty -> whether the hash meets it
}

type powVectors struct {
	Note       string            `json:"note"`
	ChainID    string            `json:"chainId"`
	Mnemonic   string            `json:"mnemonic"`
	Miner      string            `json:"miner"`
	MinerBytes string            `json:"minerBytes"`
	Targets    map[string]string `json:"targets"` // difficulty -> target, hex
	Headers    []powHeaderVector `json:"headers"`
	Found      powHeaderVector   `json:"found"` // Search's first nonce from 0 at difficulty 64
	Msg        rotMsgVector      `json:"msg"`
	Tx         txVector          `json:"tx"`
	Work       json.RawMessage   `json:"work"` // /api/mine/work for the found header's block
}

var powDifficulties = []uint64{1, 2, 16, 64, 255, 1000, 355_000, 1 << 40}

func powHeader(t *testing.T, name string, h miner.Header) powHeaderVector {
	hash := miner.Hash(h.Bytes())
	id := h.ID()
	meets := map[string]bool{}
	for _, d := range powDifficulties {
		meets[strconv.FormatUint(d, 10)] = miner.Meets(hash, d)
	}
	return powHeaderVector{Name: name, Height: h.Height, Timestamp: h.Timestamp, PrevHash: hex.EncodeToString(h.PrevHash),
		MerkleRoot: hex.EncodeToString(h.MerkleRoot), Nonce: strconv.FormatUint(h.Nonce, 10), Difficulty: strconv.FormatUint(h.Difficulty, 10),
		Miner: hex.EncodeToString(h.Miner), Bytes: hex.EncodeToString(h.Bytes()), Scrypt: hex.EncodeToString(hash), ID: hex.EncodeToString(id[:]), Meets: meets}
}

func buildPoW(t *testing.T) powVectors {
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	acc, err := w.ImportAccount("miner", mnemonic)
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(acc.Address)
	require.NoError(t, err)

	v := powVectors{
		Note:    "Native mining as package miner and x/pow's keeper do it. bytes = height | timestamp | prevHash | merkleRoot | nonce | difficulty | miner address, integers 8-byte little-endian; scrypt = scrypt(bytes, bytes, N=1024, r=1, p=1, 32); a hash meets a difficulty when, read big-endian, it is below target = (2^256-1)/difficulty. Regenerate: go test ./clients/vectors -update-vectors.",
		ChainID: powChainID, Mnemonic: mnemonic, Miner: acc.Address, MinerBytes: hex.EncodeToString(addr), Targets: map[string]string{},
	}
	for _, d := range powDifficulties {
		v.Targets[strconv.FormatUint(d, 10)] = hex.EncodeToString(miner.Target(d))
	}
	prev := bytes.Repeat([]byte{0x5a}, 32)
	base := miner.Header{Height: 534_771, Timestamp: 1_793_000_000, PrevHash: prev, MerkleRoot: miner.MerkleRoot, Difficulty: 355_000, Miner: addr}
	for _, n := range []uint64{0, 1, 255, 256, 1 << 32, 1<<63 + 5} {
		h := base
		h.Nonce = n
		v.Headers = append(v.Headers, powHeader(t, "nonce "+strconv.FormatUint(n, 10), h))
	}
	h := base
	h.Height, h.Difficulty = 1, 1
	v.Headers = append(v.Headers, powHeader(t, "height 1, difficulty 1", h))

	f := base
	f.Difficulty = 64
	var hashes atomic.Uint64
	found, ok := miner.Search(context.Background(), f, 0, 1, 1_000_000, &hashes)
	require.True(t, ok)
	v.Found = powHeader(t, "found", found)

	m := &pow.MsgSubmitPoW{Miner: acc.Address, Submission: &pow.MsgSubmitPoW_Native{Native: &pow.NativeSubmission{
		Height: found.Height, Timestamp: found.Timestamp, PrevHash: found.PrevHash, MerkleRoot: found.MerkleRoot, Nonce: found.Nonce, Difficulty: found.Difficulty}}}
	mb, err := m.Marshal()
	require.NoError(t, err)
	fields, _ := json.Marshal(map[string]any{"miner": acc.Address, "native": map[string]any{"height": found.Height, "timestamp": found.Timestamp,
		"prevHash": hex.EncodeToString(found.PrevHash), "merkleRoot": hex.EncodeToString(found.MerkleRoot), "nonce": strconv.FormatUint(found.Nonce, 10),
		"difficulty": strconv.FormatUint(found.Difficulty, 10)}})
	v.Msg = rotMsgVector{Name: "MsgSubmitPoW", TypeURL: "/aether.pow.v1.MsgSubmitPoW", Fields: fields, Bytes: hex.EncodeToString(mb)}

	params := wallet.TxParams{ChainID: powChainID, AccountNumber: 12, Sequence: 4, GasLimit: 400_000, Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.ZeroInt()))}
	signed, err := w.BuildAndSignMsgTx("miner", m, params)
	require.NoError(t, err)
	var raw txtypes.TxRaw
	require.NoError(t, raw.Unmarshal(signed.Bytes))
	doc := txtypes.SignDoc{BodyBytes: raw.BodyBytes, AuthInfoBytes: raw.AuthInfoBytes, ChainId: powChainID, AccountNumber: 12}
	docBytes, err := doc.Marshal()
	require.NoError(t, err)
	v.Tx = txVector{ChainID: powChainID, AccountNumber: 12, Sequence: 4, From: acc.Address, GasLimit: 400_000,
		BodyBytes: hex.EncodeToString(raw.BodyBytes), AuthInfoBytes: hex.EncodeToString(raw.AuthInfoBytes), SignDoc: hex.EncodeToString(docBytes),
		Signature: hex.EncodeToString(raw.Signatures[0]), TxBytes: hex.EncodeToString(signed.Bytes), TxHash: wallet.TxHash(signed)}

	v.Work, err = json.Marshal(miner.Work{ChainID: powChainID, Height: int64(found.Height), Hash: found.PrevHash, Difficulty: 64, RecencyWindow: 60})
	require.NoError(t, err)
	return v
}

func TestPoWVectors(t *testing.T) {
	v := buildPoW(t)
	bz, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	path := filepath.Join("..", "testdata", "pow.json")
	if *update {
		require.NoError(t, os.WriteFile(path, append(bz, '\n'), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run: go test ./clients/vectors -update-vectors")
	require.JSONEq(t, string(want), string(bz), "the Go implementation no longer produces the mining vectors the clients are tested against")
}
