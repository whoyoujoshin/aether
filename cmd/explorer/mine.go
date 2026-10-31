package main

// The Mine page's backend: what a browser miner needs from the chain,
// at a cost that doesn't grow with the number of miners. Hashing runs on
// the visitor's computer; the node is asked for the work about once per
// mineWorkTTL however many miners ask; and the only transaction the
// explorer relays is a mining submission, rate-limited per visitor.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/miner"
	"github.com/whoyoujoshin/aether/miner/node"
	"github.com/whoyoujoshin/aether/wallet"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// mineWorkTTL is how long one read of the work serves every miner. Blocks
// are about 6 s apart and a header stays good for RecencyWindow/2 blocks,
// so a few seconds' lag costs nothing.
const mineWorkTTL = 3 * time.Second

var (
	mineConnOnce sync.Once
	mineConn     *grpc.ClientConn
	mineConnErr  error

	mineWork = &miner.Cache{TTL: mineWorkTTL, Read: func(ctx context.Context) (miner.Work, error) {
		conn, err := mineGRPC()
		if err != nil {
			return miner.Work{}, err
		}
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return node.Work(ctx, conn)
	}}
)

// mineGRPC is one connection to the node for the mining endpoints, kept
// open: they're asked often, unlike the per-request pages.
func mineGRPC() (*grpc.ClientConn, error) {
	mineConnOnce.Do(func() {
		mineConn, mineConnErr = grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(wallet.GRPCCredentials(grpcEndpoint)))
	})
	return mineConn, mineConnErr
}

// --- GET /api/mine/work ---

// mineWorkDTO is miner.Work as it marshals, for the API's schema.
type mineWorkDTO struct {
	ChainID       string `json:"chainId"`
	Height        int64  `json:"height"`
	Hash          string `json:"hash"`       // hex: the header's prev hash
	Difficulty    string `json:"difficulty"` // decimal: a header at height must claim at least this
	RecencyWindow int64  `json:"recencyWindow"`
	Target        string `json:"target"` // hex: a header's scrypt hash, big-endian, must be below it
}

func handleMineWork(w http.ResponseWriter, r *http.Request) {
	work, err := mineWork.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("can't read the chain tip: %w", err))
		return
	}
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(mineWorkTTL/time.Second)))
	writeJSON(w, http.StatusOK, work)
}

// --- GET /api/mine/account?addr= ---

type mineAccountDTO struct {
	Address       string `json:"address"`
	Exists        bool   `json:"exists"` // false until the account first receives funds
	AccountNumber string `json:"accountNumber,omitempty"`
	Sequence      string `json:"sequence,omitempty"`
	Balance       string `json:"balance"` // uaeth
}

func handleMineAccount(w http.ResponseWriter, r *http.Request) {
	addr := strings.TrimSpace(r.URL.Query().Get("addr"))
	if addr == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing 'addr' query parameter"))
		return
	}
	if !mineLimiter.allow("acct|"+callerIP(r), 20, time.Minute) {
		writeError(w, http.StatusTooManyRequests, errors.New("too many lookups from this address; try again in a minute"))
		return
	}
	conn, err := mineGRPC()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	out := mineAccountDTO{Address: addr, Balance: "0"}
	info, err := authtypes.NewQueryClient(conn).AccountInfo(ctx, &authtypes.QueryAccountInfoRequest{Address: addr})
	switch {
	case err == nil:
		out.Exists, out.AccountNumber, out.Sequence = true, fmt.Sprint(info.Info.AccountNumber), fmt.Sprint(info.Info.Sequence)
	case status.Code(err) == codes.NotFound:
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("can't read that account (is it an aether1 address?): %w", err))
		return
	}
	if out.Exists {
		bal, err := banktypes.NewQueryClient(conn).Balance(ctx, &banktypes.QueryBalanceRequest{Address: addr, Denom: "uaeth"})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		out.Balance = bal.Balance.Amount.String()
	}
	writeJSON(w, http.StatusOK, out)
}

// --- POST /api/mine/submit {"tx": base64} ---

// registerMineSubmit mounts the one POST route. It sits outside apiRoutes,
// whose document lists GET routes; /api/mine/work's description names it.
func registerMineSubmit(mux *http.ServeMux) {
	mux.HandleFunc("/api/mine/submit", handleMineSubmit)
}

// mineSubmitMaxBytes bounds a submission: an ML-DSA-44 signature and public
// key are about 3.7 KB, the message a few hundred bytes.
const mineSubmitMaxBytes = 16 << 10

type mineSubmitReply struct {
	Hash string `json:"hash"`
	Code uint32 `json:"code"` // 0: in the mempool; the block decides if it's accepted
	Log  string `json:"log,omitempty"`
}

func handleMineSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, errors.New("POST a signed submission"))
		return
	}
	// The chain takes one native submission a block (about 6 s), so a
	// miner sending more than one every few seconds is only adding failed
	// transactions; and all of them together, more than a few a second.
	if !mineLimiter.allow("submit|"+callerIP(r), 6, time.Minute) || !mineLimiter.allow("submit", 300, time.Minute) {
		writeError(w, http.StatusTooManyRequests, errors.New("too many submissions; the chain takes one a block"))
		return
	}
	var body struct {
		Tx string `json:"tx"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2*mineSubmitMaxBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("body must be {\"tx\": base64}: %w", err))
		return
	}
	txBytes, err := base64.StdEncoding.DecodeString(body.Tx)
	if err != nil || len(txBytes) == 0 || len(txBytes) > mineSubmitMaxBytes {
		writeError(w, http.StatusBadRequest, errors.New("tx must be base64 of at most 16 KiB"))
		return
	}
	if err := onlyMiningSubmission(txBytes); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rpc, err := newRPC()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := rpc.BroadcastTxSync(ctx, txBytes)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("broadcast: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, mineSubmitReply{Hash: res.Hash.String(), Code: res.Code, Log: res.Log})
}

// onlyMiningSubmission refuses anything but a transaction carrying one
// native MsgSubmitPoW: the explorer relays mining, not payments.
func onlyMiningSubmission(txBytes []byte) error {
	tx, err := encoding().TxConfig.TxDecoder()(txBytes)
	if err != nil {
		return fmt.Errorf("not a transaction: %w", err)
	}
	msgs := tx.GetMsgs()
	if len(msgs) != 1 {
		return fmt.Errorf("a submission carries one message, not %d", len(msgs))
	}
	m, ok := msgs[0].(*pow.MsgSubmitPoW)
	if !ok {
		return fmt.Errorf("only %s is relayed here", "/aether.pow.v1.MsgSubmitPoW")
	}
	if m.GetNative() == nil {
		return errors.New("only native submissions are relayed here")
	}
	return nil
}

// --- per-visitor limits ---

// windowLimiter allows n events per key per window. Entries expire with
// their window, so memory stays bounded by recent visitors.
type windowLimiter struct {
	mu   sync.Mutex
	seen map[string]*window
}

type window struct {
	start time.Time
	n     int
}

var mineLimiter = &windowLimiter{seen: map[string]*window{}}

func (l *windowLimiter) allow(key string, n int, per time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.seen) > 50_000 {
		for k, v := range l.seen {
			if now.Sub(v.start) > per {
				delete(l.seen, k)
			}
		}
	}
	v := l.seen[key]
	if v == nil || now.Sub(v.start) > per {
		l.seen[key] = &window{start: now, n: 1}
		return true
	}
	if v.n >= n {
		return false
	}
	v.n++
	return true
}

// callerIP is the visitor's address. Behind the local reverse proxy
// (Caddy), the last X-Forwarded-For entry is the one it added.
func callerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}
