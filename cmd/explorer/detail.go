// cmd/explorer/detail.go
//
// The richer views behind the redesigned explorer pages: a block's
// transactions and proof-of-work, a transaction's decoded messages and
// events, the validator set with its recent signing record, and the
// extra fields the stats, address and governance pages show. Everything
// here is read from the node on each request, like the rest of this API;
// nothing is estimated or made up when the chain doesn't say.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cometrpchttp "github.com/cometbft/cometbft/rpc/client/http"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
	"golang.org/x/crypto/scrypt"
	"google.golang.org/grpc"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
)

var (
	encodingOnce sync.Once
	encodingCfg  app.EncodingConfig
)

// encoding is the app's codec, built once: it knows every message type
// the chain accepts, so txs decode to readable JSON (nested MsgExec
// messages included) rather than opaque bytes.
func encoding() app.EncodingConfig {
	encodingOnce.Do(func() { encodingCfg = app.MakeEncodingConfig() })
	return encodingCfg
}

func newRPC() (*cometrpchttp.HTTP, error) {
	return cometrpchttp.New(rpcEndpoint, "/websocket")
}

func newGRPC() (*grpc.ClientConn, error) {
	return grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(wallet.GRPCCredentials(grpcEndpoint)))
}

// --- events ---

type attributeDTO struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type eventDTO struct {
	Type       string         `json:"type"`
	Attributes []attributeDTO `json:"attributes"`
}

func toEventDTOs(events []abci.Event) []eventDTO {
	out := make([]eventDTO, 0, len(events))
	for _, e := range events {
		attrs := make([]attributeDTO, 0, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs = append(attrs, attributeDTO{Key: a.Key, Value: a.Value})
		}
		out = append(out, eventDTO{Type: e.Type, Attributes: attrs})
	}
	return out
}

// firstAttr is the value of key in the first event of type typ, or "".
func firstAttr(events []abci.Event, typ, key string) string {
	for _, e := range events {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return a.Value
			}
		}
	}
	return ""
}

// mainTransfer is the first transfer in a tx that isn't its fee payment
// (the ante handler's fee transfer always comes first), as recipient and
// amount; "" if there's none.
func mainTransfer(events []abci.Event) (to, amount string) {
	fee, payer := firstAttr(events, "tx", "fee"), firstAttr(events, "tx", "fee_payer")
	skippedFee := false
	for _, e := range events {
		if e.Type != "transfer" {
			continue
		}
		var sender, recipient, amt string
		for _, a := range e.Attributes {
			switch a.Key {
			case "sender":
				sender = a.Value
			case "recipient":
				recipient = a.Value
			case "amount":
				amt = a.Value
			}
		}
		if !skippedFee && fee != "" && sender == payer && amt == fee {
			skippedFee = true
			continue
		}
		return recipient, amt
	}
	return "", ""
}

// --- proof of work ---

// powProofDTO is one MsgSubmitPoW in a block. A native submission's
// header is public, so its scrypt hash is recomputed here and shown
// against the target it had to beat; AuxPoW's is proven against the
// parent chain's header instead, so only its kind is reported.
type powProofDTO struct {
	TxHash        string `json:"txHash"`
	Code          uint32 `json:"code"`
	Miner         string `json:"miner"`
	Kind          string `json:"kind"` // "native" or "auxpow"
	ClaimedHeight uint64 `json:"claimedHeight,omitempty"`
	Nonce         uint64 `json:"nonce,omitempty"`
	Difficulty    uint64 `json:"difficulty,omitempty"`
	Hash          string `json:"hash,omitempty"`   // scrypt hash, 64 hex chars
	Target        string `json:"target,omitempty"` // (2^256-1)/difficulty, 64 hex chars
	Valid         bool   `json:"valid"`
	Margin        string `json:"margin,omitempty"` // target/hash, e.g. "2.4"
	Reward        string `json:"reward"`           // uaeth minted by this tx; "" if none
}

