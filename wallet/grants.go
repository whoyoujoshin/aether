package wallet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"cosmossdk.io/x/feegrant"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Listing, creating and revoking the two kinds of permission an
// account can give another on chain: to send from it (x/authz) and to
// have its transaction fees paid (x/feegrant).

// ErrFeegrantNotActive means the node doesn't serve x/feegrant yet.
var ErrFeegrantNotActive = errors.New("x/feegrant is not active on this node yet")

// Grant is one account's permission to act for another.
type Grant struct {
	Granter string `json:"granter"`
	Grantee string `json:"grantee"`
	// Kind is "send" (a SendAuthorization, or unlimited sending) or, for
	// anything else, the authorization's type URL.
	Kind       string     `json:"kind"`
	Unlimited  bool       `json:"unlimited,omitempty"`
	SpendLimit sdk.Coins  `json:"spendLimit,omitempty"`
	AllowList  []string   `json:"allowList,omitempty"`
	Expiration *time.Time `json:"expiration,omitempty"`
}

// FeeAllowance is one account's permission to have its fees paid by
// another.
type FeeAllowance struct {
	Granter string `json:"granter"`
	Grantee string `json:"grantee"`
	// Kind is "basic", "periodic" or the allowance's type URL; an
	// allowance limited to certain messages reports what it wraps.
	Kind string `json:"kind"`
	// SpendLimit is empty for an unlimited allowance.
	SpendLimit sdk.Coins  `json:"spendLimit,omitempty"`
	Expiration *time.Time `json:"expiration,omitempty"`
	// Period and PeriodLimit are set for a periodic allowance.
	Period      time.Duration `json:"period,omitempty"`
	PeriodLimit sdk.Coins     `json:"periodLimit,omitempty"`
	AllowedMsgs []string      `json:"allowedMsgs,omitempty"`
}

const maxListed = 200

var msgSendURL = sdk.MsgTypeURL(&banktypes.MsgSend{})

// GrantsGiven lists the authz grants address has given.
func (c *Client) GrantsGiven(address string) ([]Grant, error) {
	resp, err := authz.NewQueryClient(c.conn).GranterGrants(context.Background(), &authz.QueryGranterGrantsRequest{
		Granter: address, Pagination: &query.PageRequest{Limit: maxListed},
	})
	if err != nil {
		return nil, authzErr(err)
	}
	return grantsFrom(resp.Grants)
}

// GrantsReceived lists the authz grants address has received.
func (c *Client) GrantsReceived(address string) ([]Grant, error) {
	resp, err := authz.NewQueryClient(c.conn).GranteeGrants(context.Background(), &authz.QueryGranteeGrantsRequest{
		Grantee: address, Pagination: &query.PageRequest{Limit: maxListed},
	})
	if err != nil {
		return nil, authzErr(err)
	}
	return grantsFrom(resp.Grants)
}

func authzErr(err error) error {
	if status.Code(err) == codes.Unimplemented {
		return ErrAuthzNotActive
	}
	return err
}

func grantsFrom(in []*authz.GrantAuthorization) ([]Grant, error) {
	out := make([]Grant, 0, len(in))
	for _, g := range in {
		if g.Authorization == nil {
			continue
		}
		grant := Grant{Granter: g.Granter, Grantee: g.Grantee, Expiration: g.Expiration, Kind: g.Authorization.TypeUrl}
		if g.Authorization.TypeUrl == "/"+proto.MessageName(&authz.GenericAuthorization{}) {
			var a authz.GenericAuthorization
			if err := proto.Unmarshal(g.Authorization.Value, &a); err != nil {
				return nil, fmt.Errorf("failed to decode authorization: %w", err)
			}
			if a.Msg != msgSendURL {
				grant.Kind = a.Msg
				out = append(out, grant)
				continue
			}
		}
		if send, err := decodeSendGrant(g.Authorization, g.Expiration); err == nil {
			grant.Kind, grant.Unlimited, grant.SpendLimit, grant.AllowList = "send", send.Unlimited, send.SpendLimit, send.AllowList
		}
		out = append(out, grant)
	}
	return out, nil
}

// FeeAllowancesGiven lists the fee allowances address pays for.
func (c *Client) FeeAllowancesGiven(address string) ([]FeeAllowance, error) {
	resp, err := feegrant.NewQueryClient(c.conn).AllowancesByGranter(context.Background(), &feegrant.QueryAllowancesByGranterRequest{
		Granter: address, Pagination: &query.PageRequest{Limit: maxListed},
	})
	if err != nil {
		return nil, feegrantErr(err)
	}
	return allowancesFrom(resp.Allowances)
}

