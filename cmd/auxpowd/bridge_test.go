package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/scrypt"

	pow "github.com/whoyoujoshin/aether/x/pow"
	powtypes "github.com/whoyoujoshin/aether/x/pow/types"
)

type fakeChain struct {
	mu        sync.Mutex
	st        chainState
	submitted []*pow.AuxPowData
	result    txResult
}

func (f *fakeChain) State(context.Context) (chainState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, nil
}

func (f *fakeChain) Submit(_ context.Context, d *pow.AuxPowData) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, d)
	return "TXHASH", nil
}

func (f *fakeChain) TxResult(context.Context, string) (txResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result, true, nil
}

func (f *fakeChain) advance(blocks int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st.Height += blocks
	f.st.BlockHash = bytes.Repeat([]byte{byte(f.st.Height)}, 32)
}

const (
	testUser = "pool"
	testPass = "secret"
	testDiff = 16 // a parent clears it in ~16 scrypt hashes
)

var poolAddr = sdk.AccAddress(bytes.Repeat([]byte{0x21}, 32))

func newTestBridge(t *testing.T) (*bridge, *fakeChain, *httptest.Server) {
	t.Helper()
	fc := &fakeChain{st: chainState{
		ChainID:       "aether-testnet-1",
		Height:        pow.MergedMiningActivationHeight + 10,
		BlockHash:     bytes.Repeat([]byte{0xab}, 32),
		AuxDifficulty: testDiff,
		BlockReward:   sdkmath.NewInt(50_000_000),
		ShareBps:      2_500,
		RecencyWindow: 10,
	}}
	b := newBridge(fc, nil, log.New(io.Discard, "", 0))
	b.pollEvery = time.Millisecond
	srv := httptest.NewServer(b.handler(testUser, testPass))
	t.Cleanup(srv.Close)
	return b, fc, srv
}

func rpc(t *testing.T, srv *httptest.Server, method string, params ...string) (json.RawMessage, *rpcError, int) {
	t.Helper()
	ps := make([]any, len(params))
	for i, p := range params {
		ps[i] = p
	}
	body, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": ps})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	req.SetBasicAuth(testUser, testPass)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Result, out.Error, resp.StatusCode
}

// minePool does what a merge-mining pool does with createauxblock's
// answer: commits to the hash in its parent coinbase and mines a parent
// header whose scrypt hash, read little-endian, is below _target.
func minePool(t *testing.T, ab auxBlock) string {
	t.Helper()
	display, err := hex.DecodeString(ab.Hash)
	require.NoError(t, err)
	targetLE, err := hex.DecodeString(ab.Target)
	require.NoError(t, err)
	target := new(big.Int).SetBytes(rev(targetLE))

	coinbase := testCoinbase([]byte("/pool/"), rev(display), 0)
	root := dsha(coinbase)
	header := make([]byte, 80)
	header[0] = 0x02 // Litecoin version 2, chain ID 0
	copy(header[36:68], root)
	for nonce := uint32(0); ; nonce++ {
		header[76], header[77], header[78], header[79] = byte(nonce), byte(nonce>>8), byte(nonce>>16), byte(nonce>>24)
		h, err := scrypt.Key(header, header, 1024, 1, 1, 32)
		require.NoError(t, err)
		if new(big.Int).SetBytes(rev(h)).Cmp(target) < 0 {
			break
		}
	}
	return hex.EncodeToString(serializeAuxPow(&cAuxPow{CoinbaseTx: coinbase, ParentHeader: header}))
}

func createAuxBlock(t *testing.T, srv *httptest.Server, addr string) auxBlock {
	t.Helper()
	res, rerr, status := rpc(t, srv, "createauxblock", addr)
	require.Nil(t, rerr)
	require.Equal(t, http.StatusOK, status)
	var ab auxBlock
	require.NoError(t, json.Unmarshal(res, &ab))
	return ab
}

