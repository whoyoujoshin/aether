package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
)

// Spending mandates (docs/MANDATES.md): the owner signs, with their own
// key, what this agent may pay (agentmcp mandate); agentmcp, given the
// file (--mandate-file), sends it with every paid request so a seller can
// see who answers for the agent and check it is within what the owner
// allowed. The mandate restates the owner's limits for sellers; the
// limits that bind the agent are still this server's own.

var (
	mandateFile string
	mandate     *paywall.Mandate // nil: none
	mandateHdr  string

	mandateAgentOnce sync.Once
	mandateAgent     string // the account this agent pays from; "" if unknown
)

// loadMandate reads --mandate-file at startup: it must verify and be for
// this chain. An expired one is loaded (and reported) so get_mandate can
// say so; it is never sent.
func loadMandate(path string) error {
	bz, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m paywall.Mandate
	if err := json.Unmarshal(bz, &m); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := m.Verify(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if m.Network != chainID {
		return fmt.Errorf("%s is for %s, not %s", path, m.Network, chainID)
	}
	hdr, err := paywall.EncodeMandate(&m)
	if err != nil {
		return err
	}
	mandate, mandateHdr = &m, hdr
	if time.Now().Unix() >= m.Expires {
		log.Printf("mandate %s expired %s: it won't be sent; ask the owner for a new one", m.ID, time.Unix(m.Expires, 0).UTC().Format(time.RFC3339))
	}
	return nil
}

// payingAccount is the account this agent's payments come from.
func payingAccount() string {
	mandateAgentOnce.Do(func() {
		w, err := newWallet()
		if err != nil {
			return
		}
		if agent, err := getOrCreateAgentAccount(w); err == nil {
			mandateAgent = payer(agent.Address)
		}
	})
	return mandateAgent
}

// mandateHeader is the value to send as paywall.HeaderMandate, or "" when
// there is no mandate, it has lapsed, or it names another agent.
func mandateHeader() string {
	if mandate == nil {
		return ""
	}
	now := time.Now().Unix()
	if now < mandate.NotBefore || now >= mandate.Expires {
		return ""
	}
	if a := payingAccount(); a == "" || a != mandate.Agent {
		return ""
	}
	return mandateHdr
}

// mandateRefusal turns a seller's 403 for a mandate into this server's
// error, so the agent sees a code rather than a page.
func mandateRefusal(r *httpResult) error {
	if r.status != http.StatusForbidden {
		return nil
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(r.body, &body) != nil || !strings.HasPrefix(body.Error, "MANDATE_") {
		return nil
	}
	return newError(codeMandateRefused, fmt.Sprintf("%s: %s (nothing was paid)", body.Error, body.Message))
}

type getMandateOutput struct {
	Present       bool     `json:"present"`
	ID            string   `json:"id,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	Agent         string   `json:"agent,omitempty"`
	MaxPerPayment string   `json:"maxPerPayment,omitempty"`
	MaxTotal      string   `json:"maxTotal,omitempty"`
	PayTo         []string `json:"payTo,omitempty"`
	NotBefore     string   `json:"notBefore,omitempty"`
	Expires       string   `json:"expires,omitempty"`
	Purpose       string   `json:"purpose,omitempty"`
	Sent          bool     `json:"sent" jsonschema:"whether paid requests carry it now"`
	Message       string   `json:"message"`
}

func toolGetMandate(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, getMandateOutput, error) {
	if mandate == nil {
		return nil, getMandateOutput{Message: "no spending mandate: paid requests carry none. The owner can sign one with `agentmcp mandate` and start this server with --mandate-file."}, nil
	}
	m := mandate
	out := getMandateOutput{
		Present: true, ID: m.ID, Owner: m.Owner, Agent: m.Agent, MaxPerPayment: m.MaxPerPayment, MaxTotal: m.MaxTotal, PayTo: m.PayTo,
		NotBefore: time.Unix(m.NotBefore, 0).UTC().Format(time.RFC3339), Expires: time.Unix(m.Expires, 0).UTC().Format(time.RFC3339),
		Purpose: m.Purpose, Sent: mandateHeader() != "",
	}
	now := time.Now().Unix()
	switch {
	case now >= m.Expires:
		out.Message = "expired: it isn't sent; ask the owner for a new one"
	case now < m.NotBefore:
		out.Message = "not valid yet: it is sent from notBefore"
	case payingAccount() != m.Agent:
		out.Message = fmt.Sprintf("it names agent %s, but this agent pays from %s: it isn't sent", m.Agent, payingAccount())
	default:
		out.Message = "sent with every paid request; sellers that check mandates see the owner and these limits"
	}
	return nil, out, nil
}

// runMandateCommand is `agentmcp mandate`: the owner signs a mandate with
// their own key, wherever that key is (not on the agent's machine).
func runMandateCommand(args []string) error {
	fs := flag.NewFlagSet("mandate", flag.ExitOnError)
	ownerKey := fs.String("owner-key", "", "keyring account of the owner, who signs")
	dir := fs.String("keyring-dir", "", "keyring directory holding --owner-key")
	backend := fs.String("keyring-backend", "test", "keyring backend holding --owner-key")
	agent := fs.String("agent", "", "the agent's paying address (get_agent_address; in grant mode, the granter)")
	maxPer := fs.String("max-per-payment", "", `the most one payment may be, with its unit, e.g. "0.01 AETH" or "0.05 USDC"`)
	maxTotal := fs.String("max-total", "", "optional: the most in all, with its unit (stated for sellers; agentmcp's --daily-limit is what binds)")
	payTo := fs.String("pay-to", "", "optional: the only payees allowed (comma-separated addresses)")
	validFor := fs.Duration("valid-for", 30*24*time.Hour, "how long the mandate lasts from now")
	purpose := fs.String("purpose", "", "optional: what the agent is for, shown to sellers (one line)")
	chain := fs.String("chain-id", "aether-testnet-1", "chain ID")
	out := fs.String("out", "mandate.json", "file to write")
	usdc := wallet.USDCFlags(fs, wallet.TestnetUSDC)
	_ = fs.Parse(args)
	if *ownerKey == "" || *dir == "" || *agent == "" || *maxPer == "" {
		return errors.New("--owner-key, --keyring-dir, --agent and --max-per-payment are required")
	}
	if _, err := sdk.AccAddressFromBech32(*agent); err != nil {
		return fmt.Errorf("--agent: %w", err)
	}
	a, err := wallet.NewAssetsFor(*usdc)
	if err != nil {
		return err
	}
	amount := func(name, s string) (string, error) {
		if s == "" {
			return "", nil
		}
		asset, n, err := a.Parse(s)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return paywall.ReceiptAmount(n, asset.Denom), nil
	}
	m := paywall.Mandate{Network: *chain, Agent: *agent, Purpose: *purpose}
	if m.MaxPerPayment, err = amount("--max-per-payment", *maxPer); err != nil {
		return err
	}
	if m.MaxTotal, err = amount("--max-total", *maxTotal); err != nil {
		return err
	}
	for _, p := range strings.Split(*payTo, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if _, err := sdk.AccAddressFromBech32(p); err != nil {
			return fmt.Errorf("--pay-to %q: %w", p, err)
		}
		m.PayTo = append(m.PayTo, p)
	}
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", *backend, *dir, codec.NewProtoCodec(registry))
	if err != nil {
		return fmt.Errorf("opening the keyring: %w", err)
	}
	acc, err := w.GetAccount(*ownerKey)
	if err != nil {
		return fmt.Errorf("--owner-key %q: %w", *ownerKey, err)
	}
	now := time.Now()
	m.Owner, m.NotBefore, m.Expires = acc.Address, now.Unix(), now.Add(*validFor).Unix()
	signed, err := paywall.NewMandate(m, func(msg []byte) ([]byte, []byte, error) { return w.SignBytes(*ownerKey, msg) })
	if err != nil {
		return err
	}
	bz, _ := json.MarshalIndent(signed, "", "  ")
	if err := os.WriteFile(*out, append(bz, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("Signed mandate %s: %s may pay up to %s a payment for %s until %s.\nWrote %s: start agentmcp with --mandate-file %s.\n",
		signed.ID, signed.Agent, *maxPer, signed.Owner, time.Unix(signed.Expires, 0).UTC().Format(time.RFC3339), *out, *out)
	return nil
}
