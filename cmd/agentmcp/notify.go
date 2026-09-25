package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Owner alerts: with --notify-webhook, every payment, approval request
// and refusal is POSTed there as JSON, so the person behind the agent
// sees what it does with money. With --notify-secret, each body is
// signed: X-Aether-Signature is hex HMAC-SHA256(secret, body).
//
// Alerts are best-effort and never delay or block a payment.

var (
	notifyWebhook string
	notifySecret  string
	notifyClient  = &http.Client{Timeout: 5 * time.Second}
	notifyWG      sync.WaitGroup // lets tests wait for delivery
)

func notify(event string, fields map[string]any) {
	if notifyWebhook == "" {
		return
	}
	body := map[string]any{"event": event, "time": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range fields {
		body[k] = v
	}
	if w, err := newWallet(); err == nil {
		if acc, err := w.GetAccount(accountName); err == nil {
			body["agent"] = acc.Address
		}
	}
	bz, err := json.Marshal(body)
	if err != nil {
		return
	}
	notifyWG.Add(1)
	go func() {
		defer notifyWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, notifyWebhook, bytes.NewReader(bz))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if notifySecret != "" {
			m := hmac.New(sha256.New, []byte(notifySecret))
			m.Write(bz)
			req.Header.Set("X-Aether-Signature", hex.EncodeToString(m.Sum(nil)))
		}
		resp, err := notifyClient.Do(req)
		if err != nil {
			log.Printf("notify %s: %v", event, err)
			return
		}
		resp.Body.Close()
	}()
}
