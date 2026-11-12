package app

import (
	"context"
	"fmt"

	sdkerrors "cosmossdk.io/errors"
	"cosmossdk.io/x/feegrant"
	feegrantkeeper "cosmossdk.io/x/feegrant/keeper"
	feegrantmodule "cosmossdk.io/x/feegrant/module"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/authz"
	authzkeeper "github.com/cosmos/cosmos-sdk/x/authz/keeper"
	authzmodule "github.com/cosmos/cosmos-sdk/x/authz/module"

	"github.com/whoyoujoshin/aether/x/accountauth"
)

// An authz grant or a fee allowance made by a key the granter no longer
// has is refused (x/accountauth/keygen.go, GHSA-cgcr-f6g2-ch5v). authz and
// feegrant are the SDK's own modules, so the check sits around them:
// their message servers record the granter's key generation when a grant
// or allowance is made, authz's Exec and the ante handler's use of an
// allowance check it. Every path to them goes through the message router
// (a top-level transaction, authz itself, an authenticator, ICA, a
// proposal), so every path is covered. Before KeyChangeRevokesHeight none
// of this records or refuses anything.

// retiringAuthzModule is authz's module with its message server wrapped.
type retiringAuthzModule struct {
	authzmodule.AppModule
	keeper authzkeeper.Keeper
	aa     *accountauth.Keeper
	cdc    codec.Codec
}

func (m retiringAuthzModule) RegisterServices(cfg module.Configurator) {
	authz.RegisterQueryServer(cfg.QueryServer(), m.keeper)
	authz.RegisterMsgServer(cfg.MsgServer(), retiringAuthzMsgServer{MsgServer: m.keeper, aa: m.aa, cdc: m.cdc})
	if err := cfg.RegisterMigration(authz.ModuleName, 1, authzkeeper.NewMigrator(m.keeper).Migrate1to2); err != nil {
		panic(fmt.Sprintf("failed to migrate x/%s from version 1 to 2: %v", authz.ModuleName, err))
	}
}

type retiringAuthzMsgServer struct {
	authz.MsgServer
	aa  *accountauth.Keeper
	cdc codec.Codec
}

func (s retiringAuthzMsgServer) Grant(goCtx context.Context, msg *authz.MsgGrant) (*authz.MsgGrantResponse, error) {
	res, err := s.MsgServer.Grant(goCtx, msg)
	if err != nil {
		return nil, err
	}
	auth, err := msg.GetAuthorization()
	if err != nil {
		return nil, err
	}
	s.aa.RecordGrant(sdk.UnwrapSDKContext(goCtx), sdk.MustAccAddressFromBech32(msg.Granter), sdk.MustAccAddressFromBech32(msg.Grantee), auth.MsgTypeURL())
	return res, nil
}

func (s retiringAuthzMsgServer) Revoke(goCtx context.Context, msg *authz.MsgRevoke) (*authz.MsgRevokeResponse, error) {
	res, err := s.MsgServer.Revoke(goCtx, msg)
	if err != nil {
		return nil, err
	}
	s.aa.ForgetGrant(sdk.UnwrapSDKContext(goCtx), sdk.MustAccAddressFromBech32(msg.Granter), sdk.MustAccAddressFromBech32(msg.Grantee), msg.MsgTypeUrl)
	return res, nil
}

func (s retiringAuthzMsgServer) Exec(goCtx context.Context, msg *authz.MsgExec) (*authz.MsgExecResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if accountauth.KeyChangeRevokes(ctx) {
		grantee, err := sdk.AccAddressFromBech32(msg.Grantee)
		if err != nil {
			return nil, err
		}
		msgs, err := msg.GetMessages()
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			signers, _, err := s.cdc.GetMsgV1Signers(m)
			if err != nil || len(signers) != 1 {
				continue // authz refuses these itself
			}
			granter := sdk.AccAddress(signers[0])
			if granter.Equals(grantee) {
				continue // no grant involved
			}
			if !s.aa.GrantCurrent(ctx, granter, grantee, sdk.MsgTypeURL(m)) {
				return nil, sdkerrors.Wrapf(accountauth.ErrDelegationRetired,
					"the grant from %s for %s was made before its key last changed", granter, sdk.MsgTypeURL(m))
			}
		}
	}
	return s.MsgServer.Exec(goCtx, msg)
}

// retiringFeegrantModule is feegrant's module with its message server
// wrapped.
type retiringFeegrantModule struct {
	feegrantmodule.AppModule
	keeper feegrantkeeper.Keeper
	aa     *accountauth.Keeper
}

func (m retiringFeegrantModule) RegisterServices(cfg module.Configurator) {
	feegrant.RegisterMsgServer(cfg.MsgServer(), retiringFeegrantMsgServer{MsgServer: feegrantkeeper.NewMsgServerImpl(m.keeper), aa: m.aa})
	feegrant.RegisterQueryServer(cfg.QueryServer(), m.keeper)
	if err := cfg.RegisterMigration(feegrant.ModuleName, 1, feegrantkeeper.NewMigrator(m.keeper).Migrate1to2); err != nil {
		panic(fmt.Sprintf("failed to migrate x/feegrant from version 1 to 2: %v", err))
	}
}

type retiringFeegrantMsgServer struct {
	feegrant.MsgServer
	aa *accountauth.Keeper
}

func (s retiringFeegrantMsgServer) GrantAllowance(goCtx context.Context, msg *feegrant.MsgGrantAllowance) (*feegrant.MsgGrantAllowanceResponse, error) {
	res, err := s.MsgServer.GrantAllowance(goCtx, msg)
	if err != nil {
		return nil, err
	}
	s.aa.RecordAllowance(sdk.UnwrapSDKContext(goCtx), sdk.MustAccAddressFromBech32(msg.Granter), sdk.MustAccAddressFromBech32(msg.Grantee))
	return res, nil
}

func (s retiringFeegrantMsgServer) RevokeAllowance(goCtx context.Context, msg *feegrant.MsgRevokeAllowance) (*feegrant.MsgRevokeAllowanceResponse, error) {
	res, err := s.MsgServer.RevokeAllowance(goCtx, msg)
	if err != nil {
		return nil, err
	}
	s.aa.ForgetAllowance(sdk.UnwrapSDKContext(goCtx), sdk.MustAccAddressFromBech32(msg.Granter), sdk.MustAccAddressFromBech32(msg.Grantee))
	return res, nil
}

// retiringFeegrant wraps the ante handler's feegrant keeper; nil (fee
// grants not wired yet) stays nil.
func retiringFeegrant(k authante.FeegrantKeeper, aa *accountauth.Keeper) authante.FeegrantKeeper {
	if k == nil {
		return nil
	}
	return retiringFeegrantKeeper{FeegrantKeeper: k, aa: aa}
}

// retiringFeegrantKeeper is what the ante handler's fee deduction uses:
// an allowance made by a key the granter no longer has pays nothing.
type retiringFeegrantKeeper struct {
	authante.FeegrantKeeper
	aa *accountauth.Keeper
}

func (k retiringFeegrantKeeper) UseGrantedFees(goCtx context.Context, granter, grantee sdk.AccAddress, fee sdk.Coins, msgs []sdk.Msg) error {
	if !k.aa.AllowanceCurrent(sdk.UnwrapSDKContext(goCtx), granter, grantee) {
		return sdkerrors.Wrapf(accountauth.ErrDelegationRetired, "the fee allowance from %s was made before its key last changed", granter)
	}
	return k.FeegrantKeeper.UseGrantedFees(goCtx, granter, grantee, fee, msgs)
}
