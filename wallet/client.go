package wallet

import (
	"context"
	"strconv"
	"fmt"
	"sort"
	"encoding/base64"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

// NewClient connects to a node's gRPC endpoint (e.g. "localhost:9090").
// Uses an insecure (non-TLS) connection, matching this project's
// existing devnet tooling (cmd/balancecheck) -- appropriate for a
// devnet/testnet context, not yet hardened for a public mainnet
// deployment where a real TLS-secured endpoint would be expected.
func NewClient(grpcEndpoint string) (*Client, error) {
	conn, err := grpc.NewClient(grpcEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

								for _, txResp := range resp.TxResponses {
				amount := ""
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
						} else if eventSender == address {
							amount = eventAmount
							direction = "sent"
						}
					}
				}
				all = append(all, Transaction{
					Hash:      txResp.TxHash,
					Height:    txResp.Height,
					Code:      txResp.Code,
					Direction: direction,
					Amount:    amount,
					Timestamp: txResp.Timestamp,
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
	RawLog    string
	GasUsed   int64
	GasWanted int64
	From      string
	To        string
	Amount    string
	Timestamp string
	Transfers []Transfer
	AuxPow    *AuxPowInfo
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
	if err != nil {
		return nil, err
	}

	detail := &TransactionDetail{
		Hash:      resp.TxResponse.TxHash,
		Height:    resp.TxResponse.Height,
		Code:      resp.TxResponse.Code,
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
