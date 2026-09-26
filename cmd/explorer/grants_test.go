package main

import (
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/wallet"
)

func TestPermissionDTOs(t *testing.T) {
	exp := time.Date(2026, 10, 3, 14, 0, 0, 0, time.FixedZone("x", 3600))
	out := toPermissionDTOs([]wallet.Permission{
		{Account: "a", Send: &wallet.Grant{Kind: "send", SpendLimit: sdk.NewCoins(sdk.NewInt64Coin("uaeth", 2_500_000)), AllowList: []string{"p"}, Expiration: &exp},
			Fees: &wallet.FeeAllowance{Kind: "basic", Expiration: &exp}},
		{Account: "b", Send: &wallet.Grant{Kind: "send", Unlimited: true}, Other: []wallet.Grant{{Kind: "/cosmos.gov.v1.MsgVote"}}},
	})
	bz, err := json.Marshal(out)
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"account":"a","send":{"unlimited":false,"spendLimit":"2500000","allowList":["p"],"expiration":"2026-10-03T13:00:00Z"},
		 "fees":{"kind":"basic","spendLimit":"","expiration":"2026-10-03T13:00:00Z"},"other":[]},
		{"account":"b","send":{"unlimited":true,"spendLimit":"","allowList":[],"expiration":""},"fees":null,"other":["/cosmos.gov.v1.MsgVote"]}
	]`, string(bz))
}
