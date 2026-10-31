package main

// The desktop app's Mine tab: native scrypt on this computer's CPU, for one
// of the wallet's own accounts. The work comes from the explorer's
// /api/mine/work (one cached read serves every miner), or straight from
// the node if the explorer can't answer; finds are signed here and
// broadcast to the node. One miner runs at a time.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"

	"github.com/whoyoujoshin/aether/miner"
	"github.com/whoyoujoshin/aether/miner/node"
	"github.com/whoyoujoshin/aether/wallet"
	pow "github.com/whoyoujoshin/aether/x/pow"
)

// explorerURL serves /api/mine/work; empty reads the work from the node.
var explorerURL string

const (
	mineWorkEvery = 5 * time.Second
	mineGas       = 400_000
)

type mineSubmission struct {
	Hash   string `json:"hash,omitempty"`
	Height int64  `json:"height"`
	Nonce  string `json:"nonce"`
	At     int64  `json:"at"`     // unix ms
	Status string `json:"status"` // waiting, sending, pending, accepted, refused, dropped
	Reason string `json:"reason,omitempty"`
}

type mineStatus struct {
	Running     bool             `json:"running"`
	Name        string           `json:"name,omitempty"`
	Address     string           `json:"address,omitempty"`
	Threads     int              `json:"threads"`
	MaxThreads  int              `json:"maxThreads"`
	Hashes      uint64           `json:"hashes"`
	Rate        float64          `json:"rate"` // hashes a second, about the last 10 s
	Height      int64            `json:"height,omitempty"`
	Difficulty  string           `json:"difficulty,omitempty"`
	Finds       int              `json:"finds"`
	Accepted    int              `json:"accepted"`
	Submissions []mineSubmission `json:"submissions"`
	Error       string           `json:"error,omitempty"`
}

type mineSession struct {
	name, address string
	addrBytes     []byte
	threads       int
	cancel        context.CancelFunc
	hashes        atomic.Uint64

	mu          sync.Mutex
	work        miner.Work
	finds       int
	accepted    int
	submissions []*mineSubmission
	err         string
	samples     []rateSample
	nextSeq     uint64
	haveSeq     bool
	queued      *mineSubmission // the newest find, waiting for the one in flight to reach a block
	queuedH     miner.Header
	sending     bool
	wal         *wallet.Wallet

	stopped chan struct{} // closed when run returns
}

type rateSample struct {
	at time.Time
	n  uint64
}

var (
	mineMu  sync.Mutex
	mineCur *mineSession
)

// readWork is the work to mine on: the explorer's cached answer, else the
// node's own.
func readWork(ctx context.Context) (miner.Work, error) {
	if explorerURL != "" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		w, err := miner.FromExplorer(ctx, http.DefaultClient, explorerURL)
		cancel()
		if err == nil {
			return w, nil
		}
	}
	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(wallet.GRPCCredentials(grpcEndpoint)))
	if err != nil {
		return miner.Work{}, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return node.Work(ctx, conn)
}

