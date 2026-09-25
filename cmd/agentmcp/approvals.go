package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
)

// Owner approvals: a payment above --approval-threshold isn't signed or
// sent until the owner approves it. The owner decides with
// `agentmcp approve|reject <id>`, which signs the decision with the
// owner's own key (--approver): the agent's key can't approve anything,
// so an agent that can write files on this machine still can't approve
// its own payments.
//
// Decisions are separate files (one per request) under
// <state dir>/approvals, so the owner's command never writes the
// server's state file.

const (
	approvalTTL           = 24 * time.Hour
	approvalSigningDomain = "aether-agentmcp-approval/v1\n"
	decisionApprove       = "approve"
	decisionReject        = "reject"
	statusPendingApproval = "pending_approval"
)

var (
	approvalThreshold math.Int // nil: no approvals
	approver          string   // owner address that must sign decisions
)

// approvalRequest is a payment waiting for the owner.
type approvalRequest struct {
	ID        string    `json:"id"`
	From      string    `json:"from"` // the agent
	To        string    `json:"to"`
	Amount    string    `json:"amountUaeth"`
	Memo      string    `json:"memo,omitempty"`
	Key       string    `json:"idempotencyKey"`
	CreatedAt time.Time `json:"createdAt"`
}

// paramsHash pins a decision to exactly this payment.
func (a *approvalRequest) paramsHash() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{a.ID, a.From, a.To, a.Amount, a.Memo, a.Key}, "\n")))
	return hex.EncodeToString(sum[:])
}

func approvalMessage(id, decision, paramsHash string) []byte {
	return []byte(approvalSigningDomain + id + "\n" + decision + "\n" + paramsHash + "\n")
}

type decisionFile struct {
	ID         string    `json:"id"`
	Decision   string    `json:"decision"`
	ParamsHash string    `json:"paramsHash"`
	PubKey     string    `json:"pubKey"`
	Signature  string    `json:"signature"`
	DecidedAt  time.Time `json:"decidedAt"`
}

func approvalsDir() string { return filepath.Join(filepath.Dir(stateFile), "approvals") }

func decisionPath(id string) string { return filepath.Join(approvalsDir(), id+".json") }

// readDecision returns the owner's verified decision on req, "" if
// there's none yet. A decision that fails verification is logged and
// ignored: a forged file must not do anything.
func readDecision(req *approvalRequest) string {
	bz, err := os.ReadFile(decisionPath(req.ID))
	if err != nil {
		return ""
	}
	var d decisionFile
	if err := json.Unmarshal(bz, &d); err != nil {
		log.Printf("approval %s: unreadable decision file ignored", req.ID)
		return ""
	}
	if err := verifyDecision(req, d); err != nil {
		log.Printf("approval %s: decision ignored: %v", req.ID, err)
		return ""
	}
	return d.Decision
}

func verifyDecision(req *approvalRequest, d decisionFile) error {
	if d.ID != req.ID || (d.Decision != decisionApprove && d.Decision != decisionReject) {
		return errors.New("malformed decision")
	}
	if d.ParamsHash != req.paramsHash() {
		return errors.New("decision is for a different payment")
	}
	pub, err := base64.StdEncoding.DecodeString(d.PubKey)
	if err != nil || len(pub) != mldsa.PubKeySize {
		return errors.New("bad public key")
	}
	pk := &mldsa.PubKey{Key: pub}
	want, err := sdk.AccAddressFromBech32(approver)
	if err != nil || !bytes.Equal(pk.Address(), want) {
		return fmt.Errorf("not signed by the approver %s", approver)
	}
	sig, err := base64.StdEncoding.DecodeString(d.Signature)
	if err != nil || !pk.VerifySignature(approvalMessage(d.ID, d.Decision, d.ParamsHash), sig) {
		return errors.New("bad signature")
	}
	return nil
}

// approvalGate runs for a new payment of amount. It returns proceed=true
// if the payment may be signed and sent now; otherwise out (pending) or
// err (rejected) says why not. Caller holds stateMu.
func approvalGate(st *agentState, agent string, in sendAethInput, amount math.Int) (proceed bool, out sendAethOutput, err error) {
	if approvalThreshold.IsNil() || amount.LTE(approvalThreshold) {
		return true, sendAethOutput{}, nil
	}
	req := st.Approvals[in.IdempotencyKey]
	if req != nil && (req.To != in.To || req.Amount != amount.String() || req.Memo != in.Memo || req.From != agent) {
		return false, out, newError(codeIdempotencyConflict, fmt.Sprintf("idempotencyKey %q is awaiting approval for a different payment", in.IdempotencyKey))
	}
	if req != nil && time.Since(req.CreatedAt) > approvalTTL && readDecision(req) == "" {
		delete(st.Approvals, in.IdempotencyKey) // expired undecided: ask again
		req = nil
	}
	if req == nil {
		id := make([]byte, 6)
		if _, err := rand.Read(id); err != nil {
			return false, out, err
		}
		req = &approvalRequest{ID: hex.EncodeToString(id), From: agent, To: in.To, Amount: amount.String(), Memo: in.Memo, Key: in.IdempotencyKey, CreatedAt: time.Now().UTC()}
		st.Approvals[in.IdempotencyKey] = req
		if err := st.save(); err != nil {
			return false, out, err
		}
		notify("approval_requested", map[string]any{
			"approvalId": req.ID, "to": req.To, "amount": newAmountDTO(amount), "memo": req.Memo,
			"approve": "agentmcp approve " + req.ID, "reject": "agentmcp reject " + req.ID,
		})
	}
	switch readDecision(req) {
	case decisionApprove:
		// Consumed by consumeApproval once the payment is recorded, so a
		// failure before then doesn't make the owner approve again.
		return true, sendAethOutput{}, nil
	case decisionReject:
		e := newError(codeApprovalRejected, fmt.Sprintf("the owner rejected this payment (approval %s); nothing was sent", req.ID))
		return false, out, e
	}
	return false, sendAethOutput{
		Status: statusPendingApproval, ApprovalID: req.ID,
		Amount: newAmountDTO(amount), From: payer(agent), To: in.To,
		Message: fmt.Sprintf("%s AETH is above the owner's approval threshold of %s AETH. Nothing was signed or sent. The owner approves with `agentmcp approve %s`; then call send_aeth again with the same idempotencyKey",
			formatAeth(amount), formatAeth(approvalThreshold), req.ID),
	}, nil
}

// consumeApproval drops key's approval once its payment is recorded.
// Caller holds stateMu and saves st.
func consumeApproval(st *agentState, key string) {
	if req := st.Approvals[key]; req != nil {
		delete(st.Approvals, key)
		_ = os.Remove(decisionPath(req.ID))
	}
}
