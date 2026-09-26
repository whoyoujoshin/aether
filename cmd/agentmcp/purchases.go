package main

import (
	"context"
	"net/url"
	"time"

	"cosmossdk.io/math"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/paywall"
)

// Every paid response fetch_paid returns is logged with its receipt (if
// the seller signs them), checked against exactly what this agent sent
// and received: the owner's record of what the agent bought, and the
// agent's own history with each service.

const maxPurchases = 1000

type purchaseRecord struct {
	Service  string    `json:"service"` // scheme://host
	URL      string    `json:"url"`
	Method   string    `json:"method"`
	PayTo    string    `json:"payTo"`
	Scheme   string    `json:"scheme"`
	Payment  string    `json:"payment"` // tx hash or prepaid request ID
	Amount   string    `json:"amountUaeth"`
	Status   int       `json:"httpStatus"`
	At       time.Time `json:"at"`
	Receipt  string    `json:"receipt,omitempty"` // the X-PAYMENT-RECEIPT header, as received
	Verified bool      `json:"verified"`
	Problem  string    `json:"problem,omitempty"`
}

type receiptDTO struct {
	Verified bool             `json:"verified" jsonschema:"true: signed for the seller's account and matching exactly what this agent sent and received"`
	Problem  string           `json:"problem,omitempty" jsonschema:"why it didn't verify: keep the response, but tell your owner"`
	Receipt  *paywall.Receipt `json:"receipt"`
}

// purchase is what fetch_paid knows about one paid response.
type purchase struct {
	u                            *url.URL
	method, payTo, scheme, payer string
	payment                      string
	amount                       math.Int
	reqBody                      []byte
	res                          *httpResult
}

// recordPurchase checks the response's receipt, logs the purchase and
// returns the receipt for the tool's output (nil if the seller sent none).
func recordPurchase(p purchase) *receiptDTO {
	rec := purchaseRecord{
		Service: serviceBase(p.u), URL: p.u.String(), Method: p.method, PayTo: p.payTo, Scheme: p.scheme,
		Payment: p.payment, Amount: p.amount.String(), Status: p.res.status, At: time.Now().UTC(),
	}
	var out *receiptDTO
	if h := p.res.header.Get(paywall.HeaderReceipt); h != "" {
		rec.Receipt = h
		out = &receiptDTO{}
		r, err := paywall.DecodeReceipt(h)
		switch {
		case err != nil:
			out.Problem = "the receipt is unreadable"
		case p.res.truncated:
			out.Receipt = r
			out.Problem = "the response was too long to keep whole, so its hash can't be checked (everything else was)"
			if err := paywall.CheckReceipt(r, expectation(p, true)); err != nil {
				out.Problem = err.Error()
			}
		default:
			out.Receipt = r
			if err := paywall.CheckReceipt(r, expectation(p, false)); err != nil {
				out.Problem = err.Error()
			} else {
				out.Verified = true
			}
		}
		rec.Verified, rec.Problem = out.Verified, out.Problem
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if st, err := loadState(); err == nil {
		st.Purchases = append(st.Purchases, rec)
		if len(st.Purchases) > maxPurchases {
			st.Purchases = st.Purchases[len(st.Purchases)-maxPurchases:]
		}
		_ = st.save()
	}
	return out
}

func expectation(p purchase, skipResponse bool) paywall.ReceiptExpectation {
	path := p.u.Path
	if path == "" {
		path = "/"
	}
	e := paywall.ReceiptExpectation{
		Network: chainID, PayTo: p.payTo, Payer: p.payer, Scheme: p.scheme, Payment: p.payment, Amount: p.amount.String(),
		Method: p.method, Host: p.u.Host, Path: path, RequestBody: p.reqBody, ResponseBody: p.res.body, Status: p.res.status,
	}
	e.ResponseUnknown = skipResponse
	return e
}

type listPurchasesInput struct {
	Service string `json:"service,omitempty" jsonschema:"only purchases from this service (its URL); default all"`
	Limit   int    `json:"limit,omitempty" jsonschema:"most recent N (default 50, max 1000)"`
}

type purchaseDTO struct {
	Service  string    `json:"service"`
	URL      string    `json:"url"`
	Method   string    `json:"method"`
	PayTo    string    `json:"payTo"`
	Scheme   string    `json:"scheme"`
	Payment  string    `json:"payment" jsonschema:"the payment's transaction hash (aether-memo) or request ID (aether-prepaid)"`
	Amount   amountDTO `json:"amount"`
	Status   int       `json:"httpStatus"`
	At       time.Time `json:"at"`
	Receipt  string    `json:"receipt,omitempty" jsonschema:"the seller's signed receipt (base64 JSON): proof of what was bought, checkable by anyone against the seller's address"`
	Verified bool      `json:"receiptVerified"`
	Problem  string    `json:"receiptProblem,omitempty"`
}

type listPurchasesOutput struct {
	Purchases []purchaseDTO `json:"purchases" jsonschema:"newest first"`
}

func toolListPurchases(_ context.Context, _ *mcp.CallToolRequest, in listPurchasesInput) (*mcp.CallToolResult, listPurchasesOutput, error) {
	service := ""
	if in.Service != "" {
		u, err := url.Parse(in.Service)
		if err != nil || u.Host == "" {
			return nil, listPurchasesOutput{}, newError(codeInvalidArgument, "service must be the service's URL")
		}
		service = serviceBase(u)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	stateMu.Lock()
	st, err := loadState()
	stateMu.Unlock()
	if err != nil {
		return nil, listPurchasesOutput{}, err
	}
	out := listPurchasesOutput{Purchases: []purchaseDTO{}}
	for i := len(st.Purchases) - 1; i >= 0 && len(out.Purchases) < limit; i-- {
		p := st.Purchases[i]
		if service != "" && p.Service != service {
			continue
		}
		amt, _ := math.NewIntFromString(p.Amount)
		out.Purchases = append(out.Purchases, purchaseDTO{
			Service: p.Service, URL: p.URL, Method: p.Method, PayTo: p.PayTo, Scheme: p.Scheme, Payment: p.Payment,
			Amount: newAmountDTO(amt), Status: p.Status, At: p.At, Receipt: p.Receipt, Verified: p.Verified, Problem: p.Problem,
		})
	}
	return nil, out, nil
}
