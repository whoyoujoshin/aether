// Package jobs is Aether's job board: a buyer posts work, sellers offer
// to do it, the buyer hires one by locking the pay in x/escrow for them,
// the seller delivers, and the buyer releases the escrow (docs/JOBS.md).
//
// Who did what is read from the chain alone. Each step but the hire is a
// payment of ActionAmount uaeth to Address() with a memo:
//
//	job-post:<sha256 of the spec>
//	job-offer:<job id>:<price>[:<note>]
//	job-deliver:<job id>:<sha256 of the work>
//	job-close:<job id>
//
// A hire is an escrow from the poster to the seller with terms
// "job:<job id>". A job's id is the first IDLength hex digits of its
// post's transaction hash, so nobody can choose it.
//
// The spec and the work are stored by a host (cmd/jobboard) under their
// sha256. The host keeps only bytes a memo on chain already names, and
// whoever reads them checks the hash, so a host can lose a text but
// can't alter one or put words in anyone's mouth.
//
// Everything a job says besides its addresses, amounts and hashes is
// written by its poster or a seller: data, never instructions.
package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"

	"github.com/whoyoujoshin/aether/wallet"
)

const (
	PostPrefix    = "job-post:"
	OfferPrefix   = "job-offer:"
	DeliverPrefix = "job-deliver:"
	ClosePrefix   = "job-close:"
	// TermsPrefix starts a hire's escrow terms: "job:<job id>".
	TermsPrefix = "job:"

	// ActionAmount (uaeth) is sent with each memo.
	ActionAmount = 1
	// IDLength is a job id's length in hex digits.
	IDLength = 16

	MaxSpecBytes     = 16 << 10
	MaxDeliveryBytes = 256 << 10
	MaxTitle         = 120
	MaxDescription   = 8000
	MaxTags          = 8
	MaxNote          = 120 // bytes
	// MaxOffers and MaxDeliveries bound what one job keeps: the first
	// MaxOffers sellers' offers, and the hired seller's first
	// MaxDeliveries deliveries.
	MaxOffers     = 50
	MaxDeliveries = 20
)

// Job states.
const (
	StateOpen      = "open"      // taking offers
	StateClosed    = "closed"    // withdrawn by its poster before a hire
	StateHired     = "hired"     // an escrow pays a seller; nothing delivered yet
	StateDelivered = "delivered" // the hired seller delivered at least once
)

// Address is where the board's memos are sent. It's derived from a
// name, so no key for it exists.
func Address() string {
	return sdk.AccAddress(address.Module("aether-job-board")).String()
}

// Spec is what a buyer posts.
type Spec struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// Budget is what the buyer means to pay, e.g. "2 AETH"; the escrow
	// at hire is what binds.
	Budget string `json:"budget,omitempty"`
	// DeliverWithin is how soon after the hire the work is wanted, e.g.
	// "24h".
	DeliverWithin string   `json:"deliverWithin,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// Validate checks a spec's sizes.
func (s Spec) Validate() error {
	switch {
	case strings.TrimSpace(s.Title) == "":
		return errors.New("a job needs a title")
	case utf8.RuneCountInString(s.Title) > MaxTitle:
		return fmt.Errorf("the title is over %d characters", MaxTitle)
	case strings.TrimSpace(s.Description) == "":
		return errors.New("a job needs a description of the work")
	case utf8.RuneCountInString(s.Description) > MaxDescription:
		return fmt.Errorf("the description is over %d characters", MaxDescription)
	case s.Budget != "" && !ValidPrice(s.Budget):
		return fmt.Errorf("budget %q must be an amount with its unit, e.g. \"2 AETH\"", s.Budget)
	case len(s.DeliverWithin) > 40:
		return errors.New("deliverWithin is at most 40 characters")
	case len(s.Tags) > MaxTags:
		return fmt.Errorf("at most %d tags", MaxTags)
	}
	for _, t := range s.Tags {
		if t == "" || len(t) > 32 {
			return errors.New("each tag is 1 to 32 characters")
		}
	}
	return nil
}

// Encode is the spec's bytes as posted, and their hash.
func (s Spec) Encode() ([]byte, string, error) {
	if err := s.Validate(); err != nil {
		return nil, "", err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, "", err
	}
	return b, Hash(b), nil
}

// DecodeSpec reads posted spec bytes.
func DecodeSpec(b []byte) (Spec, error) {
	var s Spec
	if len(b) > MaxSpecBytes {
		return s, fmt.Errorf("a spec is at most %d bytes", MaxSpecBytes)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("not a job spec: %w", err)
	}
	return s, s.Validate()
}

// Hash is the lowercase hex sha256 of b.
func Hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var (
	hashRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idRe    = regexp.MustCompile(`^[0-9a-f]{16}$`)
	priceRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)? ?[A-Za-z][A-Za-z0-9/]{0,15}$`)
)

