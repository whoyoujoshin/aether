package shopper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// SavedQuote is the file a later buy loads. It holds no key and no
// signature.
type SavedQuote struct {
	ChainID  string    `json:"chain_id"`
	SavedAt  time.Time `json:"saved_at"`
	Offer    Offer     `json:"offer"`
	Decision Decision  `json:"decision"`
}

// SaveQuote writes quotes/<id>.json under dir. The directory is created
// if needed. On Windows the permission bits are the closest the
// filesystem allows; the directory stays in the user profile.
func SaveQuote(dir, chainID string, o Offer, d Decision) error {
	if o.QuoteID == "" {
		return nil
	}
	quotes := filepath.Join(dir, "quotes")
	if err := os.MkdirAll(quotes, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(SavedQuote{ChainID: chainID, SavedAt: time.Now().UTC(), Offer: o, Decision: d}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(quotes, o.QuoteID+".json"), body, 0o600)
}

// LoadQuote reads a quote saved by SaveQuote.
func LoadQuote(dir, id string) (SavedQuote, error) {
	b, err := os.ReadFile(filepath.Join(dir, "quotes", id+".json"))
	if err != nil {
		return SavedQuote{}, err
	}
	var s SavedQuote
	if err := json.Unmarshal(b, &s); err != nil {
		return SavedQuote{}, err
	}
	return s, nil
}
