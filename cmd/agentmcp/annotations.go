package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Tool annotations: a human-readable title and what each tool may do, so
// a client (Claude among them) can tell a lookup from a payment and ask
// before the ones that move money. Anthropic's connector directory
// requires a title and readOnlyHint or destructiveHint on every tool.
//
// Every tool both servers register must be in toolLabels:
// TestToolAnnotations fails on one that isn't, and annotate panics.

type toolEffect int

const (
	// readOnly: looks things up; changes nothing on chain or locally.
	readOnly toolEffect = iota
	// additive: changes something, but nothing the agent can't afford to
	// lose or can't take back: an invoice record, a faucet request, a
	// 1 uaeth rating or listing, a prepaid balance paid back to itself.
	additive
	// destructive: money leaves the agent's control for good once it
	// goes through (a payment, an escrow lock, release or refund).
	destructive
)

type toolLabel struct {
	title  string
	effect toolEffect
	// idempotent: repeating the call with the same arguments has no
	// further effect (an idempotencyKey, or a latest-wins record).
	idempotent bool
}

var toolLabels = map[string]toolLabel{
	"get_agent_address":          {title: "Get agent address", effect: readOnly},
	"get_balance":                {title: "Get balance", effect: readOnly},
	"get_spending_status":        {title: "Get spending status", effect: readOnly},
	"get_account_authenticators": {title: "Get account authenticators", effect: readOnly},
	"get_transaction_status":     {title: "Get transaction status", effect: readOnly},
	"wait_for_transaction":       {title: "Wait for transaction", effect: readOnly},
	"get_transaction_history":    {title: "Get transaction history", effect: readOnly},
	"wait_for_payment":           {title: "Wait for payment", effect: readOnly},
	"list_purchases":             {title: "List purchases", effect: readOnly},
	"list_prepaid_balances":      {title: "List prepaid balances", effect: readOnly},
	"find_services":              {title: "Find paid services", effect: readOnly},
	"get_miner_status":           {title: "Get miner status", effect: readOnly},
	"get_escrow":                 {title: "Get escrow", effect: readOnly},
	"list_escrows":               {title: "List escrows", effect: readOnly},
	"get_test_wallet":            {title: "Get test wallet", effect: readOnly},

	"request_testnet_funds": {title: "Request testnet funds", effect: additive},
	"create_invoice":        {title: "Create invoice", effect: additive},
	"rate_service":          {title: "Rate service", effect: additive, idempotent: true},
	"announce_service":      {title: "Announce service", effect: additive, idempotent: true},
	"withdraw_prepaid":      {title: "Withdraw prepaid balance", effect: additive, idempotent: true},
	"create_test_wallet":    {title: "Create test wallet", effect: additive},

	"send_aeth":      {title: "Send AETH", effect: destructive, idempotent: true},
	"fetch_paid":     {title: "Fetch paid API", effect: destructive, idempotent: true},
	"create_escrow":  {title: "Create escrow", effect: destructive},
	"release_escrow": {title: "Release escrow", effect: destructive},
	"refund_escrow":  {title: "Refund escrow", effect: destructive},

	"send_from_test_wallet": {title: "Send from test wallet", effect: destructive},
	"buy_service":           {title: "Buy service", effect: destructive},
}

// annotate sets t's title and annotations from toolLabels.
func annotate(t *mcp.Tool) *mcp.Tool {
	l, ok := toolLabels[t.Name]
	if !ok {
		panic("agentmcp: no toolLabels entry for tool " + t.Name)
	}
	isDestructive := l.effect == destructive
	t.Title = l.title
	t.Annotations = &mcp.ToolAnnotations{
		Title:           l.title,
		ReadOnlyHint:    l.effect == readOnly,
		DestructiveHint: &isDestructive,
		IdempotentHint:  l.idempotent || l.effect == readOnly,
	}
	return t
}