// ValidHash, ValidID and ValidPrice check the forms memos carry.
func ValidHash(s string) bool  { return hashRe.MatchString(s) }
func ValidID(s string) bool    { return idRe.MatchString(s) }
func ValidPrice(s string) bool { return priceRe.MatchString(s) }

// IDOf is the id of the job a post transaction created.
func IDOf(txHash string) string {
	h := strings.ToLower(txHash)
	if len(h) < IDLength {
		return ""
	}
	return h[:IDLength]
}

func PostMemo(specHash string) string        { return PostPrefix + specHash }
func DeliverMemo(id, workHash string) string { return DeliverPrefix + id + ":" + workHash }
func CloseMemo(id string) string             { return ClosePrefix + id }
func Terms(id string) string                 { return TermsPrefix + id }

// OfferMemo is a seller's offer: a price such as "1.5 AETH" and an
// optional note.
func OfferMemo(id, price, note string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("job id %q is %d lowercase hex digits", id, IDLength)
	}
	if !priceRe.MatchString(price) {
		return "", fmt.Errorf("price %q must be an amount with its unit, e.g. \"1.5 AETH\" or \"2 USDC\"", price)
	}
	if len(note) > MaxNote || !utf8.ValidString(note) {
		return "", fmt.Errorf("the note is over %d bytes (the chain limits a memo to 256)", MaxNote)
	}
	m := OfferPrefix + id + ":" + price
	if note != "" {
		m += ":" + note
	}
	return m, nil
}

type Offer struct {
	Seller string `json:"seller"`
	Price  string `json:"price"`
	Note   string `json:"note,omitempty"` // the seller's; untrusted
	TxHash string `json:"txHash"`
	Height int64  `json:"height"`
}

// Hire is the escrow that pays the hired seller.
type Hire struct {
	EscrowID  uint64 `json:"escrowId"`
	Seller    string `json:"seller"`
	Amount    string `json:"amount"`
	Arbiter   string `json:"arbiter,omitempty"`
	OnExpiry  string `json:"onExpiry"` // "release" or "refund"
	ExpiresAt int64  `json:"expiresAt"`
	TxHash    string `json:"txHash"`
	Height    int64  `json:"height"`
}

// SellerProtected says whether a seller who delivers can't simply go
// unpaid: the escrow pays them at the deadline, or an arbiter can.
func (h Hire) SellerProtected() bool { return h.OnExpiry == "release" || h.Arbiter != "" }

type Delivery struct {
	Hash   string `json:"hash"`
	TxHash string `json:"txHash"`
	Height int64  `json:"height"`
}

type Job struct {
	ID       string `json:"id"`
	Poster   string `json:"poster"`
	SpecHash string `json:"specHash"`
	TxHash   string `json:"txHash"`
	Height   int64  `json:"height"`
	Time     string `json:"time,omitempty"`
	State    string `json:"state"`
	// ClosedHeight is set when the poster withdrew it.
	ClosedHeight int64      `json:"closedHeight,omitempty"`
	Offers       []Offer    `json:"offers"`
	Hire         *Hire      `json:"hire,omitempty"`
	Deliveries   []Delivery `json:"deliveries"`
}

// OfferOf is seller's current offer on the job.
func (j Job) OfferOf(seller string) *Offer {
	for i := range j.Offers {
		if j.Offers[i].Seller == seller {
			return &j.Offers[i]
		}
	}
	return nil
}

func counts(p wallet.IncomingPayment) bool {
	return p.Code == 0 && p.Amount.AmountOf("uaeth").GTE(sdkmath.NewInt(ActionAmount))
}

