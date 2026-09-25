package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
)

// The owner's side of approvals, run as `agentmcp approvals`,
// `agentmcp approve <id>` or `agentmcp reject <id>` on the machine the
// server runs on.

func runApprovalCommand(args []string) error {
	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	fs.StringVar(&keyringDir, "keyring-dir", defaultKeyringDir(), "the agent's keyring directory (locates the state file)")
	fs.StringVar(&stateFile, "state-file", "", "agentmcp's state file (defaults to <keyring-dir>/agentmcp-spend.json)")
	ownerDir := fs.String("approver-keyring-dir", "", "the OWNER's keyring directory -- never the agent's")
	ownerBackend := fs.String("approver-keyring-backend", "os", `the owner's keyring backend ("os", "file" or "test")`)
	ownerKey := fs.String("approver-key", "", "name of the owner's key in that keyring")
	cmd := args[0]
	var id string
	rest := args[1:]
	if cmd != "approvals" {
		if len(rest) == 0 || rest[0] == "" || rest[0][0] == '-' {
			return fmt.Errorf("usage: agentmcp %s <approval id> --approver-keyring-dir DIR --approver-key NAME", cmd)
		}
		id, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if stateFile == "" {
		stateFile = filepath.Join(keyringDir, "agentmcp-spend.json")
	}
	st, err := loadState()
	if err != nil {
		return err
	}

	if cmd == "approvals" {
		reqs := make([]*approvalRequest, 0, len(st.Approvals))
		for _, r := range st.Approvals {
			reqs = append(reqs, r)
		}
		sort.Slice(reqs, func(i, j int) bool { return reqs[i].CreatedAt.Before(reqs[j].CreatedAt) })
		if len(reqs) == 0 {
			fmt.Println("no payments waiting for approval")
		}
		for _, r := range reqs {
			amt, _ := math.NewIntFromString(r.Amount)
			state := "waiting"
			if d := readDecisionUnverified(r.ID); d != "" {
				state = d + "d (not yet picked up)"
			}
			fmt.Printf("%s  %s AETH to %s  memo=%q  requested %s  [%s]\n", r.ID, wallet.FormatAeth(amt), r.To, r.Memo, r.CreatedAt.Format(time.RFC3339), state)
		}
		return nil
	}

	var req *approvalRequest
	for _, r := range st.Approvals {
		if r.ID == id {
			req = r
		}
	}
	if req == nil {
		return fmt.Errorf("no payment is waiting for approval %s (see `agentmcp approvals`)", id)
	}
	if *ownerDir == "" || *ownerKey == "" {
		return fmt.Errorf("--approver-keyring-dir and --approver-key are required: decisions are signed with the owner's key")
	}
	if abs(*ownerDir) == abs(keyringDir) {
		return fmt.Errorf("the approver keyring must not be the agent's keyring: the agent could then approve its own payments")
	}
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	owner, err := wallet.NewWallet("aetherd", *ownerBackend, *ownerDir, codec.NewProtoCodec(registry))
	if err != nil {
		return err
	}
	decision := decisionApprove
	if cmd == "reject" {
		decision = decisionReject
	}
	sig, pub, err := owner.SignBytes(*ownerKey, approvalMessage(req.ID, decision, req.paramsHash()))
	if err != nil {
		return fmt.Errorf("signing with the owner's key: %w", err)
	}
	d := decisionFile{ID: req.ID, Decision: decision, ParamsHash: req.paramsHash(),
		PubKey: base64.StdEncoding.EncodeToString(pub), Signature: base64.StdEncoding.EncodeToString(sig), DecidedAt: time.Now().UTC()}
	bz, _ := json.MarshalIndent(d, "", "  ")
	if err := os.MkdirAll(approvalsDir(), 0o700); err != nil {
		return err
	}
	tmp := decisionPath(req.ID) + ".tmp"
	if err := os.WriteFile(tmp, bz, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, decisionPath(req.ID)); err != nil {
		return err
	}
	amt, _ := math.NewIntFromString(req.Amount)
	fmt.Printf("%sd: %s AETH to %s. The agent's next send_aeth with that idempotencyKey will %s.\n",
		decision, wallet.FormatAeth(amt), req.To, map[string]string{decisionApprove: "send it", decisionReject: "be refused"}[decision])
	return nil
}

func readDecisionUnverified(id string) string {
	bz, err := os.ReadFile(decisionPath(id))
	if err != nil {
		return ""
	}
	var d decisionFile
	if json.Unmarshal(bz, &d) != nil {
		return ""
	}
	return d.Decision
}

func abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return filepath.Clean(a)
}
