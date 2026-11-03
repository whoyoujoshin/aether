package shopper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"

	"github.com/whoyoujoshin/aether/x402"
)

// Receipt is the record of one payment. It has the chain hash and the
// facilitator's settlement response. It does not have the signed
// transaction or the payment header.
type Receipt struct {
	ChainID        string              `json:"chain_id"`
	Network        string              `json:"network"`
	Payer          string              `json:"payer"`
	PayTo          string              `json:"pay_to"`
	Asset          string              `json:"asset"`
	Amount         string              `json:"amount"`
	Rail           string              `json:"rail"`
	Method         string              `json:"method"`
	URL            string              `json:"url"`
	BodyHash       string              `json:"body_hash,omitempty"`
	QuoteID        string              `json:"quote_id"`
	IdempotencyKey string              `json:"idempotency_key"`
	TxHash         string              `json:"tx_hash"`
	Facilitator    x402.SettleResponse `json:"facilitator"`
	ChainFound     bool                `json:"chain_found"`
	ChainCode      uint32              `json:"chain_code"`
	ChainHeight    int64               `json:"chain_height,omitempty"`
	HTTPStatus     int                 `json:"http_status,omitempty"`
	At             time.Time           `json:"at"`
}

func saveReceipt(dir string, r Receipt) error {
	if r.TxHash == "" {
		return nil
	}
	folder := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, r.TxHash+".json"), b, 0o600)
}

func loadReceipt(dir, hash string) (Receipt, error) {
	b, err := os.ReadFile(filepath.Join(dir, "receipts", hash+".json"))
	if err != nil {
		return Receipt{}, err
	}
	var r Receipt
	err = json.Unmarshal(b, &r)
	return r, err
}

// spentSince sums receipt amounts at or after since, by denom. Unknown
// attempts are included too: they may still land.
func spentSince(dir string, since time.Time) (map[string]sdkmath.Int, error) {
	out := map[string]sdkmath.Int{}
	add := func(asset, amount string, at time.Time) {
		if asset == "" || at.Before(since) {
			return
		}
		v, ok := sdkmath.NewIntFromString(amount)
		if !ok {
			return
		}
		if cur, ok := out[asset]; ok {
			out[asset] = cur.Add(v)
		} else {
			out[asset] = v
		}
	}
	receipts, _ := os.ReadDir(filepath.Join(dir, "receipts"))
	for _, e := range receipts {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := loadReceipt(dir, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || !r.ChainFound || r.ChainCode != 0 {
			continue
		}
		add(r.Asset, r.Amount, r.At)
	}
	attempts, _ := os.ReadDir(filepath.Join(dir, "attempts"))
	for _, e := range attempts {
		b, err := os.ReadFile(filepath.Join(dir, "attempts", e.Name()))
		if err != nil {
			continue
		}
		var a attempt
		if json.Unmarshal(b, &a) != nil || a.State != attemptUnknown {
			continue
		}
		add(a.Asset, a.Amount, a.Updated)
	}
	return out, nil
}
