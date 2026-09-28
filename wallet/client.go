package wallet

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"fmt"
	"sort"
	"encoding/base64"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"github.com/cosmos/gogoproto/proto"
	"github.com/whoyoujoshin/aether/x/pow"
)

// Client wraps a real gRPC connection to a specific Aether node,
// providing chain-query operations a wallet needs (balances, account
// sequence numbers). Deliberately separate from transaction
// construction/signing -- mirrors the real SDK's own client.Context
// split, and keeps the door open for offline signing workflows where
// no live connection exists at signing time.
type Client struct {
	conn       *grpc.ClientConn
	bankClient banktypes.QueryClient
	authClient authtypes.QueryClient
}

// NewClient connects to a node's gRPC endpoint, e.g. "localhost:9090" for a
// plaintext devnet, or "grpc.example.com:443" for a TLS-terminated one (see
// GRPCCredentials).
func NewClient(grpcEndpoint string) (*Client, error) {
	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(GRPCCredentials(grpcEndpoint)))
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:       conn,
		bankClient: banktypes.NewQueryClient(conn),
		authClient: authtypes.NewQueryClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

// GRPCCredentials picks TLS or plaintext transport credentials for a gRPC
// endpoint, so callers -- this package and cmd/explorer's own direct
// grpc.NewClient calls -- don't each have to duplicate the choice. Port 443
// is an unambiguous, zero-configuration signal for "this is TLS-terminated"
// (e.g. by a reverse proxy in front of the node's own plaintext gRPC): no
// one runs plaintext gRPC there, and it needs no new flag on any command
// that already takes an endpoint string. Anything else -- "localhost:9090",
// a bare IP:port, an internal hostname -- stays plaintext, matching every
// devnet and the seed's own gRPC port today.
func GRPCCredentials(grpcEndpoint string) credentials.TransportCredentials {
	if !strings.HasSuffix(grpcEndpoint, ":443") {
		return insecure.NewCredentials()
	}
	// ServerName must be the bare host: grpc.NewClient (unlike the older
	// grpc.Dial) doesn't split host:port for the default SNI/verification
	// name on its own -- leaving ServerName unset here sends the literal
	// "host:443" as SNI, which every real TLS server (Caddy included)
	// rejects outright (a reset connection, no cert served).
	host, _, err := net.SplitHostPort(grpcEndpoint)
	if err != nil {
		host = grpcEndpoint
	}
	return credentials.NewTLS(&tls.Config{ServerName: host})
}

