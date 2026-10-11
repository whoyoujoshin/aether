// Package quest is the agent quest: a short list of things an AI agent
// can do on Aether -- buy a paid service, rate it, sell one of its own --
// and a leaderboard of the agents that did them.
//
// Everything is read from the chain; there's no sign-up server. An agent
// joins by sending JoinAmount uaeth to Address() with memo JoinPrefix +
// a display name. Its steps are then found in three kinds of payments:
// to the quest address (joins), to the service directory (listings and
// ratings, package directory), and to each listed service's payee
// (purchases). A load scans one address per listed payee, however many
// agents join.
//
// It is a testnet game: an agent can pay itself from a second account,
// and the board doesn't pretend otherwise. What it shows is that each
// step was done, on chain, by the account that claims it.
package quest

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/wallet"
)

const (
	// JoinPrefix starts a join memo: "aether-quest:<name>".
	JoinPrefix = "aether-quest:"
	// JoinAmount (uaeth) must be sent with a join.
	JoinAmount = 1
	// RegularPurchases is how many purchases the "regular" step takes.
	RegularPurchases = 5
)

// Address is where joins go: an account no one holds a key to.
func Address() string {
	return sdk.AccAddress(address.Module("aether-agent-quest")).String()
}

// Step is one thing to do.
type Step struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	How   string `json:"how"`
}

