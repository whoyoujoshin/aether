package shopper

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

const approvalDomain = "aether-shopper-approval/v1\n"

// Approval is one request waiting for a person. It cannot raise a cap.
type Approval struct {
	ID             string    `json:"id"`
	QuoteID        string    `json:"quote_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	PayTo          string    `json:"pay_to"`
	Amount         string    `json:"amount"`
	Asset          string    `json:"asset"`
	PriceDisplay   string    `json:"price_display"`
	URL            string    `json:"url,omitempty"`
	Link           string    `json:"link,omitempty"`
	Name           string    `json:"name,omitempty"`
	Kind           string    `json:"kind"`
	Expires        time.Time `json:"expires"`
}

type signedDecision struct {
	ID        string `json:"id"`
	Decision  string `json:"decision"`
	PubKey    string `json:"pub_key"`
	Signature string `json:"signature"`
}

func (b Buyer) requestApproval(key string, offer Offer, kind string) Approval {
	id := approvalID()
	a := Approval{
		ID: id, QuoteID: offer.QuoteID, IdempotencyKey: key, PayTo: offer.PayTo,
		Amount: offer.Amount, Asset: offer.Asset, PriceDisplay: offer.PriceDisplay,
		URL: offer.URL, Name: offer.Name, Kind: kind, Expires: time.Now().Add(24 * time.Hour).UTC(),
	}
	if b.ExplorerURL != "" {
		a.Link = b.ExplorerURL + "/shop/approve/" + id
	}
	_ = saveApproval(b.StateDir, a)
	return a
}

func saveApproval(dir string, a Approval) error {
	folder := filepath.Join(dir, "approvals")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, a.ID+".json"), body, 0o600)
}

func loadApproval(dir, id string) (Approval, error) {
	b, err := os.ReadFile(filepath.Join(dir, "approvals", id+".json"))
	if err != nil {
		return Approval{}, err
	}
	var a Approval
	err = json.Unmarshal(b, &a)
	return a, err
}

func approvalID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ApprovalMessage is the exact bytes the approver signs.
func ApprovalMessage(a Approval, decision string) []byte {
	return []byte(approvalDomain + a.ID + "\n" + decision + "\n" + a.QuoteID + "\n" + a.Amount + "\n" + a.Asset + "\n" + a.PayTo + "\n")
}

// SignApproval signs approve or reject with keyName. The spending key
// is not accepted: the signer must be the configured approver.
func SignApproval(dir, id, decision, approver string, w *wallet.Wallet, keyName string) error {
	if decision != "approve" && decision != "reject" {
		return fail(CodeRefused, NextStop, "A decision is approve or reject.")
	}
	a, err := loadApproval(dir, id)
	if err != nil {
		return fail(CodeRefused, NextStop, "No approval request with that id.")
	}
	if time.Now().After(a.Expires) {
		return fail(CodeRefused, NextStop, "That approval request has expired.")
	}
	acc, err := w.GetAccount(keyName)
	if err != nil {
		return fail(CodeRefused, NextStop, "The approver key could not be opened.")
	}
	if approver != "" && acc.Address != approver {
		return fail(CodeRefused, NextStop, "That key is not the approver. Nothing was approved.")
	}
	sig, pub, err := w.SignBytes(keyName, ApprovalMessage(a, decision))
	if err != nil {
		return fail(CodeRefused, NextStop, "The decision could not be signed.")
	}
	body, err := json.MarshalIndent(signedDecision{
		ID: id, Decision: decision,
		PubKey: base64.StdEncoding.EncodeToString(pub), Signature: base64.StdEncoding.EncodeToString(sig),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "approvals", id+".decision.json"), body, 0o600)
}

// approvalAllows reports whether a person signed an approval for this
// exact offer, and whether they signed a rejection.
func (b Buyer) approvalAllows(offer Offer) (ok, rejected bool) {
	if b.Policy.Approver == "" {
		return false, false
	}
	entries, err := os.ReadDir(filepath.Join(b.StateDir, "approvals"))
	if err != nil {
		return false, false
	}
	for _, e := range entries {
		if len(e.Name()) < len(".decision.json") || e.Name()[len(e.Name())-len(".decision.json"):] != ".decision.json" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".decision.json")]
		a, err := loadApproval(b.StateDir, id)
		if err != nil || a.QuoteID != offer.QuoteID || a.Amount != offer.Amount || a.Asset != offer.Asset || a.PayTo != offer.PayTo {
			continue
		}
		if time.Now().After(a.Expires) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(b.StateDir, "approvals", e.Name()))
		if err != nil {
			continue
		}
		var d signedDecision
		if json.Unmarshal(raw, &d) != nil || d.ID != id {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(d.PubKey)
		if err != nil {
			continue
		}
		sig, err := base64.StdEncoding.DecodeString(d.Signature)
		if err != nil {
			continue
		}
		pk := &mldsa.PubKey{Key: pub}
		if sdk.AccAddress(pk.Address()).String() != b.Policy.Approver {
			continue
		}
		if !pk.VerifySignature(ApprovalMessage(a, d.Decision), sig) {
			continue
		}
		if d.Decision == "reject" {
			return false, true
		}
		if d.Decision == "approve" {
			return true, false
		}
	}
	return false, false
}
