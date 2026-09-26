package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/wallet"
)

// --- GET /api/grants?addr= ---
//
// Who may spend from an address (x/authz) or have their fees paid by it
// (x/feegrant), and what it may do for others.

type sendPermDTO struct {
	Unlimited  bool     `json:"unlimited"`
	SpendLimit string   `json:"spendLimit"` // uaeth left; "" if unlimited
	AllowList  []string `json:"allowList"`  // empty: any recipient
	Expiration string   `json:"expiration"` // RFC 3339; "" if none
}

type feePermDTO struct {
	Kind       string `json:"kind"`
	SpendLimit string `json:"spendLimit"` // uaeth left; "" if unlimited
	Expiration string `json:"expiration"`
}

type permissionDTO struct {
	Account string       `json:"account"`
	Send    *sendPermDTO `json:"send"`
	Fees    *feePermDTO  `json:"fees"`
	Other   []string     `json:"other"` // other authorized message types
}

type grantsDTO struct {
	Address  string          `json:"address"`
	Active   bool            `json:"active"` // false before x/authz and x/feegrant are live
	Given    []permissionDTO `json:"given"`
	Received []permissionDTO `json:"received"`
}

func handleGrants(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("addr")
	if _, err := sdk.AccAddressFromBech32(addr); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid 'addr': %w", err))
		return
	}
	client, err := wallet.NewClient(grpcEndpoint)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer client.Close()
	given, received, err := client.Permissions(addr)
	if errors.Is(err, wallet.ErrAuthzNotActive) || errors.Is(err, wallet.ErrFeegrantNotActive) {
		writeJSON(w, http.StatusOK, grantsDTO{Address: addr, Given: []permissionDTO{}, Received: []permissionDTO{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("failed to load grants: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, grantsDTO{Address: addr, Active: true, Given: toPermissionDTOs(given), Received: toPermissionDTOs(received)})
}

func toPermissionDTOs(in []wallet.Permission) []permissionDTO {
	out := make([]permissionDTO, 0, len(in))
	for _, p := range in {
		d := permissionDTO{Account: p.Account, Other: []string{}}
		if p.Send != nil {
			d.Send = &sendPermDTO{Unlimited: p.Send.Unlimited, AllowList: nonNil(p.Send.AllowList), Expiration: rfc3339(p.Send.Expiration)}
			if !p.Send.Unlimited {
				d.Send.SpendLimit = p.Send.SpendLimit.AmountOf("uaeth").String()
			}
		}
		if p.Fees != nil {
			d.Fees = &feePermDTO{Kind: p.Fees.Kind, Expiration: rfc3339(p.Fees.Expiration)}
			if !p.Fees.SpendLimit.Empty() {
				d.Fees.SpendLimit = p.Fees.SpendLimit.AmountOf("uaeth").String()
			}
		}
		for _, g := range p.Other {
			d.Other = append(d.Other, g.Kind)
		}
		out = append(out, d)
	}
	return out
}

func rfc3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
