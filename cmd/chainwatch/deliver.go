package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// SignatureHeader is hex HMAC-SHA256(secret, body), the same scheme
// minerwatch and agentmcp's owner alerts use, so one receiver checks all
// of them.
const SignatureHeader = "X-Aether-Signature"

type deliverer struct {
	webhook string
	discord string
	secret  string
	stdout  io.Writer
	client  *http.Client
	backoff []time.Duration
	now     func() time.Time
}

func (d *deliverer) deliver(ctx context.Context, e event) {
	body := e.body(d.now().UTC().Format(time.RFC3339))
	bz, err := json.Marshal(body)
	if err != nil {
		log.Printf("encode %s: %v", e.Name, err)
		return
	}
	if d.stdout != nil {
		fmt.Fprintf(d.stdout, "%s\n", bz)
	}
	if d.webhook != "" {
		d.postJSON(ctx, d.webhook, e.Name, bz, true)
	}
	if d.discord != "" {
		text, _ := body["message"].(string)
		if text == "" {
			text = e.Name
		}
		wrapped, err := json.Marshal(map[string]any{
			"content":          truncate(text, 1900),
			"allowed_mentions": map[string]any{"parse": []string{}},
		})
		if err != nil {
			log.Printf("encode discord %s: %v", e.Name, err)
			return
		}
		d.postJSON(ctx, d.discord, e.Name, wrapped, false)
	}
}

func (d *deliverer) postJSON(ctx context.Context, endpoint, name string, bz []byte, sign bool) {
	for attempt := 0; ; attempt++ {
		err := d.post(ctx, endpoint, name, bz, sign)
		if err == nil {
			return
		}
		if attempt >= len(d.backoff) {
			log.Printf("alert %s: giving up: %v", name, err)
			return
		}
		log.Printf("alert %s: %v; retrying in %s", name, err, d.backoff[attempt])
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.backoff[attempt]):
		}
	}
}

func (d *deliverer) post(ctx context.Context, endpoint, name string, bz []byte, sign bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bz))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Aether-Event", name)
	if sign && d.secret != "" {
		req.Header.Set(SignatureHeader, signBody(d.secret, bz))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func signBody(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