// GetBalance returns every coin balance held by the given address.
func (c *Client) GetBalance(address string) (sdk.Coins, error) {
	resp, err := c.bankClient.AllBalances(context.Background(), &banktypes.QueryAllBalancesRequest{
		Address: address,
	})
	if err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

// GetAccountInfo returns the account number and current sequence for
// an address -- both required to correctly sign a new transaction.
// Uses the AccountInfo query (common to all account types since SDK
// 0.47) rather than the older Account query, which would otherwise
// require unpacking a generic protobuf Any into a concrete account
// type -- a real, avoidable complication this newer endpoint sidesteps
// entirely.
func (c *Client) GetAccountInfo(address string) (accountNumber, sequence uint64, err error) {
	resp, err := c.authClient.AccountInfo(context.Background(), &authtypes.QueryAccountInfoRequest{
		Address: address,
	})
	if err != nil {
		return 0, 0, err
	}
	return resp.Info.AccountNumber, resp.Info.Sequence, nil
}

// FormatHeightMetadataKey documents the standard Cosmos SDK gRPC
// metadata key used to pin a query to a specific historical height,
// for callers that need it (e.g. avoiding a race against the very
// latest, not-yet-finalized height). Not used internally by Client's
// own methods above, which query at the current/latest height by
// default -- exposed for callers building more advanced query flows.
const HeightMetadataKey = "x-cosmos-block-height"

// FormatHeight is a small convenience matching the string format the
// gRPC metadata key above expects.
func FormatHeight(height int64) string {
	return strconv.FormatInt(height, 10)
}

// Transaction is a simplified view of a real, on-chain transaction
// involving a given address -- either as sender or recipient.
type Transaction struct {
	Hash      string
	Height    int64
	Code      uint32
	Direction string // "sent" or "received"
	Amount    string
	Timestamp string
	Memo      string // set by the sender; untrusted
	// Counterparty is the other side of the transfer: who paid, or who
	// was paid. CounterpartyModule names it when it's a module account
	// ("pow" pays mining rewards), and is empty otherwise.
	Counterparty       string
	CounterpartyModule string
	// MsgType is the first message's short name ("MsgSend", "MsgGrant"),
	// for transactions that move no money of this address's own.
	MsgType string
}

// moduleAccountName names address when it's a module account a wallet
// sees money move to or from. Computed per call, not at package init:
// the bech32 prefix is only set once the program starts.
func moduleAccountName(address string) string {
	for _, name := range []string{"pow", "treasury", "fee_collector", "governance"} {
		if authtypes.NewModuleAddress(name).String() == address {
			return name
		}
	}
	return ""
}

// GetTransactionHistory returns real, on-chain transactions involving
// the given address, most recent first. Queries both directions
// (sender and recipient) separately -- CometBFT's tx-search query
// language does not cleanly support "sender OR recipient" in a
// single query -- then merges and sorts the results.
func (c *Client) GetTransactionHistory(address string, limit uint64) ([]Transaction, error) {
	txClient := txtypes.NewServiceClient(c.conn)

	var all []Transaction

	for _, q := range []struct {
		query     string
		direction string
	}{
		{fmt.Sprintf("message.sender='%s'", address), "sent"},
		{fmt.Sprintf("transfer.recipient='%s'", address), "received"},
	} {
		resp, err := txClient.GetTxsEvent(context.Background(), &txtypes.GetTxsEventRequest{
			Query:   q.query,
			OrderBy: txtypes.OrderBy_ORDER_BY_DESC,
			Limit:   limit,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query %s transactions: %w", q.direction, err)
		}

								for i, txResp := range resp.TxResponses {
				amount := ""
				firstTransfer, firstRecipient := "", ""
				counterparty := ""
				direction := q.direction
				for _, event := range txResp.Events {
					if event.Type == "transfer" {
						var eventSender, eventRecipient, eventAmount string
						for _, attr := range event.Attributes {
							switch attr.Key {
							case "sender":
								eventSender = attr.Value
							case "recipient":
								eventRecipient = attr.Value
							case "amount":
								eventAmount = attr.Value
							}
						}
						if firstTransfer == "" {
							firstTransfer, firstRecipient = eventAmount, eventRecipient
						}
						// A single transaction (like MsgSubmitPoW,
						// which distributes a miner cut and a
						// treasury cut as two separate real
						// transfers) can contain more than one
						// transfer event -- only use the one that
						// actually involves this address, not
						// whichever happens to be processed last.
						// Direction is determined here too, from the
						// real transfer's own sender/recipient
						// fields, not from which query happened to
						// find this transaction first -- a miner
						// submitting MsgSubmitPoW is genuinely both
						// the message's sender and a transfer
						// recipient of their own reward, and
						// "received" is the more honest, useful
						// label for a real balance gain, regardless
						// of which query matched it.
						if eventRecipient == address {
							amount = eventAmount
							direction = "received"
							counterparty = eventSender
						} else if eventSender == address {
							amount = eventAmount
							direction = "sent"
							counterparty = eventRecipient
						}
					}
				}
				// Signed it, but no transfer touches this address:
				// it moved someone else's funds -- e.g. an x/authz
				// MsgExec spending a granter's balance.
				if amount == "" && direction == "sent" {
					amount = firstTransfer
					counterparty = firstRecipient
				}
				all = append(all, Transaction{
					Hash:               txResp.TxHash,
					Height:             txResp.Height,
					Code:               txResp.Code,
					Direction:          direction,
					Amount:             amount,
					Timestamp:          txResp.Timestamp,
					Memo:               memoAt(resp.Txs, i),
					Counterparty:       counterparty,
					CounterpartyModule: moduleAccountName(counterparty),
					MsgType:            msgTypeAt(resp.Txs, i),
				})
			}
	}

		// A single real transaction can genuinely match both queries above
	// -- MsgSubmitPoW's miner is simultaneously the message sender and
	// a transfer recipient of their own reward, unlike an ordinary
	// bank send, where an address is normally only ever one or the
	// other. Deduplicate by hash so each real transaction appears
	// exactly once, keeping whichever entry was found first.
	seen := make(map[string]bool)
	var deduped []Transaction
	for _, t := range all {
		if seen[t.Hash] {
			continue
		}
		seen[t.Hash] = true
		deduped = append(deduped, t)
	}
	all = deduped

	sort.Slice(all, func(i, j int) bool {
		return all[i].Height > all[j].Height
	})

	if uint64(len(all)) > limit {
		all = all[:limit]
	}

	return all, nil
}

// TransactionDetail is a fuller view of a single transaction, for
// direct hash lookups (as opposed to Transaction, the summarized view
// used for per-address history listings).
type TransactionDetail struct {
	Hash      string
	Height    int64
	Code      uint32
	Codespace string
	RawLog    string
	GasUsed   int64
	GasWanted int64
	From      string
	To        string
	Amount    string
	Timestamp string
	Transfers []Transfer
	AuxPow    *AuxPowInfo
	Memo      string // set by the sender; untrusted
}

// ErrTransactionNotFound means the node has no record of the hash:
// not yet in a block (or dropped from the mempool), as opposed to a
// failed lookup.
var ErrTransactionNotFound = errors.New("transaction not found")

func msgTypeAt(txs []*txtypes.Tx, i int) string {
	if i >= len(txs) || txs[i] == nil || txs[i].Body == nil || len(txs[i].Body.Messages) == 0 {
		return ""
	}
	url := txs[i].Body.Messages[0].TypeUrl
	return url[strings.LastIndex(url, ".")+1:]
}

func memoAt(txs []*txtypes.Tx, i int) string {
	if i < len(txs) && txs[i] != nil && txs[i].Body != nil {
		return txs[i].Body.Memo
	}
	return ""
}

// Transfer is one real bank transfer within a transaction. A single
// transaction can genuinely contain multiple transfers -- a
// MsgSubmitPoW, for instance, distributes the miner's cut, the
// treasury's cut, and potentially a validator's cut, as separate real
// transfer events, not just one.
type Transfer struct {
	From   string
	To     string
	Amount string
}

// GetTransactionByHash looks up a single, specific real transaction by
// its hash, returning full detail rather than the summarized view
// GetTransactionHistory provides.
func (c *Client) GetTransactionByHash(hash string) (*TransactionDetail, error) {
	txClient := txtypes.NewServiceClient(c.conn)

	resp, err := txClient.GetTx(context.Background(), &txtypes.GetTxRequest{Hash: hash})
	if status.Code(err) == codes.NotFound {
		return nil, fmt.Errorf("%w: %s", ErrTransactionNotFound, hash)
	}
	if err != nil {
		return nil, err
	}

	detail := &TransactionDetail{
		Hash:      resp.TxResponse.TxHash,
		Height:    resp.TxResponse.Height,
		Code:      resp.TxResponse.Code,
		Codespace: resp.TxResponse.Codespace,
		RawLog:    resp.TxResponse.RawLog,
		GasUsed:   resp.TxResponse.GasUsed,
		GasWanted: resp.TxResponse.GasWanted,
		Timestamp: resp.TxResponse.Timestamp,
	}

		for _, event := range resp.TxResponse.Events {
		if event.Type == "transfer" {
			var t Transfer
			for _, attr := range event.Attributes {
				switch attr.Key {
				case "sender":
					t.From = attr.Value
				case "recipient":
					t.To = attr.Value
				case "amount":
					t.Amount = attr.Value
				}
			}
			detail.Transfers = append(detail.Transfers, t)
		}
	}

	// From/To/Amount stay as a simple summary (the first real transfer
	// found), matching this struct's original, narrower shape -- but
	// Transfers holds the complete, honest picture for anything with
	// more than one real transfer, like a MsgSubmitPoW's miner and
	// treasury cuts.
	if len(detail.Transfers) > 0 {
		detail.From = detail.Transfers[0].From
		detail.To = detail.Transfers[0].To
		detail.Amount = detail.Transfers[0].Amount
	}

	if resp.Tx != nil && resp.Tx.Body != nil {
		detail.Memo = resp.Tx.Body.Memo
		for _, anyMsg := range resp.Tx.Body.Messages {
			if anyMsg.TypeUrl != "/aether.pow.v1.MsgSubmitPoW" {
				continue
			}
			var msg pow.MsgSubmitPoW
			if err := proto.Unmarshal(anyMsg.Value, &msg); err != nil {
				continue
			}
			if auxPow, ok := msg.Submission.(*pow.MsgSubmitPoW_AuxPow); ok {
				detail.AuxPow = &AuxPowInfo{
					ParentHeaderBase64: base64.StdEncoding.EncodeToString(auxPow.AuxPow.ParentHeader),
					CoinbaseTxBase64:   base64.StdEncoding.EncodeToString(auxPow.AuxPow.CoinbaseTx),
					AuxBlockHashBase64: base64.StdEncoding.EncodeToString(auxPow.AuxPow.AuxBlockHash),
				}
			}
		}
	}
	return detail, nil
}

// AuxPowInfo surfaces the AuxPoW-specific fields of a MsgSubmitPoW
// transaction, when present -- for full transparency on the
// explorer's transaction detail page, rather than only showing the
// generic transfer/gas/status fields that apply to any transaction.
type AuxPowInfo struct {
	ParentHeaderBase64 string
	CoinbaseTxBase64   string
	AuxBlockHashBase64 string
}

// RecentTransaction is a chain-wide (not address-scoped) summary of a
// real transaction, for a "recent activity" feed.
type RecentTransaction struct {
	Hash      string
	Height    int64
	Code      uint32
	MsgType   string // e.g. "MsgSubmitPoW", extracted from the first message's type URL
	Timestamp string
}

// msgTypeFromURL turns a proto type URL like
// "/aether.pow.v1.MsgSubmitPoW" into just "MsgSubmitPoW" -- the part
// an explorer's viewer actually wants to read, not the full package
// path.
func msgTypeFromURL(typeURL string) string {
	idx := strings.LastIndex(typeURL, ".")
	if idx == -1 || idx == len(typeURL)-1 {
		return typeURL
	}
	return typeURL[idx+1:]
}

// GetRecentTransactions returns the most recent real transactions
// chain-wide, newest first -- not scoped to any one address. Uses the
// same Cosmos SDK tx service GetTransactionHistory already relies on;
// "tx.height>0" is a deliberate always-true event filter, since the
// tx-service query language requires at least one condition and has
// no dedicated "match everything" syntax.
func (c *Client) GetRecentTransactions(limit uint64) ([]RecentTransaction, error) {
	txClient := txtypes.NewServiceClient(c.conn)

	resp, err := txClient.GetTxsEvent(context.Background(), &txtypes.GetTxsEventRequest{
		Query:   "tx.height>0",
		OrderBy: txtypes.OrderBy_ORDER_BY_DESC,
		Limit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query recent transactions: %w", err)
	}

	txs := make([]RecentTransaction, 0, len(resp.TxResponses))
	for i, txResp := range resp.TxResponses {
		msgType := ""
		if i < len(resp.Txs) && resp.Txs[i].Body != nil && len(resp.Txs[i].Body.Messages) > 0 {
			msgType = msgTypeFromURL(resp.Txs[i].Body.Messages[0].TypeUrl)
		}
		txs = append(txs, RecentTransaction{
			Hash:      txResp.TxHash,
			Height:    txResp.Height,
			Code:      txResp.Code,
			MsgType:   msgType,
			Timestamp: txResp.Timestamp,
		})
	}

	return txs, nil
}
