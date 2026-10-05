// cmd/outbound is the other half of Helicase: an unattended relayer that
// takes everything Aether sends onto another chain -- packets, the
// acknowledgements Aether wrote for that chain's packets, and timeouts
// for that chain's packets Aether never received -- keeping that chain's
// client of Aether fresh as it goes.
//
// It signs only on the other chain, with an ordinary key there (secp256k1
// on Noble or Osmosis); it reads Aether and never signs on it. Helicase,
// run by Aether's own validators, relays the opposite direction. Between
// them no relayer needs an Aether key once a path is open.
//
// It keeps no state of its own: every cycle it reads what's pending from
// both chains, so a restart or a second instance just picks up where
// things stand (a packet relayed twice is a no-op on the other chain).
// GET /healthz answers 200 while cycles succeed and the client isn't
// near expiry, 503 otherwise, with the details as JSON.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/whoyoujoshin/aether/app"
	"github.com/whoyoujoshin/aether/counterparty"
	"github.com/whoyoujoshin/aether/relayer"
)

func main() {
	var (
		aetherRPC = flag.String("aether-rpc", "http://127.0.0.1:26657", "Aether RPC address (must index transactions)")

		cpartyRPC     = flag.String("cparty-rpc", "http://127.0.0.1:26557", "the other chain's RPC address (must index transactions)")
		cpartyGRPC    = flag.String("cparty-grpc", "127.0.0.1:9080", "the other chain's gRPC address, for account lookups and gas simulation")
		cpartyChainID = flag.String("cparty-chain-id", "counterparty", "the other chain's chain ID")
		cpartyHome    = flag.String("cparty-home", os.ExpandEnv("$HOME/.counterparty"), "keyring root dir for the other chain's key")
		cpartyKey     = flag.String("cparty-key", "relayer", "the key that signs on the other chain")
		cpartyBech32  = flag.String("cparty-bech32-prefix", counterparty.Bech32Prefix, "the other chain's address prefix, e.g. noble or osmo")
		cpartyGas     = flag.String("cparty-gas-prices", "", "gas prices on the other chain, e.g. 0.025uosmo; empty pays no fee (Noble waives it for relaying)")
		gasAdjustment = flag.Float64("gas-adjustment", 1.5, "multiplier on simulated gas")
		keyringBack   = flag.String("keyring-backend", "test", "keyring backend (test/file/os)")

		clientID = flag.String("client-id", "07-tendermint-0", "the other chain's client of Aether")
		interval = flag.Duration("interval", 6*time.Second, "how often to look for work")
		refresh  = flag.Duration("refresh-after", time.Hour, "update the other chain's client of Aether at least this often, even with no packets: a transfer to Aether with a relative timeout (1,000 Aether blocks by default, ~1.5 h) counts from that client's latest height")
		maxMsgs  = flag.Int("max-msgs", 20, "most packets, acknowledgements and timeouts per transaction")
		expiryAt = flag.Duration("expiry-warning", 24*time.Hour, "report unhealthy when the client expires sooner than this")
		listen   = flag.String("listen", "127.0.0.1:8095", "health endpoint address; empty disables it")
		once     = flag.Bool("once", false, "run one cycle and exit")
	)
	flag.Parse()

	aetherEnc := app.MakeEncodingConfig()
	aether, err := relayer.NewReadOnlyChain("aether", *aetherRPC, "", aetherEnc.Codec, aetherEnc.TxConfig)
	if err != nil {
		log.Fatal(err)
	}
	cpartyEnc := counterparty.MakeEncodingConfig(*cpartyBech32)
	kr, err := keyring.New("counterpartyd", *keyringBack, *cpartyHome, os.Stdin, cpartyEnc.Codec)
	if err != nil {
		log.Fatalf("opening keyring: %v", err)
	}
	cparty, err := relayer.NewChain(*cpartyChainID, *cpartyRPC, *cpartyGRPC, *cpartyChainID, *cpartyBech32,
		cpartyEnc.Codec, cpartyEnc.TxConfig, kr, *cpartyKey, *cpartyGas)
	if err != nil {
		log.Fatal(err)
	}
	cparty.Factory = cparty.Factory.WithSimulateAndExecute(true).WithGasAdjustment(*gasAdjustment)
	log.Printf("relaying aether -> %s over %s as %s", *cpartyChainID, *clientID, cparty.FromAddrStr)

	st := &status{}
	if *listen != "" && !*once {
		go serveHealth(*listen, st, *interval, *expiryAt)
	}
	for {
		cycle(aether, cparty, *clientID, *maxMsgs, *refresh, st)
		if *once {
			if st.snapshot().LastError != "" {
				os.Exit(1)
			}
			return
		}
		time.Sleep(*interval)
	}
}