// FeeAllowancesReceived lists the fee allowances covering address.
func (c *Client) FeeAllowancesReceived(address string) ([]FeeAllowance, error) {
	resp, err := feegrant.NewQueryClient(c.conn).Allowances(context.Background(), &feegrant.QueryAllowancesRequest{
		Grantee: address, Pagination: &query.PageRequest{Limit: maxListed},
	})
	if err != nil {
		return nil, feegrantErr(err)
	}
	return allowancesFrom(resp.Allowances)
}

func feegrantErr(err error) error {
	if status.Code(err) == codes.Unimplemented {
		return ErrFeegrantNotActive
	}
	return err
}

func allowancesFrom(in []*feegrant.Grant) ([]FeeAllowance, error) {
	out := make([]FeeAllowance, 0, len(in))
	for _, g := range in {
		a := FeeAllowance{Granter: g.Granter, Grantee: g.Grantee}
		if err := describeAllowance(g.Allowance, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func describeAllowance(any *codectypes.Any, a *FeeAllowance) error {
	if any == nil {
		a.Kind = "unknown"
		return nil
	}
	switch any.TypeUrl {
	case "/" + proto.MessageName(&feegrant.BasicAllowance{}):
		var b feegrant.BasicAllowance
		if err := proto.Unmarshal(any.Value, &b); err != nil {
			return fmt.Errorf("failed to decode fee allowance: %w", err)
		}
		a.Kind, a.SpendLimit, a.Expiration = "basic", b.SpendLimit, b.Expiration
	case "/" + proto.MessageName(&feegrant.PeriodicAllowance{}):
		var p feegrant.PeriodicAllowance
		if err := proto.Unmarshal(any.Value, &p); err != nil {
			return fmt.Errorf("failed to decode fee allowance: %w", err)
		}
		a.Kind, a.SpendLimit, a.Expiration = "periodic", p.Basic.SpendLimit, p.Basic.Expiration
		a.Period, a.PeriodLimit = p.Period, p.PeriodSpendLimit
	case "/" + proto.MessageName(&feegrant.AllowedMsgAllowance{}):
		var m feegrant.AllowedMsgAllowance
		if err := proto.Unmarshal(any.Value, &m); err != nil {
			return fmt.Errorf("failed to decode fee allowance: %w", err)
		}
		if err := describeAllowance(m.Allowance, a); err != nil {
			return err
		}
		a.AllowedMsgs = m.AllowedMessages
	default:
		a.Kind = any.TypeUrl
	}
	return nil
}

// SendGrantMsg lets grantee send up to limit from granter until
// expiration, only to allowList if it's non-empty. Granting again to
// the same grantee replaces the previous grant (and its limit).
func SendGrantMsg(granter, grantee string, limit sdk.Coins, allowList []string, expiration time.Time) (*authz.MsgGrant, error) {
	granterAddr, granteeAddr, err := pair(granter, grantee)
	if err != nil {
		return nil, err
	}
	if !limit.IsAllPositive() {
		return nil, errors.New("a send grant needs a positive spend limit")
	}
	for _, a := range allowList {
		if _, err := sdk.AccAddressFromBech32(a); err != nil {
			return nil, fmt.Errorf("invalid allowed recipient %q: %w", a, err)
		}
	}
	return authz.NewMsgGrant(granterAddr, granteeAddr, banktypes.NewSendAuthorization(limit, addrs(allowList)), &expiration)
}

// RevokeSendMsg withdraws grantee's permission to send from granter.
func RevokeSendMsg(granter, grantee string) (*authz.MsgRevoke, error) {
	granterAddr, granteeAddr, err := pair(granter, grantee)
	if err != nil {
		return nil, err
	}
	msg := authz.NewMsgRevoke(granterAddr, granteeAddr, msgSendURL)
	return &msg, nil
}

// FeeAllowanceMsg has granter pay grantee's fees, up to limit (empty:
// no limit) until expiration.
func FeeAllowanceMsg(granter, grantee string, limit sdk.Coins, expiration time.Time) (*feegrant.MsgGrantAllowance, error) {
	granterAddr, granteeAddr, err := pair(granter, grantee)
	if err != nil {
		return nil, err
	}
	return feegrant.NewMsgGrantAllowance(&feegrant.BasicAllowance{SpendLimit: limit, Expiration: &expiration}, granterAddr, granteeAddr)
}

// RevokeFeeAllowanceMsg stops granter paying grantee's fees.
func RevokeFeeAllowanceMsg(granter, grantee string) (*feegrant.MsgRevokeAllowance, error) {
	granterAddr, granteeAddr, err := pair(granter, grantee)
	if err != nil {
		return nil, err
	}
	msg := feegrant.NewMsgRevokeAllowance(granterAddr, granteeAddr)
	return &msg, nil
}

// ExecSendMsg has grantee send amount from granter to `to`, under a send
// grant granter gave it.
func ExecSendMsg(grantee, granter, to string, amount sdk.Coins) (*authz.MsgExec, error) {
	granteeAddr, granterAddr, err := pair(grantee, granter)
	if err != nil {
		return nil, err
	}
	toAddr, err := sdk.AccAddressFromBech32(to)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient %q: %w", to, err)
	}
	msg := authz.NewMsgExec(granteeAddr, []sdk.Msg{banktypes.NewMsgSend(granterAddr, toAddr, amount)})
	return &msg, nil
}

func pair(a, b string) (sdk.AccAddress, sdk.AccAddress, error) {
	aa, err := sdk.AccAddressFromBech32(a)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid address %q: %w", a, err)
	}
	ba, err := sdk.AccAddressFromBech32(b)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid address %q: %w", b, err)
	}
	if aa.Equals(ba) {
		return nil, nil, errors.New("granter and grantee must be different accounts")
	}
	return aa, ba, nil
}