func TestBridge_PoolRoundTrip(t *testing.T) {
	b, fc, srv := newTestBridge(t)
	ab := createAuxBlock(t, srv, poolAddr.String())

	// The template commits to the latest block and the pool's address, in
	// the encodings pools expect.
	want := pow.AuxPoWTemplateHash(fc.st.ChainID, fc.st.Height, fc.st.BlockHash, poolAddr)
	require.Equal(t, hex.EncodeToString(rev(want)), ab.Hash)
	require.Equal(t, pow.AuxPoWChainID, ab.ChainID)
	require.Equal(t, fc.st.Height+1, ab.Height)
	require.Equal(t, hex.EncodeToString(rev(fc.st.BlockHash)), ab.PreviousBlockHash)
	require.Equal(t, int64(12_500_000), ab.CoinbaseValue)
	targetLE, _ := hex.DecodeString(ab.Target)
	require.Equal(t, auxTarget(testDiff), new(big.Int).SetBytes(rev(targetLE)))
	var bits uint32
	_, err := hexScan(ab.Bits, &bits)
	require.NoError(t, err)
	require.LessOrEqual(t, compactTarget(bits).Cmp(auxTarget(testDiff)), 0, "bits never promise more than the real target")

	// The same request for the same block returns the same work.
	require.Equal(t, ab, createAuxBlock(t, srv, poolAddr.String()))

	res, rerr, _ := rpc(t, srv, "submitauxblock", ab.Hash, minePool(t, ab))
	require.Nil(t, rerr)
	require.Equal(t, "true", string(res))

	// What reached the chain is a submission x/pow accepts in the next
	// block, paying the pool.
	require.Len(t, fc.submitted, 1)
	d := fc.submitted[0]
	require.NoError(t, pow.CheckAuxPow(d, testDiff, fc.st.Height+1))
	require.Equal(t, want, d.AuxBlockHash)
	require.Equal(t, fc.st.Height, d.TemplateHeight)
	require.Equal(t, poolAddr.String(), d.RewardAddress)
	require.Equal(t, int64(1), b.m.submitted.Load())
	require.Eventually(t, func() bool { return b.m.accepted.Load() == 1 }, time.Second, time.Millisecond)

	// A second proof before the next block would lose the block's one
	// AuxPoW slot, so it isn't sent.
	res, rerr, _ = rpc(t, srv, "submitauxblock", ab.Hash, minePool(t, ab))
	require.Nil(t, rerr)
	require.Equal(t, "false", string(res))
	require.Len(t, fc.submitted, 1)
	require.Equal(t, int64(1), b.m.stale.Load())

	// The chain pays a template once, so after a block the same work is
	// still refused, and new work is handed out instead.
	fc.advance(1)
	res, _, _ = rpc(t, srv, "submitauxblock", ab.Hash, minePool(t, ab))
	require.Equal(t, "false", string(res))
	require.Len(t, fc.submitted, 1)
	require.Equal(t, int64(2), b.m.stale.Load())
	fresh := createAuxBlock(t, srv, poolAddr.String())
	require.NotEqual(t, ab.Hash, fresh.Hash)
	res, _, _ = rpc(t, srv, "submitauxblock", fresh.Hash, minePool(t, fresh))
	require.Equal(t, "true", string(res))
	require.Len(t, fc.submitted, 2)
}

func hexScan(s string, v *uint32) (int, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return 0, err
	}
	*v = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return 4, nil
}

func TestBridge_RefusesBadAndLateWork(t *testing.T) {
	b, fc, srv := newTestBridge(t)
	ab := createAuxBlock(t, srv, poolAddr.String())
	proof := minePool(t, ab)

	// Unknown work.
	_, rerr, status := rpc(t, srv, "submitauxblock", strings.Repeat("00", 32), proof)
	require.Equal(t, rpcInvalidParams, rerr.Code)
	require.Equal(t, "block hash unknown", rerr.Message)
	require.Equal(t, http.StatusInternalServerError, status)

	// Garbage.
	_, rerr, _ = rpc(t, srv, "submitauxblock", ab.Hash, "zz")
	require.Equal(t, rpcDeserialization, rerr.Code)

	// A proof committing to work this bridge never handed out: it parses,
	// but x/pow's check against the named work fails.
	foreign := ab
	foreign.Hash = strings.Repeat("ab", 32)
	res, rerr, _ := rpc(t, srv, "submitauxblock", ab.Hash, minePool(t, foreign))
	require.Nil(t, rerr)
	require.Equal(t, "false", string(res))
	require.Equal(t, int64(1), b.m.invalid.Load())

	// Too old for the chain to take.
	fc.advance(fc.st.RecencyWindow)
	res, _, _ = rpc(t, srv, "submitauxblock", ab.Hash, proof)
	require.Equal(t, "false", string(res))
	require.Equal(t, int64(1), b.m.stale.Load())
	require.Empty(t, fc.submitted)

	// A bad address.
	_, rerr, _ = rpc(t, srv, "createauxblock", "cosmos1notours")
	require.Equal(t, rpcInvalidAddress, rerr.Code)
}

