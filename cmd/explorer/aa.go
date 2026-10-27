// cmd/explorer/aa.go
//
// GET /api/aa: x/accountauth, this chain's account abstraction.
// An account registers a session key or a guardian threshold; there is
// no list-all query, so accounts are discovered from the indexed
// register, revoke and exec messages and each one's authenticators are
// then read live. ?addr= reads one account even if it has never
// registered anything.
package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	"github.com/whoyoujoshin/aether/x/accountauth"
)

const aaSummaryText = "x/accountauth is this chain's account abstraction. An account registers an alternate ML-DSA-44 authenticator with its own primary key: a session key (it expires, it may only run the message types it was given, and it has a lifetime uaeth spend cap) or a guardian threshold (M of N keys). MsgExecAuthenticated then acts for that account. There is no smart-contract wallet and no change to how transactions are signed."

var aaMessageURLs = []string{
	"/aether.accountauth.v1.MsgRegisterAuthenticator",
	"/aether.accountauth.v1.MsgRevokeAuthenticator",
	"/aether.accountauth.v1.MsgExecAuthenticated",
}

type aaAuthDTO struct {
	ID              uint64   `json:"id"`
	Kind            string   `json:"kind"` // session_key, guardian_threshold, unknown
	Expired         bool     `json:"expired,omitempty"`
	ExpiresAt       string   `json:"expiresAt,omitempty"`
	AllowedMsgTypes []string `json:"allowedMsgTypes,omitempty"`
	SpendLimitUaeth string   `json:"spendLimitUaeth,omitempty"`
	SpentUaeth      string   `json:"spentUaeth,omitempty"`
	Pubkey          string   `json:"pubkey,omitempty"` // first bytes, hex; the key itself is 1,312 bytes
	PubkeyBytes     int      `json:"pubkeyBytes,omitempty"`
	Guardians       int      `json:"guardians,omitempty"`
	Threshold       uint32   `json:"threshold,omitempty"`
	NextSequence    uint64   `json:"nextSequence,omitempty"`
	GuardianKeys    []string `json:"guardianKeys,omitempty"`
}

type aaAccountDTO struct {
	Address        string      `json:"address"`
	Authenticators []aaAuthDTO `json:"authenticators"`
	LastHeight     int64       `json:"lastHeight,omitempty"`
	LastAction     string      `json:"lastAction,omitempty"`
}

type aaTxDTO struct {
	Hash    string `json:"hash"`
	Height  int64  `json:"height"`
	Time    string `json:"time,omitempty"`
	Action  string `json:"action"`
	Account string `json:"account"`
	Signer  string `json:"signer,omitempty"`
	Code    uint32 `json:"code"`
	Detail  string `json:"detail,omitempty"`
}

type aaPageDTO struct {
	Module       string         `json:"module"`
	Supported    bool           `json:"supported"`
	Summary      string         `json:"summary"`
	Messages     []string       `json:"messages"`
	Accounts     []aaAccountDTO `json:"accounts"`
	Transactions []aaTxDTO      `json:"transactions"`
	Note         string         `json:"note,omitempty"`
}

func handleAA(w http.ResponseWriter, r *http.Request) {
	addr := strings.TrimSpace(r.URL.Query().Get("addr"))
	if addr != "" {
		if _, err := sdk.AccAddressFromBech32(addr); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid address"))
			return
		}
	}
	page, err := loadAA(r.Context(), addr)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

const aaCacheTTL = 15 * time.Second

var (
	aaMu       sync.Mutex
	aaCacheKey string
	aaCacheAt  time.Time
	aaCache    aaPageDTO
)

func loadAA(ctx context.Context, addr string) (aaPageDTO, error) {
	aaMu.Lock()
	if aaCacheKey == addr && !aaCacheAt.IsZero() && time.Since(aaCacheAt) < aaCacheTTL {
		page := aaCache
		aaMu.Unlock()
		return page, nil
	}
	aaMu.Unlock()
	page, err := readAA(ctx, addr)
	if err != nil || ctx.Err() != nil {
		return page, err
	}
	aaMu.Lock()
	aaCacheKey, aaCacheAt, aaCache = addr, time.Now(), page
	aaMu.Unlock()
	return page, nil
}