// cycle relays what's pending once: up to maxMsgs packets,
// acknowledgements and timeouts in one transaction, behind a client
// update.
func cycle(aether, cparty *relayer.Chain, clientID string, maxMsgs int, refreshAfter time.Duration, st *status) {
	plan, err := relayer.Plan(aether, cparty, clientID, cparty.FromAddrStr, maxMsgs, refreshAfter)
	if err == nil && plan.Any() {
		if _, err = cparty.SignAndBroadcast(plan.Msgs...); err == nil {
			log.Printf("relayed onto %s at proof height %s: %d packets, %d acknowledgements, %d timeouts (client update: %v)",
				cparty.ChainID, plan.ProofHeight, plan.Packets, plan.Acks, plan.Timeouts, plan.Update)
		}
	}
	expiry, expErr := relayer.ClientExpiry(cparty, clientID)
	if err == nil {
		err = expErr
	}
	st.record(plan, expiry, err)
}

type counts struct {
	Updates   int `json:"updates"`
	Packets   int `json:"packets"`
	Acks      int `json:"acknowledgements"`
	Timeouts  int `json:"timeouts"`
	Successes int `json:"cycles_ok"`
	Failures  int `json:"cycles_failed"`
}

type health struct {
	OK            bool      `json:"ok"`
	LastSuccess   time.Time `json:"last_success,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	ClientExpires time.Time `json:"client_expires,omitempty"`
	Relayed       counts    `json:"relayed"`
}

type status struct {
	mu sync.Mutex
	h  health
}

func (s *status) record(plan *relayer.PlanResult, expiry time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !expiry.IsZero() {
		s.h.ClientExpires = expiry
	}
	if err != nil {
		if err.Error() != s.h.LastError {
			log.Printf("cycle failed: %v", err)
		}
		s.h.LastError = err.Error()
		s.h.Relayed.Failures++
		return
	}
	s.h.LastError = ""
	s.h.LastSuccess = time.Now()
	s.h.Relayed.Successes++
	if plan != nil {
		if plan.Update {
			s.h.Relayed.Updates++
		}
		s.h.Relayed.Packets += plan.Packets
		s.h.Relayed.Acks += plan.Acks
		s.h.Relayed.Timeouts += plan.Timeouts
	}
}

func (s *status) snapshot() health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.h
}

// assess decides health: a recent successful cycle, and a client not
// about to expire.
func assess(h health, now time.Time, interval, expiryWarning time.Duration) (health, error) {
	switch {
	case h.LastSuccess.IsZero():
		return h, errors.New("no successful cycle yet")
	case now.Sub(h.LastSuccess) > 5*interval:
		return h, errors.New("no successful cycle recently")
	case !h.ClientExpires.IsZero() && h.ClientExpires.Sub(now) < expiryWarning:
		return h, errors.New("client expires soon")
	}
	h.OK = true
	return h, nil
}

func serveHealth(addr string, st *status, interval, expiryWarning time.Duration) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		h, err := assess(st.snapshot(), time.Now(), interval, expiryWarning)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			if h.LastError == "" {
				h.LastError = err.Error()
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	log.Printf("health on http://%s/healthz", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