// Below the activation height the chain would pay the bridge's own key, so
// no work is handed out.
func TestBridge_RefusesWorkBeforeActivation(t *testing.T) {
	_, fc, srv := newTestBridge(t)
	fc.st.Height = pow.MergedMiningActivationHeight - 2
	_, rerr, _ := rpc(t, srv, "createauxblock", poolAddr.String())
	require.Equal(t, rpcNotActive, rerr.Code)
	require.Contains(t, rerr.Message, "merged mining isn't active")

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	fc.st.Height = pow.MergedMiningActivationHeight - 1 // the next block is the first merged one
	_, rerr, _ = rpc(t, srv, "createauxblock", poolAddr.String())
	require.Nil(t, rerr)
}

func TestBridge_SlotLostOnChainCountsAsStale(t *testing.T) {
	b, fc, srv := newTestBridge(t)
	fc.result = txResult{Height: fc.st.Height + 1, Code: powtypes.ErrTooManySubmissionsThisBlock.ABCICode(), Codespace: powtypes.ErrTooManySubmissionsThisBlock.Codespace()}
	ab := createAuxBlock(t, srv, poolAddr.String())
	res, _, _ := rpc(t, srv, "submitauxblock", ab.Hash, minePool(t, ab))
	require.Equal(t, "true", string(res))
	require.Eventually(t, func() bool { return b.m.stale.Load() == 1 }, time.Second, time.Millisecond)
	require.Zero(t, b.m.accepted.Load())
}

func TestBridge_GetAuxBlock(t *testing.T) {
	b, _, srv := newTestBridge(t)
	_, rerr, _ := rpc(t, srv, "getauxblock")
	require.Contains(t, rerr.Message, "--reward-address")

	b.defaultReward = poolAddr
	res, rerr, _ := rpc(t, srv, "getauxblock")
	require.Nil(t, rerr)
	var ab auxBlock
	require.NoError(t, json.Unmarshal(res, &ab))
	require.Equal(t, createAuxBlock(t, srv, poolAddr.String()), ab)

	res, rerr, _ = rpc(t, srv, "getauxblock", ab.Hash, minePool(t, ab))
	require.Nil(t, rerr)
	require.Equal(t, "true", string(res))
}

func TestBridge_HTTP(t *testing.T) {
	b, _, srv := newTestBridge(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"id":1,"method":"getblockcount","params":[]}`))
	req.SetBasicAuth(testUser, "wrong")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	res, rerr, status := rpc(t, srv, "getblockcount")
	require.Nil(t, rerr)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fmt.Sprint(pow.MergedMiningActivationHeight+10), string(res))

	_, rerr, status = rpc(t, srv, "sendtoaddress", "x")
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, -32601, rerr.Code)

	// A batch is answered in order with HTTP 200.
	req, _ = http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`[{"id":1,"method":"getblockcount","params":[]},{"id":2,"method":"nope","params":[]}]`))
	req.SetBasicAuth(testUser, testPass)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var batch []rpcResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&batch))
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, batch, 2)
	require.Nil(t, batch[0].Error)
	require.Equal(t, -32601, batch[1].Error.Code)

	resp, err = http.Get(srv.URL + "/health")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	b.m.accepted.Add(3)
	resp, err = http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Contains(t, string(body), `auxpowd_shares_total{outcome="accepted"} 3`)
}

func TestCompact(t *testing.T) {
	for _, d := range []uint64{1, 16, 4096, 11_600_000, 1 << 40} {
		target := auxTarget(d)
		bits := compact(target)
		got := compactTarget(bits)
		require.LessOrEqual(t, got.Cmp(target), 0, "difficulty %d", d)
		// Within the precision of a 3-byte mantissa.
		diff := new(big.Int).Sub(target, got)
		require.Negative(t, diff.Cmp(new(big.Int).Rsh(target, 15)), "difficulty %d", d)
	}
	// Litecoin's genesis bits round-trip.
	require.Equal(t, uint32(0x1e0ffff0), compact(compactTarget(0x1e0ffff0)))
}

// Calls pool software such as yiimp makes on a merged-mined daemon besides
// the aux ones.
func TestBridge_PoolCompatibilityCalls(t *testing.T) {
	b, fc, srv := newTestBridge(t)
	b.defaultReward = poolAddr

	// getauxblock's target under Dogecoin's legacy name, same bytes.
	res, rerr, _ := rpc(t, srv, "getauxblock")
	require.Nil(t, rerr)
	var ab auxBlock
	require.NoError(t, json.Unmarshal(res, &ab))
	require.Equal(t, ab.Target, ab.LegacyTarget)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(res, &raw))
	require.Contains(t, raw, "target")

	// The template request object pools send (yiimp sends the rules it
	// supports) is accepted and ignored.
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"id":1,"method":"getblocktemplate","params":[{"rules":["segwit"]}]}`))
	req.SetBasicAuth(testUser, testPass)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	res, rerr, _ = rpc(t, srv, "getblocktemplate")
	require.Nil(t, rerr)
	var tpl struct {
		Height            int64          `json:"height"`
		PreviousBlockHash string         `json:"previousblockhash"`
		Transactions      []any          `json:"transactions"`
		CoinbaseAux       map[string]any `json:"coinbaseaux"`
		Target            string         `json:"target"`
		Bits              string         `json:"bits"`
		CoinbaseValue     int64          `json:"coinbasevalue"`
	}
	require.NoError(t, json.Unmarshal(res, &tpl))
	require.Equal(t, fc.st.Height+1, tpl.Height)
	require.Equal(t, hex.EncodeToString(rev(fc.st.BlockHash)), tpl.PreviousBlockHash)
	require.NotNil(t, tpl.Transactions)
	require.Empty(t, tpl.Transactions)
	require.NotNil(t, tpl.CoinbaseAux)
	be, _ := hex.DecodeString(tpl.Target)
	require.Equal(t, auxTarget(testDiff), new(big.Int).SetBytes(be), "big-endian, as bitcoind's getblocktemplate")
	require.Equal(t, ab.Bits, tpl.Bits)
	require.Equal(t, ab.CoinbaseValue, tpl.CoinbaseValue)

	res, _, _ = rpc(t, srv, "validateaddress", poolAddr.String())
	require.JSONEq(t, `{"isvalid":true,"address":"`+poolAddr.String()+`","ismine":true}`, string(res))
	other := sdk.AccAddress(bytes.Repeat([]byte{0x22}, 32)).String()
	res, _, _ = rpc(t, srv, "validateaddress", other)
	require.JSONEq(t, `{"isvalid":true,"address":"`+other+`","ismine":false}`, string(res))
	res, _, _ = rpc(t, srv, "validateaddress", "Lnotaether")
	require.JSONEq(t, `{"isvalid":false}`, string(res))

	res, _, _ = rpc(t, srv, "getdifficulty")
	require.Equal(t, "16", string(res))
	res, _, _ = rpc(t, srv, "getmininginfo")
	require.JSONEq(t, fmt.Sprintf(`{"blocks":%d,"difficulty":16,"chain":"aether-testnet-1"}`, pow.MergedMiningActivationHeight+10), string(res))

	// Below the activation height there's no template either.
	fc.st.Height = pow.MergedMiningActivationHeight - 2
	_, rerr, _ = rpc(t, srv, "getblocktemplate")
	require.Equal(t, rpcNotActive, rerr.Code)
}