func readAA(ctx context.Context, addr string) (aaPageDTO, error) {
	page := aaPageDTO{
		Module:       "accountauth",
		Supported:    true,
		Summary:      aaSummaryText,
		Messages:     append([]string(nil), aaMessageURLs...),
		Accounts:     []aaAccountDTO{},
		Transactions: []aaTxDTO{},
	}
	conn, err := newGRPC()
	if err != nil {
		return page, err
	}
	defer conn.Close()
	q := accountauth.NewQueryClient(conn)

	known := map[string][]aaAuthDTO{}
	now := time.Now().UTC()
	if addr != "" {
		resp, err := q.Authenticators(ctx, &accountauth.QueryAuthenticatorsRequest{Account: addr})
		if grpcUnimplemented(err) {
			return unsupportedAA(), nil
		}
		if err != nil {
			return page, fmt.Errorf("reading authenticators: %w", err)
		}
		known[addr] = authDTOs(resp.GetAuthenticators(), now)
	} else {
		probe := sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)).String()
		if _, err := q.Authenticators(ctx, &accountauth.QueryAuthenticatorsRequest{Account: probe}); grpcUnimplemented(err) {
			return unsupportedAA(), nil
		} else if err != nil {
			return page, fmt.Errorf("reading authenticators: %w", err)
		}
	}

	rpc, err := newRPC()
	if err != nil {
		page.Note = "Transactions couldn't be read: " + clipErr(err, 160)
		page.Accounts = accountsFrom(known, nil)
		return page, nil
	}
	var txs []aaTxDTO
	var notes []string
	truncated := false
	for _, typeURL := range aaMessageURLs {
		found, err := recentActionTxs(ctx, rpc, typeURL, 40)
		if err != nil {
			notes = append(notes, clipErr(err, 120))
			continue
		}
		if len(found) == 40 {
			truncated = true
		}
		for _, tx := range found {
			rows := aaTxsFrom(tx)
			for _, row := range rows {
				if addr != "" && row.Account != addr && row.Signer != addr {
					continue
				}
				txs = append(txs, row)
				if tx.code == 0 && row.Account != "" {
					if _, ok := known[row.Account]; !ok {
						known[row.Account] = nil
					}
				}
			}
		}
	}
	for account, auths := range known {
		if auths != nil || account == addr {
			continue
		}
		resp, err := q.Authenticators(ctx, &accountauth.QueryAuthenticatorsRequest{Account: account})
		if err != nil {
			known[account] = []aaAuthDTO{}
			continue
		}
		known[account] = authDTOs(resp.GetAuthenticators(), now)
	}
	sort.Slice(txs, func(i, j int) bool {
		if txs[i].Height != txs[j].Height {
			return txs[i].Height > txs[j].Height
		}
		return txs[i].Hash < txs[j].Hash
	})
	if addr != "" {
		auths := known[addr]
		if auths == nil {
			auths = []aaAuthDTO{}
		}
		var last aaTxDTO
		for _, tx := range txs {
			if tx.Account == addr && tx.Height >= last.Height {
				last = tx
			}
		}
		page.Accounts = []aaAccountDTO{{
			Address: addr, Authenticators: auths, LastHeight: last.Height, LastAction: last.Action,
		}}
	} else {
		page.Accounts = accountsFrom(known, txs)
		if len(page.Accounts) > 50 {
			page.Accounts = page.Accounts[:50]
			notes = append(notes, "Showing 50 accounts.")
		}
	}
	if txs == nil {
		txs = []aaTxDTO{}
	}
	page.Transactions = txs
	if truncated {
		notes = append(notes, "Showing the newest 40 indexed messages of each kind.")
	}
	if len(notes) > 0 {
		page.Note = "Some account-abstraction history couldn't be read: " + strings.Join(notes, " ")
	}
	return page, nil
}

func unsupportedAA() aaPageDTO {
	return aaPageDTO{
		Module:       "accountauth",
		Supported:    false,
		Summary:      aaSummaryText,
		Messages:     append([]string(nil), aaMessageURLs...),
		Accounts:     []aaAccountDTO{},
		Transactions: []aaTxDTO{},
		Note:         "This node has no x/accountauth query service, so there are no accounts to list. The explorer did not invent an account-abstraction module.",
	}
}

func authDTOs(list []*accountauth.Authenticator, now time.Time) []aaAuthDTO {
	out := make([]aaAuthDTO, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		out = append(out, authenticatorToDTO(*a, now))
	}
	return out
}