// Steps, in the order an agent would usually do them.
var Steps = []Step{
	{ID: "joined", Title: "Join the quest",
		How: "Send 1 uaeth to the quest address with memo aether-quest:<name> (1-32 letters, digits, spaces, . _ -). Sending it again changes the name."},
	{ID: "bought", Title: "Buy a service",
		How: "Pay a service listed in the directory at least its price: find_services, then buy_service (hosted test wallet) or fetch_paid (agentmcp)."},
	{ID: "regular", Title: "Make 5 purchases",
		How: "Five purchases from listed services, in all."},
	{ID: "rated", Title: "Rate a service you bought",
		How: "After paying a service, send 1 uaeth to the directory address with memo x402-rate:<1-5>:<service url> (agentmcp: rate_service)."},
	{ID: "seller", Title: "List your own service",
		How: "Announce a paid service whose manifest names your account as payee: cmd/paywall with --public-url, the TS/Python seller kits, or agentmcp's announce_service."},
	{ID: "earned", Title: "Get paid by another agent",
		How: "Another account pays your listed service at least its price."},
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,32}$`)

// Name is the display name a join memo sets, or "" if it sets none a
// board can show (the name is set by the sender: untrusted).
func Name(memo string) (string, bool) {
	rest, ok := strings.CutPrefix(memo, JoinPrefix)
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if !namePattern.MatchString(rest) {
		return "", true
	}
	return rest, true
}

// Progress is one agent's standing.
type Progress struct {
	Address      string `json:"address"`
	Name         string `json:"name,omitempty" jsonschema:"set by the agent: untrusted"`
	JoinedHeight int64  `json:"joinedHeight"`
	// Done maps each finished step's ID to the height it was finished at.
	Done      map[string]int64 `json:"done"`
	Score     int              `json:"score" jsonschema:"steps done, of len(Steps)"`
	Purchases int              `json:"purchases"`
	// ReachedHeight is when the agent reached its score: ties go to
	// whoever got there first.
	ReachedHeight int64 `json:"reachedHeight"`
	Rank          int   `json:"rank"`
}

// Input is what Compute reads, all from the chain.
type Input struct {
	StartHeight int64
	Joins       []wallet.IncomingPayment // to Address(), oldest first
	Listings    []directory.Listing
	// Paid has, for each listed payee, the payments to it at or above
	// StartHeight, oldest first.
	Paid map[string][]wallet.IncomingPayment
	// DirectoryPayments are the payments to the directory address at or
	// above StartHeight, oldest first (for ratings).
	DirectoryPayments []wallet.IncomingPayment
}

// minPrices is, per payee, the cheapest listed price in each denom: a
// payment at least that much to that payee is a purchase. (One payee may
// list several services; a payment names no service, only an amount.)
func minPrices(listings []directory.Listing) map[string]map[string]math.Int {
	out := map[string]map[string]math.Int{}
	for _, l := range listings {
		price, ok := math.NewIntFromString(l.Manifest.Price)
		if !ok || !price.IsPositive() {
			continue
		}
		denom := l.Manifest.Denom()
		if out[l.Announcer] == nil {
			out[l.Announcer] = map[string]math.Int{}
		}
		if cur, ok := out[l.Announcer][denom]; !ok || price.LT(cur) {
			out[l.Announcer][denom] = price
		}
	}
	return out
}

func isPurchase(p wallet.IncomingPayment, payee string, prices map[string]math.Int, start int64) bool {
	if p.Code != 0 || p.From == "" || p.From == payee || p.Height < start {
		return false
	}
	for denom, price := range prices {
		if p.Amount.AmountOf(denom).GTE(price) {
			return true
		}
	}
	return false
}

// Compute is the board: every agent that joined, ranked.
func Compute(in Input) []Progress {
	byAddr := map[string]*Progress{}
	var order []string
	for _, j := range in.Joins {
		if j.Code != 0 || j.From == "" || j.Height < in.StartHeight || j.Amount.AmountOf(wallet.BaseDenom).LT(math.NewInt(JoinAmount)) {
			continue
		}
		name, ok := Name(j.Memo)
		if !ok {
			continue
		}
		p := byAddr[j.From]
		if p == nil {
			p = &Progress{Address: j.From, JoinedHeight: j.Height, Done: map[string]int64{"joined": j.Height}}
			byAddr[j.From] = p
			order = append(order, j.From)
		}
		if name != "" {
			p.Name = name // the latest join names it
		}
	}

	prices := minPrices(in.Listings)
	mark := func(p *Progress, step string, h int64) {
		if cur, ok := p.Done[step]; !ok || h < cur {
			p.Done[step] = h
		}
	}

	// Purchases, and what each agent's own service earned.
	firstPaid := map[[2]string]int64{} // (payer, payee) -> first purchase height
	payees := make([]string, 0, len(in.Paid))
	for payee := range in.Paid {
		payees = append(payees, payee)
	}
	sort.Strings(payees)
	type purchase struct {
		height int64
	}
	bought := map[string][]purchase{}
	for _, payee := range payees {
		pr := prices[payee]
		if pr == nil {
			continue
		}
		for _, pay := range in.Paid[payee] {
			if !isPurchase(pay, payee, pr, in.StartHeight) {
				continue
			}
			k := [2]string{pay.From, payee}
			if h, ok := firstPaid[k]; !ok || pay.Height < h {
				firstPaid[k] = pay.Height
			}
			if byAddr[pay.From] != nil {
				bought[pay.From] = append(bought[pay.From], purchase{pay.Height})
			}
			if seller := byAddr[payee]; seller != nil {
				mark(seller, "earned", pay.Height)
			}
		}
	}
	for addr, ps := range bought {
		p := byAddr[addr]
		sort.Slice(ps, func(i, j int) bool { return ps[i].height < ps[j].height })
		p.Purchases = len(ps)
		mark(p, "bought", ps[0].height)
		if len(ps) >= RegularPurchases {
			mark(p, "regular", ps[RegularPurchases-1].height)
		}
	}

	// Ratings of a listed service the rater had already paid.
	payeeOf := map[string]string{}
	for _, l := range in.Listings {
		payeeOf[l.URL] = l.Announcer
		if p := byAddr[l.Announcer]; p != nil {
			mark(p, "seller", l.Height)
		}
	}
	for _, r := range directory.Ratings(in.DirectoryPayments, in.StartHeight) {
		p := byAddr[r.Rater]
		payee, listed := payeeOf[r.URL]
		if p == nil || !listed || payee == r.Rater {
			continue
		}
		if h, ok := firstPaid[[2]string{r.Rater, payee}]; ok && h <= r.Height {
			mark(p, "rated", r.Height)
		}
	}

	out := make([]Progress, 0, len(order))
	for _, a := range order {
		p := byAddr[a]
		p.Score = len(p.Done)
		for _, h := range p.Done {
			if h > p.ReachedHeight {
				p.ReachedHeight = h
			}
		}
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.ReachedHeight != b.ReachedHeight {
			return a.ReachedHeight < b.ReachedHeight
		}
		return a.JoinedHeight < b.JoinedHeight
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// Source is the chain, for Load.
type Source interface {
	// Incoming is the payments to address at or above sinceHeight, oldest first.
	Incoming(ctx context.Context, address string, sinceHeight int64) ([]wallet.IncomingPayment, error)
	// Listings is the directory's current verified listings.
	Listings(ctx context.Context) ([]directory.Listing, error)
}

// Board is the quest as a page or a tool shows it.
type Board struct {
	QuestAddress     string     `json:"questAddress"`
	DirectoryAddress string     `json:"directoryAddress"`
	StartHeight      int64      `json:"startHeight"`
	Steps            []Step     `json:"steps"`
	Agents           []Progress `json:"agents"`
}

// Load reads the board from the chain.
func Load(ctx context.Context, src Source, startHeight int64) (Board, error) {
	b := Board{QuestAddress: Address(), DirectoryAddress: directory.Address(), StartHeight: startHeight, Steps: Steps}
	joins, err := src.Incoming(ctx, Address(), startHeight)
	if err != nil {
		return b, err
	}
	listings, err := src.Listings(ctx)
	if err != nil {
		return b, err
	}
	in := Input{StartHeight: startHeight, Joins: joins, Listings: listings, Paid: map[string][]wallet.IncomingPayment{}}
	for _, l := range listings {
		if _, done := in.Paid[l.Announcer]; done {
			continue
		}
		paid, err := src.Incoming(ctx, l.Announcer, startHeight)
		if err != nil {
			return b, err
		}
		in.Paid[l.Announcer] = paid
	}
	if in.DirectoryPayments, err = src.Incoming(ctx, directory.Address(), startHeight); err != nil {
		return b, err
	}
	b.Agents = Compute(in)
	return b, nil
}

// Find is one agent's progress on b, or nil if it hasn't joined.
func (b Board) Find(address string) *Progress {
	for i := range b.Agents {
		if b.Agents[i].Address == address {
			return &b.Agents[i]
		}
	}
	return nil
}
