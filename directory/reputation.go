package directory

import (
	"sort"
	"strconv"
	"strings"

	sdkmath "cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/wallet"
)

// Reputation, from what's on chain:
//
//   - Stats: payments to a service's payee in a recent window -- how many,
//     from how many accounts, how much.
//   - Ratings: a buyer rates a service by sending AnnounceAmount uaeth to
//     Address() with memo RatePrefix + "<1-5>:" + the service's URL. A
//     rating counts only if the rater paid that service's payee, in the
//     window, before rating it; the latest rating per rater counts.
//
// Fees are zero, so a seller can pay itself from accounts it controls
// for free: Stats, and ratings from accounts you know nothing about, can
// be manufactured. What can't be is a rating from an account you trust
// -- your owner's, your own, or ones you choose -- which is why callers
// summarize ratings by who they trust (Summarize).

// RatePrefix starts a rating's memo: "x402-rate:<score>:<url>".
const RatePrefix = "x402-rate:"

// DefaultWindow is how far back reputation looks, in blocks (about a
// week of ~60s blocks).
const DefaultWindow = 10_080

// Rating is one account's latest rating of a service.
type Rating struct {
	Rater  string `json:"rater"`
	URL    string `json:"url"`
	Score  int    `json:"score"` // 1-5
	Height int64  `json:"height"`
	TxHash string `json:"txHash"`
}

// RatingMemo is the memo that rates url with score.
func RatingMemo(url string, score int) (string, error) {
	u, err := NormalizeURL(url)
	if err != nil {
		return "", err
	}
	if score < 1 || score > 5 {
		return "", errScore
	}
	return RatePrefix + strconv.Itoa(score) + ":" + u, nil
}

type scoreError struct{}

func (scoreError) Error() string { return "a rating's score is 1 to 5" }

var errScore = scoreError{}

// Ratings folds payments to Address() (oldest first) into each rater's
// latest rating per service, at or above sinceHeight.
func Ratings(payments []wallet.IncomingPayment, sinceHeight int64) []Rating {
	type key struct{ rater, url string }
	latest := map[key]Rating{}
	for _, p := range payments {
		if p.Code != 0 || p.From == "" || p.Height < sinceHeight || p.Amount.AmountOf(wallet.BaseDenom).LT(sdkInt(AnnounceAmount)) {
			continue
		}
		rest, ok := strings.CutPrefix(p.Memo, RatePrefix)
		if !ok || len(rest) < 3 || rest[1] != ':' || rest[0] < '1' || rest[0] > '5' {
			continue
		}
		u, err := NormalizeURL(rest[2:])
		if err != nil {
			continue
		}
		latest[key{p.From, u}] = Rating{Rater: p.From, URL: u, Score: int(rest[0] - '0'), Height: p.Height, TxHash: p.Hash}
	}
	out := make([]Rating, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Height > out[j].Height })
	return out
}

// Stats summarizes payments to a service's payee.
type Stats struct {
	SinceHeight int64       `json:"sinceHeight"`
	Payments    int         `json:"payments"`
	Payers      int         `json:"payers"` // distinct accounts
	Volume      sdkmath.Int `json:"volume"` // uaeth
}

// Reputation is what the chain says about one service.
type Reputation struct {
	Stats Stats `json:"stats"`
	// Ratings backed by a payment from the rater to the payee.
	Ratings []Rating `json:"ratings"`
}

// Assess computes a service's reputation from payments to its payee at
// or above sinceHeight (oldest first) and all current ratings.
func Assess(url, payTo string, paid []wallet.IncomingPayment, ratings []Rating, sinceHeight int64) Reputation {
	rep := Reputation{Stats: Stats{SinceHeight: sinceHeight, Volume: sdkmath.ZeroInt()}, Ratings: []Rating{}}
	firstPaid := map[string]int64{} // payer -> height of their first payment in the window
	for _, p := range paid {
		if p.Code != 0 || p.From == "" || p.From == payTo || p.Height < sinceHeight {
			continue
		}
		rep.Stats.Payments++
		rep.Stats.Volume = rep.Stats.Volume.Add(p.Amount.AmountOf(wallet.BaseDenom))
		if h, seen := firstPaid[p.From]; !seen || p.Height < h {
			firstPaid[p.From] = p.Height
		}
	}
	rep.Stats.Payers = len(firstPaid)
	for _, r := range ratings {
		if r.URL != url || r.Rater == payTo {
			continue
		}
		if h, ok := firstPaid[r.Rater]; ok && h <= r.Height {
			rep.Ratings = append(rep.Ratings, r)
		}
	}
	return rep
}

// RatingSummary is the count and average of some ratings.
type RatingSummary struct {
	Count   int     `json:"count"`
	Average float64 `json:"average,omitempty"`
}

// Summarize summarizes the ratings whose rater include accepts (nil: all).
func Summarize(ratings []Rating, include func(rater string) bool) RatingSummary {
	var s RatingSummary
	total := 0
	for _, r := range ratings {
		if include == nil || include(r.Rater) {
			s.Count++
			total += r.Score
		}
	}
	if s.Count > 0 {
		s.Average = float64(total) / float64(s.Count)
	}
	return s
}