// powHeaderBytes mirrors x/pow's headerToBytes (keeper.go), which is
// unexported: the same little-endian layout the keeper hashes, and
// cmd/powminer mines against.
func powHeaderBytes(n *pow.NativeSubmission, miner sdk.AccAddress) []byte {
	buf := make([]byte, 0, 64)
	putU64 := func(v uint64) {
		var tmp [8]byte
		for i := 0; i < 8; i++ {
			tmp[i] = byte(v >> (8 * i))
		}
		buf = append(buf, tmp[:]...)
	}
	putU64(n.Height)
	putU64(uint64(n.Timestamp))
	buf = append(buf, n.PrevHash...)
	buf = append(buf, n.MerkleRoot...)
	putU64(n.Nonce)
	putU64(n.Difficulty)
	buf = append(buf, miner.Bytes()...)
	return buf
}

// nativeProof fills in the hash, target and margin of a native
// submission, using the keeper's own parameters (x/pow.ScryptN etc.).
func nativeProof(p *powProofDTO, n *pow.NativeSubmission, miner sdk.AccAddress) {
	p.ClaimedHeight, p.Nonce, p.Difficulty = n.Height, n.Nonce, n.Difficulty
	if n.Difficulty == 0 {
		return
	}
	data := powHeaderBytes(n, miner)
	hash, err := scrypt.Key(data, data, pow.ScryptN, pow.ScryptR, pow.ScryptP, 32)
	if err != nil {
		return
	}
	maxTarget := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	target := new(big.Int).Div(maxTarget, new(big.Int).SetUint64(n.Difficulty))
	h := new(big.Int).SetBytes(hash)
	p.Hash = hex.EncodeToString(hash)
	p.Target = fmt.Sprintf("%064x", target)
	p.Valid = h.Cmp(target) < 0
	if h.Sign() > 0 {
		margin, _ := new(big.Float).Quo(new(big.Float).SetInt(target), new(big.Float).SetInt(h)).Float64()
		p.Margin = strconv.FormatFloat(margin, 'f', 1, 64)
	}
}

// --- decoding ---

type decodedTx struct {
	body     txtypes.TxBody
	authInfo txtypes.AuthInfo
	ok       bool
}

func decodeTx(bz []byte) decodedTx {
	var raw txtypes.TxRaw
	if err := proto.Unmarshal(bz, &raw); err != nil {
		return decodedTx{}
	}
	var d decodedTx
	if err := proto.Unmarshal(raw.BodyBytes, &d.body); err != nil {
		return decodedTx{}
	}
	_ = proto.Unmarshal(raw.AuthInfoBytes, &d.authInfo)
	d.ok = true
	return d
}

// msgTypeName turns "/cosmos.bank.v1beta1.MsgSend" into "MsgSend".
func msgTypeName(typeURL string) string {
	if i := strings.LastIndex(typeURL, "."); i >= 0 && i < len(typeURL)-1 {
		return typeURL[i+1:]
	}
	return typeURL
}

// --- GET /api/block?height= (extended) ---

type blockTxDTO struct {
	Hash     string `json:"hash"`
	MsgType  string `json:"msgType"`
	MsgCount int    `json:"msgCount"`
	Code     uint32 `json:"code"`
	From     string `json:"from"`
	To       string `json:"to"`
	Amount   string `json:"amount"`
}

type blockExtrasDTO struct {
	blockDetailDTO
	ParentHash   string        `json:"parentHash"`
	SizeBytes    int           `json:"sizeBytes"`
	GasUsed      int64         `json:"gasUsed"`
	GasWanted    int64         `json:"gasWanted"`
	MaxGas       int64         `json:"maxGas"` // -1: no block gas limit
	LatestHeight int64         `json:"latestHeight"`
	EpochLength  int64         `json:"epochLength"`
	Proposer     string        `json:"proposer"` // the proposer's miner account, when it registered one
	Txs          []blockTxDTO  `json:"txs"`
	Pow          []powProofDTO `json:"pow"`
}

