package shopper

import (
	"html/template"
	"net"
	"net/http"
	"strings"

	"github.com/whoyoujoshin/aether/wallet"
)

// ApprovalPage serves one local approval. It is meant for 127.0.0.1 on
// the computer that holds the shopper, not for a public explorer.
// Wallet is nil when this process must not sign (the agent). shopper
// serve sets it to the approver key.
type ApprovalPage struct {
	Dir      string
	Wallet   *wallet.Wallet
	Approver string
	KeyName  string
}

var approvalTmpl = template.Must(template.New("a").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Allow this buy?</title>
<style>
body{font-family:system-ui,sans-serif;margin:0;background:#0e1116;color:#f4f1ea}
main{max-width:28rem;margin:0 auto;padding:1.25rem}
h1{font-size:1.4rem}
.price{font-size:1.6rem;font-weight:700}
button{font:inherit;padding:.8rem 1rem;border-radius:.6rem;border:0;width:100%;margin-top:.5rem}
.yes{background:#f4f1ea;color:#111}
.no{background:transparent;color:#f4f1ea;border:1px solid #888}
.note{color:#b7b1a6;font-size:.95rem}
</style></head><body><main>
<h1>Allow this buy?</h1>
<p>{{.Name}}</p>
<p class="price">{{.Price}}</p>
<p class="note">This stays on this computer. It does not raise your spending limit.</p>
{{if .CanSign}}
<form method="POST">
<button class="yes" name="decision" value="approve">Approve</button>
<button class="no" name="decision" value="reject">No</button>
</form>
{{else}}
<p class="note">To say yes, on this computer run shopper approve {{.ID}} with the approver key. This page cannot sign by itself.</p>
{{end}}
{{if .Done}}<p>{{.Done}}</p>{{end}}
</main></body></html>`))

type approvalView struct {
	Name, Price, ID, Done string
	CanSign               bool
}

// Handler is the local approval page. Paths are /approve/{id}.
func (p ApprovalPage) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /approve/{id}", p.show)
	mux.HandleFunc("POST /approve/{id}", p.decide)
	return mux
}

func (p ApprovalPage) show(w http.ResponseWriter, r *http.Request) {
	p.render(w, r, "")
}

func (p ApprovalPage) decide(w http.ResponseWriter, r *http.Request) {
	if p.Wallet == nil || p.KeyName == "" {
		http.Error(w, "This page cannot sign. Nothing was spent.", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	decision := r.FormValue("decision")
	if decision != "approve" && decision != "reject" {
		http.Error(w, "Choose Approve or No.", http.StatusBadRequest)
		return
	}
	if err := SignApproval(p.Dir, id, decision, p.Approver, p.Wallet, p.KeyName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if decision == "approve" {
		p.render(w, r, "Approved. You can close this page.")
		return
	}
	p.render(w, r, "Rejected. Nothing was spent.")
}

func (p ApprovalPage) render(w http.ResponseWriter, r *http.Request, done string) {
	a, err := loadApproval(p.Dir, r.PathValue("id"))
	if err != nil {
		http.Error(w, "That request is not on this computer.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = approvalTmpl.Execute(w, approvalView{
		Name: a.Name, Price: a.PriceDisplay, ID: a.ID, Done: done, CanSign: p.Wallet != nil && p.KeyName != "",
	})
}

// Loopback reports whether addr is a local listen address. A public
// host is refused: approvals are not served from the seed.
func Loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
