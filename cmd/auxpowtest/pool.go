package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// --auxpowd mode: act as a merge-mining pool against cmd/auxpowd, through
// its JSON-RPC alone, the way pool software does. Each round asks for work
// paid to --reward-address (createauxblock), commits to it in a parent
// coinbase, mines a Litecoin-style parent header whose scrypt hash, read
// little-endian, is below the work's _target, and submits the standard
// CAuxPow serialization (submitauxblock). It shares no code with the
// bridge, so it checks the bridge's encodings independently.

type auxBlock struct {
	Hash   string `json:"hash"`
	Height int64  `json:"height"`
	Target string `json:"_target"`
	Value  int64  `json:"coinbasevalue"`
}

func callAuxpowd(url, user, pass, method string, out any, params ...string) error {
	ps := make([]any, len(params))
	for i, p := range params {
		ps[i] = p
	}
	body, err := json.Marshal(map[string]any{"id": 1, "method": method, "params": ps})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, pass)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s: HTTP %d: %w", method, resp.StatusCode, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: error %d: %s", method, r.Error.Code, r.Error.Message)
	}
	return json.Unmarshal(r.Result, out)
}

func runPool(url, user, pass, reward string, rounds int, wait time.Duration) error {
	accepted := 0
	for round := 1; round <= rounds; round++ {
		var ab auxBlock
		if err := callAuxpowd(url, user, pass, "createauxblock", &ab, reward); err != nil {
			return err
		}
		committed, err := hex.DecodeString(ab.Hash) // display order: the bytes a pool puts in its coinbase
		if err != nil || len(committed) != 32 {
			return fmt.Errorf("createauxblock hash %q isn't 32 bytes of hex", ab.Hash)
		}
		targetLE, err := hex.DecodeString(ab.Target)
		if err != nil {
			return fmt.Errorf("createauxblock _target: %w", err)
		}
		target := new(big.Int).SetBytes(reverse(targetLE))

		chainNonce := uint32(time.Now().UnixNano())
		scriptSig := buildScriptSig([]byte(fmt.Sprintf("/auxpowtest round %d/", round)), committed, 1, chainNonce)
		coinbase := buildCoinbaseTx(scriptSig)
		prev := make([]byte, 32)
		binary.LittleEndian.PutUint64(prev, uint64(time.Now().UnixNano()))

		start := time.Now()
		var header []byte
		for nonce := uint32(0); ; nonce++ {
			candidate := buildParentHeader(0x20000000, uint32(time.Now().Unix()), 0x1e0ffff0, nonce, prev, doubleSHA256(coinbase))
			h, err := scryptHash(candidate)
			if err != nil {
				return err
			}
			if new(big.Int).SetBytes(reverse(h)).Cmp(target) < 0 {
				header = candidate
				break
			}
		}

		var proof []byte
		proof = append(proof, coinbase...)
		proof = append(proof, make([]byte, 32)...) // parent block hash: unused by the chain
		proof = append(proof, 0)                   // coinbase branch: the coinbase is the parent's only tx
		proof = append(proof, 0, 0, 0, 0)          // coinbase index
		proof = append(proof, 0)                   // chain branch: Aether is the only merged chain
		proof = append(proof, 0, 0, 0, 0)          // chain index
		proof = append(proof, header...)

		var ok bool
		if err := callAuxpowd(url, user, pass, "submitauxblock", &ok, ab.Hash, hex.EncodeToString(proof)); err != nil {
			return err
		}
		fmt.Printf("round %d: work for height %d (%d uaeth), parent mined in %s, submitted: %v\n",
			round, ab.Height, ab.Value, time.Since(start).Round(time.Millisecond), ok)
		if ok {
			accepted++
		}
		if round < rounds {
			time.Sleep(wait)
		}
	}
	fmt.Printf("%d of %d proofs taken by auxpowd\n", accepted, rounds)
	return nil
}