func blockTxs(block *cmttypes.Block, results []*abci.ExecTxResult) ([]blockTxDTO, []powProofDTO, int64, int64) {
	txs := make([]blockTxDTO, 0, len(block.Data.Txs))
	proofs := []powProofDTO{}
	var gasUsed, gasWanted int64
	for i, bz := range block.Data.Txs {
		t := blockTxDTO{Hash: strings.ToUpper(hex.EncodeToString(bz.Hash()))}
		var events []abci.Event
		if i < len(results) && results[i] != nil {
			r := results[i]
			t.Code = r.Code
			events = r.Events
			gasUsed += r.GasUsed
			gasWanted += r.GasWanted
		}
		t.From = firstAttr(events, "message", "sender")
		if t.From == "" {
			t.From = firstAttr(events, "transfer", "sender")
		}
		t.To, t.Amount = mainTransfer(events)

		d := decodeTx(bz)
		if d.ok {
			t.MsgCount = len(d.body.Messages)
			if len(d.body.Messages) > 0 {
				t.MsgType = msgTypeName(d.body.Messages[0].TypeUrl)
			}
			for _, m := range d.body.Messages {
				if m.TypeUrl != "/aether.pow.v1.MsgSubmitPoW" {
					continue
				}
				var msg pow.MsgSubmitPoW
				if err := proto.Unmarshal(m.Value, &msg); err != nil {
					continue
				}
				p := powProofDTO{TxHash: t.Hash, Code: t.Code, Miner: msg.Miner, Reward: firstAttr(events, "coinbase", "amount")}
				switch s := msg.Submission.(type) {
				case *pow.MsgSubmitPoW_Native:
					p.Kind = "native"
					if miner, err := sdk.AccAddressFromBech32(msg.Miner); err == nil && s.Native != nil {
						nativeProof(&p, s.Native, miner)
					}
				case *pow.MsgSubmitPoW_AuxPow:
					p.Kind = "auxpow"
					p.Valid = t.Code == 0
				}
				proofs = append(proofs, p)
			}
		}
		txs = append(txs, t)
	}
	return txs, proofs, gasUsed, gasWanted
}

func blockExtras(ctx context.Context, rpc *cometrpchttp.HTTP, result blockDetailDTO, block *cmttypes.Block) blockExtrasDTO {
	out := blockExtrasDTO{
		blockDetailDTO: result,
		ParentHash:     block.LastBlockID.Hash.String(),
		SizeBytes:      block.Size(),
		MaxGas:         -1,
		Txs:            []blockTxDTO{},
		Pow:            []powProofDTO{},
	}
	height := block.Height
	var results []*abci.ExecTxResult
	if br, err := rpc.BlockResults(ctx, &height); err == nil {
		results = br.TxsResults
	}
	out.Txs, out.Pow, out.GasUsed, out.GasWanted = blockTxs(block, results)
	if cp, err := rpc.ConsensusParams(ctx, &height); err == nil {
		out.MaxGas = cp.ConsensusParams.Block.MaxGas
	}
	if st, err := rpc.Status(ctx); err == nil {
		out.LatestHeight = st.SyncInfo.LatestBlockHeight
	}
	if conn, err := newGRPC(); err == nil {
		defer conn.Close()
		if p, err := pow.NewQueryClient(conn).Params(ctx, &pow.QueryParamsRequest{}); err == nil {
			out.EpochLength = effectiveEpochLength(p.EpochLength)
		}
	}
	if miners, err := consensusMiners(ctx, rpc); err == nil {
		out.Proposer, _ = minerFor(miners, strings.ToUpper(result.ProposerAddress))
	}
	return out
}

// --- GET /api/tx?hash= (extended) ---

type txExtrasDTO struct {
	transactionDetailDTO
	Codespace    string            `json:"codespace"`
	MsgTypes     []string          `json:"msgTypes"`
	Messages     []json.RawMessage `json:"messages"` // each message as proto JSON, "@type" first-class
	Events       []eventDTO        `json:"events"`
	Fee          string            `json:"fee"` // uaeth; "" if none
	GasLimit     uint64            `json:"gasLimit"`
	Memo         string            `json:"memo"`
	Signer       string            `json:"signer"`
	Sequence     *uint64           `json:"sequence"`
	LatestHeight int64             `json:"latestHeight"`
	Raw          json.RawMessage   `json:"raw"`
}

