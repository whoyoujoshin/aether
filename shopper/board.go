package shopper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/jobs"
	"github.com/whoyoujoshin/aether/paywall"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x402"
)

// JobOffer is one seller's price on an open job. Notes are untrusted.
type JobOffer struct {
	ID          string
	Title       string
	Description string
	Tags        []string
	Budget      string
	Seller      string
	Price       string
	Note        string
	Height      int64
}

// ProbePayment performs one unpaid request and reads the payment header.
// client should refuse redirects. A transport error is returned; a 402
// is a normal ProbeResult.
func ProbePayment(ctx context.Context, client *http.Client, method, rawURL string, body []byte) (ProbeResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var rdr io.Reader
	if len(body) > 0 {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return ProbeResult{}, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	out := ProbeResult{Status: resp.StatusCode}
	if h := resp.Header.Get(paywall.HeaderPaymentRequired); h != "" {
		var pr x402.PaymentRequired
		if err := paywall.DecodeHeader(h, &pr); err == nil {
			out.Required = &pr
		}
	}
	return out, nil
}

// FetchOpenJobs reads the public board. query is sent as the host's q,
// and wordSets (including synonym expansions) are queried too, so a
// phrase the host would miss can still be found when it is in a title.
// Failures return what was read plus the error.
func FetchOpenJobs(ctx context.Context, client *http.Client, boardURL string, queries []string) ([]JobOffer, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	boardURL = strings.TrimRight(boardURL, "/")
	seen := map[string]bool{}
	var ids []string
	var firstErr error
	if len(queries) == 0 {
		queries = []string{""}
	}
	for _, q := range queries {
		list, err := getList(ctx, client, boardURL, q)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, j := range list {
			if j.ID == "" || seen[j.ID] {
				continue
			}
			seen[j.ID] = true
			ids = append(ids, j.ID)
			if len(ids) >= 40 {
				break
			}
		}
	}
	var out []JobOffer
	for _, id := range ids {
		offers, err := getJob(ctx, client, boardURL, id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, offers...)
	}
	return out, firstErr
}

func getList(ctx context.Context, client *http.Client, board, q string) ([]jobSummary, error) {
	u, err := url.Parse(board)
	if err != nil {
		return nil, err
	}
	qv := u.Query()
	qv.Set("state", "open")
	qv.Set("limit", "500")
	if strings.TrimSpace(q) != "" {
		qv.Set("q", q)
	}
	u.RawQuery = qv.Encode()
	var body struct {
		Jobs []jobSummary `json:"jobs"`
	}
	if err := getJSON(ctx, client, u.String(), &body); err != nil {
		return nil, err
	}
	return body.Jobs, nil
}

type jobSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func getJob(ctx context.Context, client *http.Client, board, id string) ([]JobOffer, error) {
	var body struct {
		ID     string       `json:"id"`
		Height int64        `json:"height"`
		Offers []jobs.Offer `json:"offers"`
		Spec   *struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Budget      string   `json:"budget"`
			Tags        []string `json:"tags"`
		} `json:"spec"`
	}
	if err := getJSON(ctx, client, board+"/"+id, &body); err != nil {
		return nil, err
	}
	base := JobOffer{ID: body.ID, Height: body.Height}
	if body.Spec != nil {
		base.Title = body.Spec.Title
		base.Description = body.Spec.Description
		base.Budget = body.Spec.Budget
		base.Tags = body.Spec.Tags
	}
	if len(body.Offers) == 0 {
		return []JobOffer{base}, nil
	}
	var out []JobOffer
	for _, o := range body.Offers {
		row := base
		row.Seller = o.Seller
		row.Price = o.Price
		row.Note = o.Note
		out = append(out, row)
	}
	return out, nil
}

func getJSON(ctx context.Context, client *http.Client, raw string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("job board %s: HTTP %d", raw, resp.StatusCode)
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("job board %s: %w", raw, err)
	}
	return nil
}

func rankJobs(rows []JobOffer, sets [][]string, assets *wallet.Assets, chainID, method, bodyHash string) []Offer {
	var out []Offer
	for _, j := range rows {
		hay := strings.ToLower(j.Title + " " + j.Description + " " + strings.Join(j.Tags, " ") + " " + j.Note)
		if len(sets) > 0 && !Matches(sets, hay) {
			continue
		}
		o := Offer{
			Rail: RailJob, Name: j.Title, Description: j.Description, JobID: j.ID,
			ListedHeight: j.Height, PayTo: j.Seller, Buyable: false,
			Reason: "Custom job, not ready to buy yet.",
		}
		if o.Name == "" {
			o.Name = "Job " + j.ID
		}
		if j.Price != "" && assets != nil {
			if asset, amt, err := assets.Parse(j.Price); err == nil {
				o.Amount = amt.String()
				o.Asset = asset.Denom
				o.Symbol = asset.Symbol
				o.PriceDisplay = asset.Format(amt)
			} else {
				o.PriceDisplay = j.Price
			}
		} else if j.Budget != "" {
			o.PriceDisplay = j.Budget
		}
		o.QuoteID = QuoteID(chainID, RailJob, j.ID, method, bodyHash, o.Amount, o.Asset, o.PayTo, "escrow")
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aok := sdkmath.NewIntFromString(out[i].Amount)
		bi, bok := sdkmath.NewIntFromString(out[j].Amount)
		if aok && bok && out[i].Asset == out[j].Asset && out[i].Asset != "" && !ai.Equal(bi) {
			return ai.LT(bi)
		}
		return out[i].JobID < out[j].JobID
	})
	return out
}

// QueryVariants is the host q values to try: the caller's text and up
// to three synonym expansions.
func QueryVariants(syns []Synonym, query string) []string {
	q := strings.TrimSpace(query)
	var out []string
	if q != "" {
		out = append(out, q)
	}
	seen := map[string]bool{strings.ToLower(q): true}
	for _, set := range Expand(syns, query) {
		s := strings.Join(set, " ")
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= 4 {
			break
		}
	}
	return out
}
