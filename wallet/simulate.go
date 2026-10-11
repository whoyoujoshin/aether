package wallet

import (
	"context"

	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

// Simulate runs signed transaction bytes through the node's simulation
// (the ante handler and the messages, without committing) and returns
// the gas it used. The node skips signature verification in simulation,
// so a caller that needs the signature checked does that itself.
func (c *Client) Simulate(ctx context.Context, txBytes []byte) (gasUsed uint64, err error) {
	resp, err := txtypes.NewServiceClient(c.conn).Simulate(ctx, &txtypes.SimulateRequest{TxBytes: txBytes})
	if err != nil {
		return 0, err
	}
	if resp.GasInfo == nil {
		return 0, nil
	}
	return resp.GasInfo.GasUsed, nil
}