// Work stays the same for half the recency window, then moves on.
func TestBridge_WorkIsStableForHalfTheWindow(t *testing.T) {
	_, fc, srv := newTestBridge(t)
	first := createAuxBlock(t, srv, poolAddr.String())
	fc.advance(fc.st.RecencyWindow/2 - 1)
	require.Equal(t, first.Hash, createAuxBlock(t, srv, poolAddr.String()).Hash)
	fc.advance(1)
	next := createAuxBlock(t, srv, poolAddr.String())
	require.NotEqual(t, first.Hash, next.Hash)
	require.Equal(t, fc.st.Height+1, next.Height)

	// Each reward address has its own work.
	other := createAuxBlock(t, srv, sdk.AccAddress(bytes.Repeat([]byte{0x22}, 32)).String())
	require.NotEqual(t, next.Hash, other.Hash)
}

// yiimp commits the work it had when it built a job but submits with the
// work it fetched last. The bridge uses the work the coinbase commits to,
// which is what the chain checks and pays.
func TestBridge_ProofNamingNewerWorkUsesTheCommittedWork(t *testing.T) {
	_, fc, srv := newTestBridge(t)
	committed := createAuxBlock(t, srv, poolAddr.String())
	proof := minePool(t, committed)

	fc.advance(fc.st.RecencyWindow / 2) // the work moves on
	latest := createAuxBlock(t, srv, poolAddr.String())
	require.NotEqual(t, committed.Hash, latest.Hash)

	res, rerr, _ := rpc(t, srv, "submitauxblock", latest.Hash, proof)
	require.Nil(t, rerr)
	require.Equal(t, "true", string(res))
	require.Len(t, fc.submitted, 1)
	d := fc.submitted[0]
	committedHash, _ := hex.DecodeString(committed.Hash)
	require.Equal(t, rev(committedHash), d.AuxBlockHash)
	require.Equal(t, committed.Height-1, d.TemplateHeight)
	require.NoError(t, pow.CheckAuxPow(d, testDiff, fc.st.Height+1))
}
