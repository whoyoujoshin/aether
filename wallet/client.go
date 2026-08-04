package wallet

import (
	"context"
	"strconv"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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