func txExtras(ctx context.Context, base transactionDetailDTO) (txExtrasDTO, error) {
	out := txExtrasDTO{transactionDetailDTO: base, MsgTypes: []string{}, Messages: []json.RawMessage{}, Events: []eventDTO{}}
	conn, err := newGRPC()
	if err != nil {
		return out, err
	}
	defer conn.Close()
	resp, err := txtypes.NewServiceClient(conn).GetTx(ctx, &txtypes.GetTxRequest{Hash: base.Hash})
	if err != nil {
		return out, err
	}
	cdc := encoding().Codec
	if tr := resp.TxResponse; tr != nil {
		out.Codespace = tr.Codespace
		out.Events = toEventDTOs(tr.Events)
		out.Signer = firstAttr(tr.Events, "message", "sender")
	}
	if tx := resp.Tx; tx != nil {
		if tx.Body != nil {
			out.Memo = tx.Body.Memo
			for _, m := range tx.Body.Messages {
				out.MsgTypes = append(out.MsgTypes, msgTypeName(m.TypeUrl))
				bz, err := cdc.MarshalJSON(m)
				if err != nil {
					bz, _ = json.Marshal(map[string]string{"@type": m.TypeUrl})
				}
				out.Messages = append(out.Messages, bz)
			}
		}
		if ai := tx.AuthInfo; ai != nil {
			if ai.Fee != nil {
				out.Fee = ai.Fee.Amount.AmountOf("uaeth").String()
				if ai.Fee.Amount.IsZero() {
					out.Fee = "0"
				}
				out.GasLimit = ai.Fee.GasLimit
			}
			if len(ai.SignerInfos) > 0 {
				seq := ai.SignerInfos[0].Sequence
				out.Sequence = &seq
			}
		}
	}
	if raw, err := cdc.MarshalJSON(resp); err == nil {
		out.Raw = raw
	}
	if h, err := fetchLatestHeight(rpcEndpoint); err == nil {
		out.LatestHeight = h
	}
	return out, nil
}

// --- GET /api/validator-set ---
//
// The CometBFT validator set, joined to the miner accounts behind it
// (via their MsgRegisterValidatorPubkey), x/pow's tenure record, and
// which of the last signingWindow blocks each one signed.

const signingWindow = 25

type validatorSetEntryDTO struct {
	ConsensusAddress string `json:"consensusAddress"` // hex; "" if not in the CometBFT set yet
	Account          string `json:"account"`          // the miner account behind it
	Bootstrap        bool   `json:"bootstrap"`        // a genesis validator, not one that registered
	VotingPower      int64  `json:"votingPower"`
	TenureRatio      string `json:"tenureRatio"`
	EnteredAtUnix    int64  `json:"enteredAtUnix"`
	Signed           []bool `json:"signed"` // oldest first; empty if not in the CometBFT set
	Missed           int    `json:"missed"`
	Banned           bool   `json:"banned"`
	Status           string `json:"status"` // "active", "missing", "pending" or "banned"
}

type validatorSetDTO struct {
	Height        int64                  `json:"height"`
	Window        int                    `json:"window"`
	TotalPower    int64                  `json:"totalPower"`
	TopKSize      int64                  `json:"topKSize"`
	Validators    []validatorSetEntryDTO `json:"validators"`
	SigningHeight []int64                `json:"signingHeights"`
}

var (
	minersMu      sync.Mutex
	minersCache   map[string]string
	minersFetched time.Time
)

// consensusMiners maps consensus addresses (upper-case hex) to the miner
// accounts that registered them, from the chain's own registration txs.
// Cached briefly: it's a tx search, and registrations are rare.
func consensusMiners(ctx context.Context, rpc *cometrpchttp.HTTP) (map[string]string, error) {
	minersMu.Lock()
	defer minersMu.Unlock()
	if minersCache != nil && time.Since(minersFetched) < time.Minute {
		return minersCache, nil
	}
	out := map[string]string{}
	perPage := 100
	for page := 1; page <= 10; page++ {
		p := page
		res, err := rpc.TxSearch(ctx, "message.action='/aether.pow.v1.MsgRegisterValidatorPubkey'", false, &p, &perPage, "asc")
		if err != nil {
			return nil, err
		}
		for _, r := range res.Txs {
			if r.TxResult.Code != 0 {
				continue
			}
			d := decodeTx(r.Tx)
			for _, m := range d.body.Messages {
				if m.TypeUrl != "/aether.pow.v1.MsgRegisterValidatorPubkey" {
					continue
				}
				var msg pow.MsgRegisterValidatorPubkey
				if proto.Unmarshal(m.Value, &msg) != nil || len(msg.ConsensusPubkey) != ed25519.PubKeySize {
					continue
				}
				out[ed25519.PubKey(msg.ConsensusPubkey).Address().String()] = msg.Miner
			}
		}
		if page*perPage >= res.TotalCount {
			break
		}
	}
	minersCache, minersFetched = out, time.Now()
	return out, nil
}

