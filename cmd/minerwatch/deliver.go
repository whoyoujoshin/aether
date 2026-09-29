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

// SignatureHeader carries hex HMAC-SHA256(secret, body), the same
// scheme agentmcp's owner alerts use, so one receiver checks both.
const SignatureHeader = "X-Aether-Signature"

type deliverer struct {
	webhook string
	secret  string
	stdout  io.Writer // one JSON object per line; nil: none
	client  *http.Client
	backoff []time.Duration // waits between webhook attempts
	now     func() time.Time
}

func (d *deliverer) deliver(ctx context.Context, e Event) {
	bz, err := json.Marshal(e.Body(d.now().UTC().Format(time.RFC3339)))
	if err != nil {
		log.Printf("encode %s: %v", e.Name, err)
		return
	}
	if d.stdout != nil {
		fmt.Fprintf(d.stdout, "%s\n", bz)
	}
	if d.webhook == "" {
		return
	}
	for attempt := 0; ; attempt++ {
		err := d.post(ctx, e.Name, bz)
		if err == nil {
			return
		}
		if attempt >= len(d.backoff) {
			log.Printf("webhook %s (%s): giving up: %v", e.Name, e.ID(), err)
			return
		}
		log.Printf("webhook %s (%s): %v; retrying in %s", e.Name, e.ID(), err, d.backoff[attempt])
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.backoff[attempt]):
		}
	}
}

func (d *deliverer) post(ctx context.Context, name string, bz []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.webhook, bytes.NewReader(bz))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Aether-Event", name)
	if d.secret != "" {
		req.Header.Set(SignatureHeader, sign(d.secret, bz))
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

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
