package wallet

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/whoyoujoshin/aether/x/pow"
	"github.com/whoyoujoshin/aether/x/treasury"
)

// ChainStatus is the chain at a glance, every field as of Height.
type ChainStatus struct {
	ChainID string `json:"chainId"`
	Height  int64  `json:"height"`
	// BlockTime is when the latest block was made (RFC 3339).
	BlockTime string `json:"blockTime"`
	// Zero when it couldn't be measured (a chain under 2 blocks).
	AvgBlockSeconds float64 `json:"avgBlockSeconds"`

	Epoch           int64 `json:"epoch"`
	EpochLength     int64 `json:"epochLength"`
	SelectionHeight int64 `json:"selectionHeight"`

	Difficulty       string `json:"difficulty"`
	BlockRewardUaeth string `json:"blockRewardUaeth"`
	ActiveValidators int    `json:"activeValidators"`
	TopKSize         int64  `json:"topKSize"`
	TreasuryUaeth    string `json:"treasuryUaeth"`
}

// ChainStatus reads it from the node's latest block.
func (c *Client) ChainStatus(ctx context.Context) (*ChainStatus, error) {
	cmt := cmtservice.NewServiceClient(c.conn)
	latest, err := cmt.GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return nil, fmt.Errorf("latest block: %w", err)
	}
	if latest.SdkBlock == nil {
		return nil, fmt.Errorf("latest block: node returned no block")
	}
	hdr := latest.SdkBlock.Header
	at := metadata.AppendToOutgoingContext(ctx, grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(hdr.Height, 10))
	q := pow.NewQueryClient(c.conn)

	params, err := q.Params(at, &pow.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("pow params: %w", err)
	}
	reward, err := q.BlockReward(at, &pow.QueryBlockRewardRequest{})
	if err != nil {
		return nil, fmt.Errorf("block reward: %w", err)
	}
	active, err := q.ActiveValidators(at, &pow.QueryActiveValidatorsRequest{})
	if err != nil {
		return nil, fmt.Errorf("active validators: %w", err)
	}
	tr, err := treasury.NewQueryClient(c.conn).Balance(at, &treasury.QueryBalanceRequest{})
	if err != nil {
		return nil, fmt.Errorf("treasury: %w", err)
	}

	epoch, _, selection := EpochWindow(hdr.Height, params.EpochLength)
	st := &ChainStatus{
		ChainID:          hdr.ChainID,
		Height:           hdr.Height,
		BlockTime:        hdr.Time.UTC().Format(time.RFC3339),
		Epoch:            epoch,
		EpochLength:      params.EpochLength,
		SelectionHeight:  selection,
		Difficulty:       params.Difficulty,
		BlockRewardUaeth: reward.BlockReward,
		ActiveValidators: len(active.Validators),
		TopKSize:         params.TopKSize,
		TreasuryUaeth:    tr.RealBankBalance,
	}
	if avg, ok := c.avgBlockTime(ctx, cmt, hdr.Height, hdr.Time.UnixNano()); ok {
		st.AvgBlockSeconds = avg
	}
	return st, nil
}
