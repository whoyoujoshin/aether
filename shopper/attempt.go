package shopper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	attemptSigned   = "signed"
	attemptRejected = "rejected"
	attemptUnknown  = "unknown"
	attemptSettled  = "settled"
	attemptFailed   = "failed"
)

// attempt is one idempotency key. TxB64 is the signed transaction,
// kept so a retry can send the same bytes. It is never logged.
type attempt struct {
	Key           string    `json:"key"`
	QuoteID       string    `json:"quote_id"`
	Payer         string    `json:"payer"`
	TxHash        string    `json:"tx_hash,omitempty"`
	TxB64         string    `json:"tx_b64,omitempty"`
	State         string    `json:"state"`
	URL           string    `json:"url"`
	Method        string    `json:"method"`
	Amount        string    `json:"amount"`
	Asset         string    `json:"asset"`
	PayTo         string    `json:"pay_to"`
	Reason        string    `json:"reason,omitempty"`
	ResourceTries int       `json:"resource_tries,omitempty"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	Updated       time.Time `json:"updated"`
}

func attemptPath(dir, key string) string {
	return filepath.Join(dir, "attempts", key+".json")
}

func loadAttempt(dir, key string) (*attempt, error) {
	b, err := os.ReadFile(attemptPath(dir, key))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a attempt
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func saveAttempt(dir string, a *attempt) error {
	a.Updated = time.Now().UTC()
	if err := os.MkdirAll(filepath.Join(dir, "attempts"), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(attemptPath(dir, a.Key), b, 0o600)
}

// otherInFlight reports another key for this payer that has been signed
// and is not yet known to have landed or failed.
func otherInFlight(dir, payer, exceptKey string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "attempts"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "attempts", e.Name()))
		if err != nil {
			return "", err
		}
		var a attempt
		if json.Unmarshal(b, &a) != nil {
			continue
		}
		if a.Key == exceptKey || a.Payer != payer || a.TxHash == "" {
			continue
		}
		switch a.State {
		case attemptSigned, attemptUnknown:
			return a.Key, nil
		}
	}
	return "", nil
}

// AttemptStatus is the local state of one idempotency key: its state
// word and tx hash. A missing key returns empty strings and a nil error.
func AttemptStatus(dir, key string) (state, hash string, err error) {
	a, err := loadAttempt(dir, key)
	if err != nil || a == nil {
		return "", "", err
	}
	return a.State, a.TxHash, nil
}