func authenticatorToDTO(a accountauth.Authenticator, now time.Time) aaAuthDTO {
	dto := aaAuthDTO{ID: a.Id, Kind: "unknown"}
	switch k := a.Kind.(type) {
	case *accountauth.Authenticator_SessionKey:
		dto.Kind = "session_key"
		sk := k.SessionKey
		if sk == nil {
			return dto
		}
		dto.Pubkey = shortKey(sk.Pubkey)
		dto.PubkeyBytes = len(sk.Pubkey)
		if len(sk.AllowedMsgTypes) > 0 {
			dto.AllowedMsgTypes = append([]string(nil), sk.AllowedMsgTypes...)
		}
		dto.SpendLimitUaeth = sk.SpendLimitUaeth
		dto.SpentUaeth = sk.SpentUaeth
		if sk.ExpiresAtUnix > 0 {
			exp := time.Unix(sk.ExpiresAtUnix, 0).UTC()
			dto.ExpiresAt = exp.Format(time.RFC3339)
			dto.Expired = !now.Before(exp)
		}
	case *accountauth.Authenticator_GuardianThreshold:
		dto.Kind = "guardian_threshold"
		gt := k.GuardianThreshold
		if gt == nil {
			return dto
		}
		dto.Guardians = len(gt.GuardianPubkeys)
		dto.Threshold = gt.Threshold
		dto.NextSequence = gt.NextSequence
		for _, pk := range gt.GuardianPubkeys {
			dto.GuardianKeys = append(dto.GuardianKeys, shortKey(pk))
		}
	}
	return dto
}

func shortKey(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n := 8
	if len(b) < n {
		n = len(b)
	}
	s := hexEncode(b[:n])
	if len(b) > n {
		s += "…"
	}
	return s
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}

func accountsFrom(auth map[string][]aaAuthDTO, txs []aaTxDTO) []aaAccountDTO {
	last := map[string]aaTxDTO{}
	for _, tx := range txs {
		if tx.Account == "" {
			continue
		}
		prev, ok := last[tx.Account]
		if !ok || tx.Height > prev.Height {
			last[tx.Account] = tx
		}
	}
	addrs := make([]string, 0, len(auth))
	for a := range auth {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool {
		hi, hj := last[addrs[i]].Height, last[addrs[j]].Height
		if hi != hj {
			return hi > hj
		}
		return addrs[i] < addrs[j]
	})
	out := make([]aaAccountDTO, 0, len(addrs))
	for _, a := range addrs {
		auths := auth[a]
		if auths == nil {
			auths = []aaAuthDTO{}
		}
		out = append(out, aaAccountDTO{
			Address: a, Authenticators: auths, LastHeight: last[a].Height, LastAction: last[a].Action,
		})
	}
	return out
}

func aaTxsFrom(tx searchedTx) []aaTxDTO {
	d := decodeTx(tx.raw)
	if !d.ok {
		return nil
	}
	var out []aaTxDTO
	for _, m := range d.body.Messages {
		row := aaTxDTO{
			Hash: tx.hash, Height: tx.height, Time: formatTime(tx.when), Code: tx.code,
			Action: msgTypeName(m.TypeUrl),
		}
		switch m.TypeUrl {
		case aaMessageURLs[0]:
			var msg accountauth.MsgRegisterAuthenticator
			if proto.Unmarshal(m.Value, &msg) != nil {
				continue
			}
			row.Account, row.Signer = msg.Account, msg.Account
			row.Detail = authKindPhrase(msg.Authenticator)
		case aaMessageURLs[1]:
			var msg accountauth.MsgRevokeAuthenticator
			if proto.Unmarshal(m.Value, &msg) != nil {
				continue
			}
			row.Account, row.Signer = msg.Account, msg.Account
			row.Detail = fmt.Sprintf("authenticator %d", msg.Id)
		case aaMessageURLs[2]:
			var msg accountauth.MsgExecAuthenticated
			if proto.Unmarshal(m.Value, &msg) != nil {
				continue
			}
			row.Account, row.Signer = msg.Account, msg.Signer
			row.Detail = execDetail(&msg)
		default:
			continue
		}
		out = append(out, row)
	}
	return out
}

func authKindPhrase(a *accountauth.Authenticator) string {
	if a == nil {
		return "authenticator"
	}
	switch k := a.Kind.(type) {
	case *accountauth.Authenticator_SessionKey:
		return "session key"
	case *accountauth.Authenticator_GuardianThreshold:
		if k.GuardianThreshold == nil {
			return "guardian threshold"
		}
		return fmt.Sprintf("guardian threshold %d of %d", k.GuardianThreshold.Threshold, len(k.GuardianThreshold.GuardianPubkeys))
	default:
		return "authenticator"
	}
}

func execDetail(msg *accountauth.MsgExecAuthenticated) string {
	if msg == nil {
		return ""
	}
	var names []string
	for _, m := range msg.Msgs {
		if m == nil || m.TypeUrl == "" {
			continue
		}
		names = append(names, msgTypeName(m.TypeUrl))
	}
	if len(names) == 0 {
		return fmt.Sprintf("authenticator %d", msg.AuthenticatorId)
	}
	return fmt.Sprintf("authenticator %d: %s", msg.AuthenticatorId, strings.Join(names, ", "))
}
