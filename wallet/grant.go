package wallet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrAuthzNotActive means the node doesn't serve x/authz at all:
	// it's below app.AuthzFeegrantActivationHeight (or an older binary).
	ErrAuthzNotActive = errors.New("x/authz is not active on this node yet")
	// ErrGrantNotFound means the granter hasn't granted the grantee
	// permission to send (or revoked it, or it was used up).
	ErrGrantNotFound = errors.New("no send grant found")
)

// SendGrant is what a grantee may spend from a granter's account via
// x/authz MsgExec.
type SendGrant struct {
	// Unlimited is true for a GenericAuthorization on MsgSend: no cap
	// beyond the granter's balance. Otherwise SpendLimit is the cap
	// remaining; the chain lowers it with every spend.
	Unlimited  bool
	SpendLimit sdk.Coins
	// AllowList, if non-empty, is the only recipients allowed.
	AllowList  []string
	Expiration *time.Time
}

// GetSendGrant returns the grantee's current permission to send from
// the granter's account, as stored on chain. The chain treats a grant
// past its Expiration as absent even before pruning it, so callers
// should check Expiration themselves.
func (c *Client) GetSendGrant(granter, grantee string) (*SendGrant, error) {
	resp, err := authz.NewQueryClient(c.conn).Grants(context.Background(), &authz.QueryGrantsRequest{
		Granter:    granter,
		Grantee:    grantee,
		MsgTypeUrl: sdk.MsgTypeURL(&banktypes.MsgSend{}),
	})
	if status.Code(err) == codes.Unimplemented {
		return nil, ErrAuthzNotActive
	}
	if err != nil {
		if status.Code(err) == codes.NotFound || strings.Contains(err.Error(), authz.ErrNoAuthorizationFound.Error()) {
			return nil, fmt.Errorf("%w from %s to %s", ErrGrantNotFound, granter, grantee)
		}
		return nil, err
	}
	if len(resp.Grants) == 0 || resp.Grants[0].Authorization == nil {
		return nil, fmt.Errorf("%w from %s to %s", ErrGrantNotFound, granter, grantee)
	}
	return decodeSendGrant(resp.Grants[0].Authorization, resp.Grants[0].Expiration)
}

// decodeSendGrant reads a SendAuthorization, or a GenericAuthorization
// (for MsgSend: that's the only way one comes back from a MsgSend
// query), into a SendGrant.
func decodeSendGrant(auth *codectypes.Any, expiration *time.Time) (*SendGrant, error) {
	out := &SendGrant{Expiration: expiration}
	switch auth.TypeUrl {
	case "/" + proto.MessageName(&banktypes.SendAuthorization{}):
		var a banktypes.SendAuthorization
		if err := proto.Unmarshal(auth.Value, &a); err != nil {
			return nil, fmt.Errorf("failed to decode send authorization: %w", err)
		}
		out.SpendLimit, out.AllowList = a.SpendLimit, a.AllowList
	case "/" + proto.MessageName(&authz.GenericAuthorization{}):
		out.Unlimited = true
	default:
		return nil, fmt.Errorf("unsupported authorization type %s", auth.TypeUrl)
	}
	return out, nil
}