func addrs(in []string) []sdk.AccAddress {
	out := make([]sdk.AccAddress, len(in))
	for i, a := range in {
		out[i] = sdk.MustAccAddressFromBech32(a)
	}
	return out
}

// Permission is everything one account lets another do: its send
// grant, other authz grants and fee allowance, merged.
type Permission struct {
	Account string        `json:"account"` // the other side
	Send    *Grant        `json:"send,omitempty"`
	Other   []Grant       `json:"other,omitempty"` // non-send authz grants
	Fees    *FeeAllowance `json:"fees,omitempty"`
}

// Permissions returns what address has let other accounts do (given)
// and what others have let it do (received), one entry per account.
func (c *Client) Permissions(address string) (given, received []Permission, err error) {
	return LoadPermissions(c, address)
}

// PermissionSource is what LoadPermissions needs from a node.
type PermissionSource interface {
	GrantsGiven(address string) ([]Grant, error)
	GrantsReceived(address string) ([]Grant, error)
	FeeAllowancesGiven(address string) ([]FeeAllowance, error)
	FeeAllowancesReceived(address string) ([]FeeAllowance, error)
}

func LoadPermissions(c PermissionSource, address string) (given, received []Permission, err error) {
	g, err := c.GrantsGiven(address)
	if err != nil {
		return nil, nil, err
	}
	r, err := c.GrantsReceived(address)
	if err != nil {
		return nil, nil, err
	}
	fg, err := c.FeeAllowancesGiven(address)
	if err != nil {
		return nil, nil, err
	}
	fr, err := c.FeeAllowancesReceived(address)
	if err != nil {
		return nil, nil, err
	}
	given = MergePermissions(g, fg, func(x Grant) string { return x.Grantee }, func(x FeeAllowance) string { return x.Grantee })
	received = MergePermissions(r, fr, func(x Grant) string { return x.Granter }, func(x FeeAllowance) string { return x.Granter })
	return given, received, nil
}

// MergePermissions groups grants and fee allowances by the account on
// their other side, sorted by address.
func MergePermissions(grants []Grant, fees []FeeAllowance, grantOther func(Grant) string, feeOther func(FeeAllowance) string) []Permission {
	by := map[string]*Permission{}
	get := func(acc string) *Permission {
		if by[acc] == nil {
			by[acc] = &Permission{Account: acc}
		}
		return by[acc]
	}
	for _, g := range grants {
		p := get(grantOther(g))
		if g.Kind == "send" {
			g := g
			p.Send = &g
		} else {
			p.Other = append(p.Other, g)
		}
	}
	for _, f := range fees {
		f := f
		get(feeOther(f)).Fees = &f
	}
	out := make([]Permission, 0, len(by))
	for _, p := range by {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}
