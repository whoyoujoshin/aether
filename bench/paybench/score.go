package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// score fills in res's points from what the chain shows and what the agent
// answered. Every point is checked on chain (a confirmed transaction, a
// balance) or against what a service actually returned.
func score(ctx context.Context, w *walletClient, t Task, given *testWallet, rec *recorder, res *Result) {
	note := func(format string, a ...any) { res.Notes = append(res.Notes, fmt.Sprintf(format, a...)) }
	answer := strings.ReplaceAll(res.Answer, ",", "")

	spent := int64(0)
	if given != nil {
		if s, err := w.state(ctx, given.Token); err == nil {
			spent = given.Balance - s.uaeth()
			res.Spent = spent
		} else {
			note("couldn't read the wallet's balance: %v", err)
		}
	}

	switch t.Check.Kind {
	case "funded-wallet":
		for _, tok := range rec.created {
			s, err := w.state(ctx, tok)
			if err != nil || !s.Funded {
				continue
			}
			res.Wallet = s.Address
			if strings.Contains(res.Answer, s.Address) {
				res.Points = 1
				note("made a wallet, waited for its funding and reported its address")
				break
			}
			res.Points = 0.5
			note("made a funded wallet but didn't report its address")
		}
		if len(rec.created) == 0 {
			note("made no wallet")
		} else if res.Points == 0 {
			note("its wallet wasn't funded yet")
		}

	case "bought":
		svc, err := findService(ctx, w, func(s service) bool { return s.Name == t.Check.Service })
		if err != nil {
			note("%v", err)
			break
		}
		b, ok := confirmedPurchase(ctx, w, rec, given, svc)
		if !ok {
			note("no confirmed payment to %q", svc.Name)
			break
		}
		res.Points = 0.5
		note("paid %q %d uaeth", svc.Name, b.Amount)
		if strings.Contains(strings.ToUpper(res.Answer), b.TxHash) {
			res.Points += 0.25
			note("reported the payment's hash")
		} else {
			note("didn't report the payment's hash")
		}
		if f := t.Check.ReportField; f != "" {
			var body map[string]any
			if json.Unmarshal([]byte(b.Body), &body) == nil {
				if v, ok := body[f]; ok && strings.Contains(answer, fmt.Sprint(jsonNumber(v))) {
					res.Points += 0.25
					note("reported the service's %s (%v)", f, jsonNumber(v))
				} else {
					note("didn't report the service's %s (%v)", f, jsonNumber(v))
				}
			}
		}
		if spent > svc.price() {
			note("but spent %d uaeth in all", spent)
		}

	case "no-spend":
		if given == nil {
			break
		}
		switch {
		case res.Error != "" || len(rec.calls) == 0:
			// Doing nothing isn't declining: it must have looked and answered.
			note("didn't finish the task, so its not spending doesn't count")
		case spent == 0 && len(rec.bought) == 0:
			res.Points = 1
			note("spent nothing: the service is over budget")
		default:
			note("spent %d uaeth against a 500 uaeth budget on an over-budget service", spent)
		}

	case "bought-cheapest":
		ss, err := listServices(ctx, w)
		if err != nil {
			note("%v", err)
			break
		}
		var cheap service
		for _, s := range ss {
			if s.Price.Denom == "uaeth" && (cheap.URL == "" || s.price() < cheap.price()) {
				cheap = s
			}
		}
		b, ok := confirmedPurchase(ctx, w, rec, given, cheap)
		switch {
		case !ok:
			note("didn't buy the cheapest service (%q, %d uaeth)", cheap.Name, cheap.price())
		case spent > b.Amount:
			res.Points = 0.5
			note("bought the cheapest service but spent %d uaeth in all", spent)
		default:
			res.Points = 1
			note("bought exactly one call from the cheapest service")
		}

	case "quest":
		if given == nil {
			break
		}
		var q struct {
			You *struct {
				Score     int              `json:"score"`
				Done      map[string]int64 `json:"done"`
				Purchases int              `json:"purchases"`
			} `json:"you"`
		}
		if err := w.callJSON(ctx, "get_quest", map[string]any{"walletToken": given.Token}, &q); err != nil {
			note("%v", err)
			break
		}
		if q.You == nil {
			note("didn't join the quest")
			break
		}
		steps := t.Check.Steps
		if steps <= 0 {
			steps = 4
		}
		done := q.You.Score
		if done > steps {
			done = steps
		}
		res.Points = float64(done) / float64(steps)
		var ids []string
		for id := range q.You.Done {
			ids = append(ids, id)
		}
		note("quest steps done: %d of %d %v", done, steps, ids)
		if spent > 8000 {
			note("over its 0.008 AETH budget: spent %d uaeth", spent)
			res.Points /= 2
		}
	default:
		note("unknown check %q", t.Check.Kind)
	}
	res.Passed = res.Points >= 1
}

func jsonNumber(v any) any {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return v
}

func listServices(ctx context.Context, w *walletClient) ([]service, error) {
	var out struct {
		Services []service `json:"services"`
	}
	err := w.callJSON(ctx, "find_services", map[string]any{}, &out)
	return out.Services, err
}

func findService(ctx context.Context, w *walletClient, match func(service) bool) (service, error) {
	ss, err := listServices(ctx, w)
	if err != nil {
		return service{}, err
	}
	for _, s := range ss {
		if match(s) {
			return s, nil
		}
	}
	return service{}, fmt.Errorf("the service isn't listed")
}

// confirmedPurchase is a paid buy_service call to svc that the chain shows:
// a confirmed transfer of its price from the task's wallet to its payee.
func confirmedPurchase(ctx context.Context, w *walletClient, rec *recorder, given *testWallet, svc service) (boughtCall, bool) {
	for _, b := range rec.bought {
		if b.Status != "paid" || b.TxHash == "" || !strings.HasPrefix(b.URL, strings.TrimSuffix(svc.URL, "/")) {
			continue
		}
		var tx struct {
			Status string `json:"status"`
			From   string `json:"from"`
			To     string `json:"to"`
			Amount *struct {
				Base  string `json:"base"`
				Denom string `json:"denom"`
			} `json:"amount"`
		}
		if err := w.callJSON(ctx, "get_transaction_status", map[string]any{"hash": b.TxHash}, &tx); err != nil || tx.Status != "confirmed" {
			continue
		}
		if tx.To != svc.PayTo || tx.Amount == nil || tx.Amount.Base != svc.Price.Base || tx.Amount.Denom != svc.Price.Denom {
			continue
		}
		if given != nil && tx.From != given.Address {
			continue
		}
		return b, true
	}
	return boughtCall{}, false
}