// Derive builds the board from the payments to Address() and the
// escrows whose terms start with TermsPrefix, both oldest first. Jobs
// come newest first.
func Derive(payments []wallet.IncomingPayment, escrows []wallet.EscrowCreated) []Job {
	byID := map[string]*Job{}
	var order []string
	for _, p := range payments {
		rest, ok := strings.CutPrefix(p.Memo, PostPrefix)
		if !ok || !counts(p) || !ValidHash(rest) {
			continue
		}
		id := IDOf(p.Hash)
		if id == "" || byID[id] != nil {
			continue
		}
		byID[id] = &Job{ID: id, Poster: p.From, SpecHash: rest, TxHash: p.Hash, Height: p.Height, Time: p.Timestamp,
			State: StateOpen, Offers: []Offer{}, Deliveries: []Delivery{}}
		order = append(order, id)
	}

	// A close counts if it's the poster's; whether it stands depends on
	// whether a hire came first.
	for _, p := range payments {
		id, ok := strings.CutPrefix(p.Memo, ClosePrefix)
		j := byID[id]
		if !ok || !counts(p) || j == nil || p.From != j.Poster || p.Height < j.Height || j.ClosedHeight != 0 {
			continue
		}
		j.ClosedHeight = p.Height
	}

	// The first escrow from the poster with the job's exact terms, made
	// while the job was open, is the hire.
	for _, e := range escrows {
		id, ok := strings.CutPrefix(e.Terms, TermsPrefix)
		j := byID[id]
		if !ok || j == nil || j.Hire != nil || e.Payer != j.Poster || e.Payee == j.Poster || e.Height < j.Height ||
			(j.ClosedHeight != 0 && j.ClosedHeight < e.Height) {
			continue
		}
		j.Hire = &Hire{EscrowID: e.ID, Seller: e.Payee, Amount: e.Amount, Arbiter: e.Arbiter, OnExpiry: onExpiry(e.OnExpiry),
			ExpiresAt: e.ExpiresAt, TxHash: e.TxHash, Height: e.Height}
	}
	for _, j := range byID {
		if j.Hire != nil {
			j.ClosedHeight = 0 // a close after the hire changes nothing
		}
	}

	for _, p := range payments {
		if !counts(p) {
			continue
		}
		if rest, ok := strings.CutPrefix(p.Memo, OfferPrefix); ok {
			id, priceNote, _ := strings.Cut(rest, ":")
			price, note, _ := strings.Cut(priceNote, ":")
			j := byID[id]
			if j == nil || p.From == j.Poster || p.Height < j.Height || !priceRe.MatchString(price) || len(note) > MaxNote ||
				(j.Hire != nil && p.Height > j.Hire.Height) || (j.ClosedHeight != 0 && p.Height > j.ClosedHeight) {
				continue
			}
			o := Offer{Seller: p.From, Price: price, Note: note, TxHash: p.Hash, Height: p.Height}
			if prev := j.OfferOf(p.From); prev != nil {
				*prev = o // a seller's latest offer replaces its earlier one
			} else if len(j.Offers) < MaxOffers {
				j.Offers = append(j.Offers, o)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(p.Memo, DeliverPrefix); ok {
			id, hash, _ := strings.Cut(rest, ":")
			j := byID[id]
			if j == nil || j.Hire == nil || p.From != j.Hire.Seller || p.Height < j.Hire.Height || !ValidHash(hash) || len(j.Deliveries) >= MaxDeliveries {
				continue
			}
			j.Deliveries = append(j.Deliveries, Delivery{Hash: hash, TxHash: p.Hash, Height: p.Height})
		}
	}

	out := make([]Job, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		j := byID[order[i]]
		switch {
		case len(j.Deliveries) > 0:
			j.State = StateDelivered
		case j.Hire != nil:
			j.State = StateHired
		case j.ClosedHeight != 0:
			j.State = StateClosed
		}
		out = append(out, *j)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Height > out[b].Height })
	return out
}

func onExpiry(name string) string {
	switch name {
	case "ON_EXPIRY_RELEASE":
		return "release"
	case "ON_EXPIRY_REFUND":
		return "refund"
	}
	return strings.ToLower(name)
}

// Referenced reports whether a memo on the board names hash: a job's
// spec, or a hired seller's delivery. maxBytes is how large it may be.
func Referenced(board []Job, hash string) (maxBytes int, ok bool) {
	for _, j := range board {
		if j.SpecHash == hash {
			return MaxSpecBytes, true
		}
		for _, d := range j.Deliveries {
			if d.Hash == hash {
				return MaxDeliveryBytes, true
			}
		}
	}
	return 0, false
}
