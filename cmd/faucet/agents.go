package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Agent keys give a bot an identity of its own: its drips are counted as
// an agent's, under its name, and it gets its own quota (--agent-limit)
// instead of sharing its IP's. A bot makes an ed25519 key, registers the
// public half once with POST /agents {"name", "public_key"}, and signs
// each request:
//
//	X-Aether-Agent:           ak_...                 (from /agents)
//	X-Aether-Agent-Timestamp: unix seconds, within 5 minutes of now
//	X-Aether-Agent-Signature: base64(ed25519(timestamp + "\n" + body))
//
// Registration is open: an agent key proves continuity, not who runs it.

const (
	headerAgent          = "X-Aether-Agent"
	headerAgentTimestamp = "X-Aether-Agent-Timestamp"
	headerAgentSignature = "X-Aether-Agent-Signature"
	agentClockSkew       = 5 * time.Minute
	agentCallerPrefix    = "agent:"
)

var (
	agentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

	errAgentUnknown   = errors.New("unknown agent key; register it with POST /agents")
	errAgentHeaders   = errors.New("an agent request needs " + headerAgentTimestamp + " and " + headerAgentSignature)
	errAgentClock     = errors.New("agent timestamp is more than 5 minutes from the faucet's clock")
	errAgentSignature = errors.New("agent signature doesn't verify against the registered key")
)

type agentKey struct {
	ID         string    `json:"agent_id"`
	Name       string    `json:"name"`
	PublicKey  string    `json:"public_key"` // base64 ed25519
	Registered time.Time `json:"registered"`
}

type agentRegistry struct {
	mu   sync.Mutex
	path string // "" keeps registrations in memory only
	keys map[string]agentKey
	now  func() time.Time
}

func openAgentRegistry(path string) (*agentRegistry, error) {
	r := &agentRegistry{path: path, keys: map[string]agentKey{}, now: time.Now}
	if path == "" {
		return r, nil
	}
	bz, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var list []agentKey
	if err := json.Unmarshal(bz, &list); err != nil {
		return nil, err
	}
	for _, k := range list {
		r.keys[k.ID] = k
	}
	return r, nil
}

// agentID is "ak_" and the first 12 hex digits of sha256(public key).
func agentID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "ak_" + hex.EncodeToString(sum[:6])
}

// register adds pub under name, or returns its existing registration
// (keeping the first name) when it's already known.
func (r *agentRegistry) register(name, publicKeyB64 string) (agentKey, bool, error) {
	if !agentNamePattern.MatchString(name) {
		return agentKey{}, false, errors.New("name: 1 to 40 letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	pub, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return agentKey{}, false, errors.New("public_key: base64 of a 32-byte ed25519 public key")
	}
	id := agentID(pub)
	r.mu.Lock()
	defer r.mu.Unlock()
	if k, ok := r.keys[id]; ok {
		return k, false, nil
	}
	k := agentKey{ID: id, Name: name, PublicKey: publicKeyB64, Registered: r.now().UTC()}
	r.keys[id] = k
	if err := r.saveLocked(); err != nil {
		delete(r.keys, id)
		return agentKey{}, false, err
	}
	return k, true, nil
}

func (r *agentRegistry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	list := make([]agentKey, 0, len(r.keys))
	for _, k := range r.keys {
		list = append(list, k)
	}
	bz, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, bz, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// verify checks an agent-signed request. ok is false, with no error, for
// a request that doesn't claim to be an agent's.
func (r *agentRegistry) verify(h interface{ Get(string) string }, body []byte) (k agentKey, ok bool, err error) {
	id := h.Get(headerAgent)
	if id == "" {
		return agentKey{}, false, nil
	}
	r.mu.Lock()
	k, known := r.keys[id]
	r.mu.Unlock()
	if !known {
		return agentKey{}, true, errAgentUnknown
	}
	ts, sigB64 := h.Get(headerAgentTimestamp), h.Get(headerAgentSignature)
	if ts == "" || sigB64 == "" {
		return agentKey{}, true, errAgentHeaders
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return agentKey{}, true, errAgentHeaders
	}
	if d := r.now().Sub(time.Unix(secs, 0)); d > agentClockSkew || d < -agentClockSkew {
		return agentKey{}, true, errAgentClock
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	pub, _ := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil || !ed25519.Verify(pub, append([]byte(ts+"\n"), body...), sig) {
		return agentKey{}, true, errAgentSignature
	}
	return k, true, nil
}

// signAgentRequest sets the agent headers on h for body, for tests and Go
// clients.
func signAgentRequest(h interface{ Set(string, string) }, id string, priv ed25519.PrivateKey, body []byte, now time.Time) {
	ts := strconv.FormatInt(now.Unix(), 10)
	h.Set(headerAgent, id)
	h.Set(headerAgentTimestamp, ts)
	h.Set(headerAgentSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, append([]byte(ts+"\n"), body...))))
}