// POST /api/mine/start {"name": "...", "threads": 2}
func handleMineStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	var req struct {
		Name    string `json:"name"`
		Threads int    `json:"threads"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Threads < 1 || req.Threads > runtime.NumCPU() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("threads must be 1 to %d", runtime.NumCPU()))
		return
	}
	wal, err := newWallet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	acc, err := wal.GetAccount(req.Name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("account %q not found: %w", req.Name, err))
		return
	}
	addr, err := sdk.AccAddressFromBech32(acc.Address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	work, err := readWork(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, nodeError(fmt.Errorf("can't read the chain tip: %w", err)))
		return
	}

	mineMu.Lock()
	if mineCur != nil {
		mineCur.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &mineSession{name: req.Name, address: acc.Address, addrBytes: addr, threads: req.Threads, cancel: cancel, work: work,
		samples: []rateSample{{time.Now(), 0}}, stopped: make(chan struct{})}
	mineCur = s
	mineMu.Unlock()

	go s.run(ctx, wal)
	writeJSON(w, http.StatusOK, s.status())
}

// POST /api/mine/stop
func handleMineStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("use POST"))
		return
	}
	mineMu.Lock()
	s := mineCur
	if s != nil {
		s.cancel()
	}
	mineMu.Unlock()
	if s == nil {
		writeJSON(w, http.StatusOK, mineStatus{MaxThreads: runtime.NumCPU(), Submissions: []mineSubmission{}})
		return
	}
	writeJSON(w, http.StatusOK, s.status())
}

// GET /api/mine/status
func handleMineStatus(w http.ResponseWriter, r *http.Request) {
	mineMu.Lock()
	s := mineCur
	mineMu.Unlock()
	if s == nil {
		writeJSON(w, http.StatusOK, mineStatus{MaxThreads: runtime.NumCPU(), Submissions: []mineSubmission{}})
		return
	}
	writeJSON(w, http.StatusOK, s.status())
}

func (s *mineSession) status() mineStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	n := s.hashes.Load()
	s.samples = append(s.samples, rateSample{now, n})
	for len(s.samples) > 2 && now.Sub(s.samples[0].at) > 10*time.Second {
		s.samples = s.samples[1:]
	}
	var rate float64
	if first := s.samples[0]; now.Sub(first.at) > 500*time.Millisecond {
		rate = float64(n-first.n) / now.Sub(first.at).Seconds()
	}
	st := mineStatus{Name: s.name, Address: s.address, Threads: s.threads, MaxThreads: runtime.NumCPU(), Hashes: n,
		Height: s.work.Height, Finds: s.finds, Accepted: s.accepted, Error: s.err, Submissions: []mineSubmission{}}
	select {
	case <-s.stopped:
	default:
		st.Running, st.Rate = true, rate
	}
	if s.work.Difficulty > 0 {
		st.Difficulty = fmt.Sprint(s.work.Difficulty)
	}
	for _, sub := range s.submissions {
		st.Submissions = append(st.Submissions, *sub)
	}
	return st
}

// run mines until ctx ends: threads search the current header, a fresh
// header replaces it when the work moves on, and each find is submitted.
func (s *mineSession) run(ctx context.Context, wal *wallet.Wallet) {
	defer close(s.stopped)
	s.mu.Lock()
	s.wal = wal
	s.mu.Unlock()
	for ctx.Err() == nil {
		s.mu.Lock()
		work := s.work
		s.mu.Unlock()
		h := work.Header(s.addrBytes, time.Now().Unix())
		jobCtx, stopJob := context.WithCancel(ctx)
		found := make(chan miner.Header, s.threads)
		var wg sync.WaitGroup
		for i := 0; i < s.threads; i++ {
			wg.Add(1)
			go func(start uint64) {
				defer wg.Done()
				if f, ok := miner.Search(jobCtx, h, start, uint64(s.threads), ^uint64(0), &s.hashes); ok {
					found <- f
				}
			}(uint64(i))
		}
		tick := time.NewTicker(mineWorkEvery)
	wait:
		for {
			select {
			case <-ctx.Done():
				break wait
			case f := <-found:
				s.enqueue(f)
				if next, err := readWork(ctx); err == nil {
					s.setWork(next, "")
				}
				break wait
			case <-tick.C:
				next, err := readWork(ctx)
				if err != nil {
					s.setWork(work, "can't read the chain: "+err.Error())
					continue
				}
				s.setWork(next, "")
				if work.Stale(next) {
					break wait
				}
			}
		}
		tick.Stop()
		stopJob()
		wg.Wait()
	}
}

func (s *mineSession) setWork(w miner.Work, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.work, s.err = w, errText
}

// enqueue takes a find. The chain takes one native submission a block, so
// one is sent at a time: a find made while another is on its way waits,
// and a newer one replaces it (a find stays good for RecencyWindow/2
// blocks or more, so waiting loses nothing).
func (s *mineSession) enqueue(h miner.Header) {
	sub := &mineSubmission{Height: int64(h.Height), Nonce: fmt.Sprint(h.Nonce), At: time.Now().UnixMilli(), Status: "waiting"}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finds++
	if s.queued != nil {
		s.queued.Status, s.queued.Reason = "dropped", "a newer find replaced it while another was on its way"
	}
	s.queued, s.queuedH = sub, h
	s.submissions = append([]*mineSubmission{sub}, s.submissions...)
	if len(s.submissions) > 20 {
		s.submissions = s.submissions[:20]
	}
	if !s.sending {
		s.sending = true
		go s.pump()
	}
}

// pump sends queued finds one at a time until none is left.
func (s *mineSession) pump() {
	for {
		s.mu.Lock()
		sub, h, wal := s.queued, s.queuedH, s.wal
		s.queued = nil
		if sub == nil {
			s.sending = false
			s.mu.Unlock()
			return
		}
		sub.Status = "sending"
		s.mu.Unlock()
		s.submit(wal, h, sub)
	}
}

// submit signs and broadcasts a find, then follows it into a block.
func (s *mineSession) submit(wal *wallet.Wallet, h miner.Header, sub *mineSubmission) {
	set := func(status, reason string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		sub.Status, sub.Reason = status, reason
		if status == "accepted" {
			s.accepted++
		}
	}

	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		set("refused", err.Error())
		return
	}
	defer client.Close()
	msg := &pow.MsgSubmitPoW{Miner: s.address, Submission: &pow.MsgSubmitPoW_Native{Native: &pow.NativeSubmission{
		Height: h.Height, Timestamp: h.Timestamp, PrevHash: h.PrevHash, MerkleRoot: h.MerkleRoot, Nonce: h.Nonce, Difficulty: h.Difficulty}}}

	var res wallet.BroadcastResult
	for attempt := 0; attempt < 2; attempt++ {
		num, seq, err := client.GetAccountInfo(s.address)
		if err != nil {
			set("refused", "this account doesn't exist on chain yet: get testnet AETH from the Faucet first")
			return
		}
		s.mu.Lock()
		if s.haveSeq && s.nextSeq > seq {
			seq = s.nextSeq
		}
		chain := s.work.ChainID
		s.mu.Unlock()
		if chain == "" {
			chain = chainID
		}
		signed, err := wal.BuildAndSignMsgTx(s.name, msg, wallet.TxParams{ChainID: chain, AccountNumber: num, Sequence: seq, GasLimit: mineGas,
			Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.ZeroInt()))})
		if err != nil {
			set("refused", err.Error())
			return
		}
		res, err = client.BroadcastTx(signed)
		if err != nil {
			set("refused", nodeError(err).Error())
			return
		}
		if res.Code == 32 && attempt == 0 { // account sequence mismatch: ask the chain again
			s.mu.Lock()
			s.haveSeq = false
			s.mu.Unlock()
			continue
		}
		if res.Code == 0 {
			s.mu.Lock()
			s.nextSeq, s.haveSeq = seq+1, true
			s.mu.Unlock()
		}
		break
	}
	s.mu.Lock()
	sub.Hash = res.TxHash
	s.mu.Unlock()
	if res.Code != 0 {
		set("refused", refusalReason(res.RawLog))
		return
	}
	set("pending", "")
	for i := 0; i < 30; i++ {
		time.Sleep(3 * time.Second)
		d, err := client.GetTransactionByHash(res.TxHash)
		if errors.Is(err, wallet.ErrTransactionNotFound) || err != nil {
			continue
		}
		if d.Code == 0 {
			set("accepted", "")
		} else {
			set("refused", refusalReason(d.RawLog))
		}
		return
	}
	set("refused", "not in a block after a minute and a half")
}

// refusalReason says in a few words why the chain refused a submission.
func refusalReason(log string) string {
	switch {
	case strings.Contains(log, "already been accepted at height"):
		return "another miner's find took this block"
	case strings.Contains(log, "blocks behind current height"):
		return "too late: the block it was mined on is too old"
	case strings.Contains(log, "exact mining header has already been accepted"):
		return "already submitted"
	case strings.Contains(log, "below required difficulty"):
		return "difficulty went up before it landed"
	}
	if len(log) > 160 {
		return log[:160] + "…"
	}
	return log
}
