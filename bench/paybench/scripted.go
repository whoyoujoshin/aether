package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// scriptedAgent is the reference agent: it does each task the way a
// careful agent should, through the same tools and the same harness, with
// no model. It checks the harness and the scoring end to end and sets the
// ceiling the models are compared to.
type scriptedAgent struct{}

// tokenPattern is a wallet token in a prompt: a short handle, or the
// sealed token the test wallet issued before handles.
var tokenPattern = regexp.MustCompile(`aether-test-wallet-v1\.[A-Za-z0-9_-]+|\btw[0-9a-hjkmnp-tv-z]{16}\b`)
var namePattern = regexp.MustCompile(`under the name "([^"]+)"`)

type service struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	PayTo string `json:"payTo"`
	Price struct {
		Base  string `json:"base"`
		Denom string `json:"denom"`
	} `json:"price"`
}

func (s service) price() int64 {
	var n int64
	fmt.Sscan(s.Price.Base, &n)
	return n
}

func (scriptedAgent) Run(ctx context.Context, _, prompt string, _ []Tool, call ToolCaller, _ int) (string, Usage, error) {
	u := Usage{}
	do := func(name string, args map[string]any, into any) error {
		bz, _ := json.Marshal(args)
		u.Turns++
		text, isErr := call(ctx, name, bz)
		if isErr {
			return fmt.Errorf("%s: %s", name, text)
		}
		if into == nil {
			return nil
		}
		return json.Unmarshal([]byte(text), into)
	}
	services := func() ([]service, error) {
		var out struct {
			Services []service `json:"services"`
		}
		err := do("find_services", map[string]any{}, &out)
		return out.Services, err
	}
	cheapest := func(ss []service) (service, error) {
		var best service
		for _, s := range ss {
			if s.Price.Denom == "uaeth" && (best.URL == "" || s.price() < best.price()) {
				best = s
			}
		}
		if best.URL == "" {
			return best, errors.New("no AETH-priced service listed")
		}
		return best, nil
	}
	type bought struct {
		Status string `json:"status"`
		Body   string `json:"body"`
		TxHash string `json:"txHash"`
	}
	token := tokenPattern.FindString(prompt)

	switch {
	case strings.Contains(prompt, "Create an Aether test wallet"):
		var w struct {
			WalletToken string `json:"walletToken"`
			Address     string `json:"address"`
		}
		if err := do("create_test_wallet", map[string]any{}, &w); err != nil {
			return "", u, err
		}
		for i := 0; i < 60; i++ {
			var s walletState
			if err := do("get_test_wallet", map[string]any{"walletToken": w.WalletToken}, &s); err == nil && s.Funded {
				return fmt.Sprintf("Address %s, balance %d uaeth.", s.Address, s.uaeth()), u, nil
			}
			time.Sleep(3 * time.Second)
		}
		return "", u, errors.New("never funded")

	case strings.Contains(prompt, "Aether chain pulse"):
		ss, err := services()
		if err != nil {
			return "", u, err
		}
		for _, s := range ss {
			if s.Name == "Aether chain pulse" {
				var b bought
				if err := do("buy_service", map[string]any{"walletToken": token, "url": s.URL, "maxAmount": "0.001 AETH"}, &b); err != nil {
					return "", u, err
				}
				var body struct {
					Height int64 `json:"height"`
				}
				_ = json.Unmarshal([]byte(b.Body), &body)
				return fmt.Sprintf("Height %d; payment %s.", body.Height, b.TxHash), u, nil
			}
		}
		return "", u, errors.New("pulse isn't listed")

	case strings.Contains(prompt, "address report"):
		ss, err := services()
		if err != nil {
			return "", u, err
		}
		for _, s := range ss {
			if s.Name == "Aether address report" && s.price() > 500 {
				return fmt.Sprintf("Not bought: it costs %s uaeth, over the 0.0005 AETH budget.", s.Price.Base), u, nil
			}
		}
		return "Not bought: no address report service within budget.", u, nil

	case strings.Contains(prompt, "cheapest service"):
		ss, err := services()
		if err != nil {
			return "", u, err
		}
		best, err := cheapest(ss)
		if err != nil {
			return "", u, err
		}
		var b bought
		if err := do("buy_service", map[string]any{"walletToken": token, "url": best.URL, "maxAmount": best.Price.Base + "uaeth"}, &b); err != nil {
			return "", u, err
		}
		return fmt.Sprintf("%s returned: %s", best.Name, b.Body), u, nil

	case strings.Contains(prompt, "agent quest"):
		name := "paybench"
		if m := namePattern.FindStringSubmatch(prompt); m != nil {
			name = m[1]
		}
		var q struct {
			QuestAddress     string `json:"questAddress"`
			DirectoryAddress string `json:"directoryAddress"`
		}
		if err := do("get_quest", map[string]any{"walletToken": token}, &q); err != nil {
			return "", u, err
		}
		if err := do("send_from_test_wallet", map[string]any{"walletToken": token, "to": q.QuestAddress, "amount": "1uaeth", "memo": "aether-quest:" + name}, nil); err != nil {
			return "", u, err
		}
		ss, err := services()
		if err != nil {
			return "", u, err
		}
		best, err := cheapest(ss)
		if err != nil {
			return "", u, err
		}
		for i := 0; i < 5; i++ {
			if err := do("buy_service", map[string]any{"walletToken": token, "url": best.URL, "maxAmount": best.Price.Base + "uaeth"}, nil); err != nil {
				return "", u, err
			}
		}
		if err := do("send_from_test_wallet", map[string]any{"walletToken": token, "to": q.DirectoryAddress, "amount": "1uaeth", "memo": "x402-rate:5:" + best.URL}, nil); err != nil {
			return "", u, err
		}
		return "Joined, bought five times and rated " + best.Name + ".", u, nil
	}
	return "", u, errors.New("the reference agent doesn't know this task")
}