// minerFor is the miner account behind a consensus address (hex): the
// one that registered it, or else the address x/pow's BootstrapValidator
// derives for a genesis validator, sdk.AccAddress(consensus address).
func minerFor(miners map[string]string, consensusHex string) (account string, bootstrap bool) {
	if m, ok := miners[consensusHex]; ok {
		return m, false
	}
	bz, err := hex.DecodeString(consensusHex)
	if err != nil || len(bz) == 0 {
		return "", false
	}
	return sdk.AccAddress(bz).String(), true
}

// effectiveEpochLength is the epoch length x/pow actually divides by:
// its CurrentEpoch treats an unset (zero) length as 1.
func effectiveEpochLength(n int64) int64 {
	if n <= 0 {
		return 1
	}
	return n
}

func handleValidatorSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rpc, err := newRPC()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	st, err := rpc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to fetch chain status: %w", err))
		return
	}
	latest := st.SyncInfo.LatestBlockHeight

	perPage := 100
	vals, err := rpc.Validators(ctx, &latest, nil, &perPage)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to fetch validator set: %w", err))
		return
	}

	// Commit(h) holds the signatures for block h.
	first := latest - signingWindow + 1
	if first < 1 {
		first = 1
	}
	heights := make([]int64, 0, signingWindow)
	for h := first; h <= latest; h++ {
		heights = append(heights, h)
	}
	signedAt := make([]map[string]bool, len(heights))
	var wg sync.WaitGroup
	for i, h := range heights {
		wg.Add(1)
		go func(i int, h int64) {
			defer wg.Done()
			c, err := rpc.Commit(ctx, &h)
			m := map[string]bool{}
			if err == nil && c.Commit != nil {
				for _, s := range c.Commit.Signatures {
					if s.BlockIDFlag == cmttypes.BlockIDFlagCommit {
						m[s.ValidatorAddress.String()] = true
					}
				}
			}
			signedAt[i] = m
		}(i, h)
	}
	wg.Wait()

	miners, _ := consensusMiners(ctx, rpc)

	out := validatorSetDTO{Height: latest, Window: len(heights), Validators: []validatorSetEntryDTO{}, SigningHeight: heights}
	byAccount := map[string]int{}
	for _, v := range vals.Validators {
		addr := v.Address.String()
		e := validatorSetEntryDTO{ConsensusAddress: addr, VotingPower: v.VotingPower, Signed: make([]bool, len(heights))}
		e.Account, e.Bootstrap = minerFor(miners, addr)
		for i := range heights {
			e.Signed[i] = signedAt[i][addr]
			if !e.Signed[i] {
				e.Missed++
			}
		}
		e.Status = "active"
		if e.Missed*2 > len(heights) {
			e.Status = "missing"
		}
		out.TotalPower += v.VotingPower
		if e.Account != "" {
			byAccount[e.Account] = len(out.Validators)
		}
		out.Validators = append(out.Validators, e)
	}

	if conn, err := newGRPC(); err == nil {
		defer conn.Close()
		q := pow.NewQueryClient(conn)
		if p, err := q.Params(ctx, &pow.QueryParamsRequest{}); err == nil {
			out.TopKSize = p.TopKSize
		}
		if info, err := q.ValidatorInfo(ctx, &pow.QueryValidatorInfoRequest{}); err == nil {
			for _, vi := range info.Validators {
				i, ok := byAccount[vi.Address]
				if !ok {
					// In x/pow's active set, but CometBFT hasn't applied
					// the update yet (it takes effect at an epoch edge).
					out.Validators = append(out.Validators, validatorSetEntryDTO{Account: vi.Address, Signed: []bool{}, Status: "pending"})
					i = len(out.Validators) - 1
				}
				out.Validators[i].TenureRatio = vi.TenureRatio
				out.Validators[i].EnteredAtUnix = vi.EnteredAtUnix
			}
		}
		for i := range out.Validators {
			if out.Validators[i].Account == "" {
				continue
			}
			if b, err := q.BanStatus(ctx, &pow.QueryBanStatusRequest{Miner: out.Validators[i].Account}); err == nil && b.Banned {
				out.Validators[i].Banned = true
				out.Validators[i].Status = "banned"
			}
		}
	}

	sort.SliceStable(out.Validators, func(i, j int) bool {
		return out.Validators[i].VotingPower > out.Validators[j].VotingPower
	})
	writeJSON(w, http.StatusOK, out)
}

// --- GET /api/governance/params ---

type governanceParamsDTO struct {
	MinDeposit      int64 `json:"minDeposit"`    // uaeth
	DepositPeriod   int64 `json:"depositPeriod"` // seconds
	VotingPeriod    int64 `json:"votingPeriod"`  // seconds
	ActiveValidator int   `json:"activeValidators"`
}

func handleGovernanceParams(w http.ResponseWriter, r *http.Request) {
	conn, err := newGRPC()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer conn.Close()
	ctx := r.Context()
	p, err := governance.NewQueryClient(conn).Params(ctx, &governance.QueryParamsRequest{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := governanceParamsDTO{MinDeposit: p.MinDeposit, DepositPeriod: p.DepositPeriod, VotingPeriod: p.VotingPeriod}
	if av, err := pow.NewQueryClient(conn).ActiveValidators(ctx, &pow.QueryActiveValidatorsRequest{}); err == nil {
		out.ActiveValidator = len(av.Validators)
	}
	writeJSON(w, http.StatusOK, out)
}

// --- extra address fields ---

type escrowDTO struct {
	Balance      string `json:"balance"` // uaeth held back from an active validator's mining rewards
	UnlockHeight int64  `json:"unlockHeight"`
	Pending      bool   `json:"pending"`
}

type addressExtrasDTO struct {
	Escrow      escrowDTO `json:"escrow"`
	Banned      bool      `json:"banned"`
	IsValidator bool      `json:"isValidator"`
	TxsSigned   int       `json:"txsSigned"` // every tx this address signed, not just the recent ones listed
}

func addressExtras(ctx context.Context, addr string) addressExtrasDTO {
	out := addressExtrasDTO{Escrow: escrowDTO{Balance: "0"}}
	if conn, err := newGRPC(); err == nil {
		defer conn.Close()
		q := pow.NewQueryClient(conn)
		if e, err := q.Escrow(ctx, &pow.QueryEscrowRequest{Miner: addr}); err == nil {
			if e.Balance != "" {
				out.Escrow.Balance = e.Balance
			}
			out.Escrow.UnlockHeight, out.Escrow.Pending = e.UnlockHeight, e.HasPendingEscrow
		}
		if b, err := q.BanStatus(ctx, &pow.QueryBanStatusRequest{Miner: addr}); err == nil {
			out.Banned = b.Banned
		}
		if av, err := q.ActiveValidators(ctx, &pow.QueryActiveValidatorsRequest{}); err == nil {
			for _, v := range av.Validators {
				if v == addr {
					out.IsValidator = true
				}
			}
		}
	}
	if rpc, err := newRPC(); err == nil {
		one := 1
		if res, err := rpc.TxSearch(ctx, fmt.Sprintf("message.sender='%s'", addr), false, &one, &one, "desc"); err == nil {
			out.TxsSigned = res.TotalCount
		}
	}
	return out
}

// --- extra stats fields ---

type statsExtrasDTO struct {
	EpochLength      int64 `json:"epochLength"`
	TargetBlockTime  int64 `json:"targetBlockTime"` // seconds between PoW submissions the difficulty aims for
	ActiveValidators int   `json:"activeValidators"`
}

func statsExtras(ctx context.Context, conn *grpc.ClientConn) statsExtrasDTO {
	var out statsExtrasDTO
	q := pow.NewQueryClient(conn)
	if p, err := q.Params(ctx, &pow.QueryParamsRequest{}); err == nil {
		out.EpochLength, out.TargetBlockTime = effectiveEpochLength(p.EpochLength), p.TargetBlockTime
	}
	if av, err := q.ActiveValidators(ctx, &pow.QueryActiveValidatorsRequest{}); err == nil {
		out.ActiveValidators = len(av.Validators)
	}
	return out
}
