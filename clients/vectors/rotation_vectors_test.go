package vectors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/crypto/mldsa"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/accountauth"
)

// clients/testdata/rotation.json: key rotation (docs/PQ-KEY-ROTATION.md)
// as the Go implementation produces it, byte for byte, for the TypeScript
// and Python clients: the signing bytes, every message's encoding, a
// signed MsgRotateKey transaction, a Rotation query answer, and the
// signs-for rule. Regenerate with go test ./clients/vectors -update-vectors.

// Standard BIP-39 test mnemonics, one per key.
var rotationKeys = []struct{ name, mnemonic string }{
	{"owner", mnemonic},
	{"next", "legal winner thank year wave sausage worth useful legal winner thank yellow"},
	{"guardian1", "letter advice cage absurd amount doctor acoustic avoid letter advice cage above"},
	{"guardian2", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong"},
	{"watchtower", "void come effort suffer camp survey warrior heavy shoot primary clutch crush open amazing screen patrol group space point ten exist slush involve unfold"},
}

const rotationChainID = "aether-rc-vectors"

type rotKeyVector struct {
	Name     string `json:"name"`
	Mnemonic string `json:"mnemonic"`
	Seed     string `json:"seed"`
	PubKey   string `json:"pubKey"`
	Address  string `json:"address"`
}

type rotMsgVector struct {
	Name    string          `json:"name"`
	TypeURL string          `json:"typeUrl"`
	Fields  json.RawMessage `json:"fields"` // the message as JSON, bytes as hex
	Bytes   string          `json:"bytes"`  // its protobuf encoding, hex
}

type signsForVector struct {
	Name     string `json:"name"`
	Account  string `json:"account"`
	PubKey   string `json:"pubKey"`
	Stored   string `json:"stored"` // the key stored on the account, "" for none
	SignsFor bool   `json:"signsFor"`
}

type rotationVectors struct {
	Note         string         `json:"note"`
	ChainID      string         `json:"chainId"`
	Keys         []rotKeyVector `json:"keys"`
	SigningBytes []struct {
		Name     string `json:"name"`
		Text     string `json:"text"`   // what's hashed
		SHA256   string `json:"sha256"` // the bytes that are signed
		SignedBy string `json:"signedBy"`
		Sig      string `json:"signature"`
	} `json:"signingBytes"`
	Messages    []rotMsgVector `json:"messages"`
	RotateKeyTx txVector       `json:"rotateKeyTx"`
	Query       struct {
		Request  string          `json:"request"`  // QueryRotationRequest, base64 as abci_query takes it
		Response string          `json:"response"` // QueryRotationResponse, base64 as the node returns it
		Expected json.RawMessage `json:"expected"`
	} `json:"rotationQuery"`
	SignsFor []signsForVector `json:"signsFor"`
}

func buildRotation(t *testing.T) rotationVectors {
	v := rotationVectors{
		Note:    "Key rotation (docs/PQ-KEY-ROTATION.md) as the Go implementation produces it. Signing bytes are the sha256 of the text, which is signed with ML-DSA-44 (deterministic, empty context). Messages are protobuf, bytes fields hex in 'fields'. Regenerate: go test ./clients/vectors -update-vectors.",
		ChainID: rotationChainID,
	}
	registry := codectypes.NewInterfaceRegistry()
	mldsa.RegisterInterfaces(registry)
	w, err := wallet.NewWallet("aetherd", "test", t.TempDir(), codec.NewProtoCodec(registry))
	require.NoError(t, err)
	keys := map[string]wallet.Account{}
	for _, k := range rotationKeys {
		acc, err := w.ImportAccount(k.name, k.mnemonic)
		require.NoError(t, err)
		seed, err := mldsa.Algo.Derive()(k.mnemonic, "", "")
		require.NoError(t, err)
		keys[k.name] = acc
		v.Keys = append(v.Keys, rotKeyVector{k.name, k.mnemonic, hex.EncodeToString(seed), hex.EncodeToString(acc.PubKey), acc.Address})
	}
	owner, next, g1, g2, wt := keys["owner"], keys["next"], keys["guardian1"], keys["guardian2"], keys["watchtower"]
	account := owner.Address

	sign := func(name, label, text string, bz []byte) []byte {
		sum := sha256.Sum256([]byte(text))
		require.Equal(t, sum[:], bz, label)
		sig, _, err := w.SignBytes(name, bz)
		require.NoError(t, err)
		v.SigningBytes = append(v.SigningBytes, struct {
			Name     string `json:"name"`
			Text     string `json:"text"`
			SHA256   string `json:"sha256"`
			SignedBy string `json:"signedBy"`
			Sig      string `json:"signature"`
		}{label, text, hex.EncodeToString(bz), name, hex.EncodeToString(sig)})
		return sig
	}
	const nonce, recoveryNonce = 3, 1
	pubHex := hex.EncodeToString(next.PubKey)
	rotatePop := sign("next", "rotate (new key's proof, nonce 3)",
		"aether-accountauth-rotate/v1\n"+rotationChainID+"\n"+account+"\n3\n"+pubHex+"\n",
		accountauth.RotateKeySigningBytes(rotationChainID, account, nonce, next.PubKey))
	recoverPop := sign("next", "recover (new key's proof, recovery nonce 1)",
		"aether-accountauth-rotate/v1\n"+rotationChainID+"\n"+account+"\n1\n"+pubHex+"\n",
		accountauth.RotateKeySigningBytes(rotationChainID, account, recoveryNonce, next.PubKey))
	recoverText := "aether-accountauth-recover/v1\n" + rotationChainID + "\n" + account + "\n1\n" + pubHex + "\n"
	recoverBytes := accountauth.RecoverKeySigningBytes(rotationChainID, account, recoveryNonce, next.PubKey)
	g1Recover := sign("guardian1", "recover (guardian 1, recovery nonce 1)", recoverText, recoverBytes)
	g2Recover := sign("guardian2", "recover (guardian 2, recovery nonce 1)", recoverText, recoverBytes)
	cancelText := "aether-accountauth-guardian-cancel/v1\n" + rotationChainID + "\n" + account + "\n1\n"
	g1Cancel := sign("guardian1", "guardian cancel (guardian 1, recovery nonce 1)", cancelText,
		accountauth.GuardianCancelSigningBytes(rotationChainID, account, recoveryNonce))

	watchSig := sign("watchtower", "watchtower cancel (nonce 3)",
		"aether-accountauth-watchtower-cancel/v1\n"+rotationChainID+"\n"+account+"\n3\n",
		accountauth.WatchtowerCancelSigningBytes(rotationChainID, account, nonce))
	newSet := accountauth.RecoverySet{GuardianPubkeys: [][]byte{g1.PubKey, g2.PubKey}, Threshold: 1, WatchtowerPubkeys: [][]byte{wt.PubKey}}
	g1SetRec := sign("guardian1", "guardian set recovery (guardian 1, recovery nonce 1)",
		"aether-accountauth-guardian-set-recovery/v1\n"+rotationChainID+"\n"+account+"\n1\n1\nguardians:"+
			hex.EncodeToString(g1.PubKey)+","+hex.EncodeToString(g2.PubKey)+"\nwatchtowers:"+hex.EncodeToString(wt.PubKey)+"\n",
		accountauth.GuardianSetRecoverySigningBytes(rotationChainID, account, recoveryNonce, newSet))
	sign("guardian1", "guardian set recovery, removing the set (guardian 1, recovery nonce 1)",
		"aether-accountauth-guardian-set-recovery/v1\n"+rotationChainID+"\n"+account+"\n1\n0\nguardians:\nwatchtowers:\n",
		accountauth.GuardianSetRecoverySigningBytes(rotationChainID, account, recoveryNonce, accountauth.RecoverySet{}))

	// The wallet's builders produce the same proofs.
	m, err := w.RotateKeyMsg(rotationChainID, account, "next", nonce)
	require.NoError(t, err)
	require.Equal(t, rotatePop, m.NewKeySignature)

	addMsg := func(name string, msg proto.Message, fields any) {
		bz, err := proto.Marshal(msg)
		require.NoError(t, err)
		fj, err := json.Marshal(fields)
		require.NoError(t, err)
		v.Messages = append(v.Messages, rotMsgVector{name, sdk.MsgTypeURL(msg.(sdk.Msg)), fj, hex.EncodeToString(bz)})
	}
	type gsig struct {
		Pubkey    string `json:"pubkey"`
		Signature string `json:"signature"`
	}
	addMsg("MsgRotateKey", m, map[string]any{"account": account, "newPubkey": pubHex, "newKeySignature": hex.EncodeToString(rotatePop)})
	addMsg("MsgCancelPendingRotation", &accountauth.MsgCancelPendingRotation{Account: account}, map[string]any{"account": account})
	setRec := &accountauth.MsgSetRecovery{Account: account, GuardianPubkeys: [][]byte{g1.PubKey, g2.PubKey}, Threshold: 2}
	addMsg("MsgSetRecovery", setRec, map[string]any{"account": account, "guardianPubkeys": []string{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g2.PubKey)}, "threshold": 2})
	withWatch := &accountauth.MsgSetRecovery{Account: account, GuardianPubkeys: [][]byte{g1.PubKey, g2.PubKey}, Threshold: 2, WatchtowerPubkeys: [][]byte{wt.PubKey}}
	addMsg("MsgSetRecovery (with a watchtower)", withWatch, map[string]any{"account": account, "guardianPubkeys": []string{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g2.PubKey)}, "threshold": 2,
		"watchtowerPubkeys": []string{hex.EncodeToString(wt.PubKey)}})
	removeRec := &accountauth.MsgSetRecovery{Account: account}
	addMsg("MsgSetRecovery (remove)", removeRec, map[string]any{"account": account, "guardianPubkeys": []string{}, "threshold": 0})
	sigs := []*accountauth.GuardianSignature{{Pubkey: g1.PubKey, Signature: g1Recover}, {Pubkey: g2.PubKey, Signature: g2Recover}}
	rec, err := w.RecoverKeyMsg(rotationChainID, g1.Address, account, "next", recoveryNonce, sigs)
	require.NoError(t, err)
	require.Equal(t, recoverPop, rec.NewKeySignature)
	addMsg("MsgRecoverKey", rec, map[string]any{"submitter": g1.Address, "account": account, "newPubkey": pubHex, "newKeySignature": hex.EncodeToString(recoverPop),
		"guardianSignatures": []gsig{{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g1Recover)}, {hex.EncodeToString(g2.PubKey), hex.EncodeToString(g2Recover)}}})
	cancel := &accountauth.MsgGuardianCancel{Submitter: g1.Address, Account: account, GuardianSignatures: []*accountauth.GuardianSignature{{Pubkey: g1.PubKey, Signature: g1Cancel}}}
	addMsg("MsgGuardianCancel", cancel, map[string]any{"submitter": g1.Address, "account": account,
		"guardianSignatures": []gsig{{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g1Cancel)}}})

	gsr := &accountauth.MsgGuardianSetRecovery{Submitter: g1.Address, Account: account, GuardianPubkeys: newSet.GuardianPubkeys, Threshold: 1,
		WatchtowerPubkeys: newSet.WatchtowerPubkeys, GuardianSignatures: []*accountauth.GuardianSignature{{Pubkey: g1.PubKey, Signature: g1SetRec}}}
	addMsg("MsgGuardianSetRecovery", gsr, map[string]any{"submitter": g1.Address, "account": account,
		"guardianPubkeys": []string{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g2.PubKey)}, "threshold": 1,
		"watchtowerPubkeys":  []string{hex.EncodeToString(wt.PubKey)},
		"guardianSignatures": []gsig{{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g1SetRec)}}})
	addMsg("MsgWatchtowerCancel", &accountauth.MsgWatchtowerCancel{Submitter: g2.Address, Account: account, WatchtowerPubkey: wt.PubKey, Signature: watchSig},
		map[string]any{"submitter": g2.Address, "account": account, "watchtowerPubkey": hex.EncodeToString(wt.PubKey), "signature": hex.EncodeToString(watchSig)})

	// MsgRotateKey in a transaction the owner signs, as the clients would
	// send it.
	params := wallet.TxParams{ChainID: rotationChainID, AccountNumber: 7, Sequence: 3, GasLimit: 400_000, Fees: sdk.NewCoins(sdk.NewCoin("uaeth", math.ZeroInt()))}
	signed, err := w.BuildAndSignMsgTx("owner", m, params)
	require.NoError(t, err)
	var raw txtypes.TxRaw
	require.NoError(t, raw.Unmarshal(signed.Bytes))
	doc := txtypes.SignDoc{BodyBytes: raw.BodyBytes, AuthInfoBytes: raw.AuthInfoBytes, ChainId: rotationChainID, AccountNumber: 7}
	docBytes, err := doc.Marshal()
	require.NoError(t, err)
	v.RotateKeyTx = txVector{ChainID: rotationChainID, AccountNumber: 7, Sequence: 3, From: account, GasLimit: 400_000,
		BodyBytes: hex.EncodeToString(raw.BodyBytes), AuthInfoBytes: hex.EncodeToString(raw.AuthInfoBytes), SignDoc: hex.EncodeToString(docBytes),
		Signature: hex.EncodeToString(raw.Signatures[0]), TxBytes: hex.EncodeToString(signed.Bytes), TxHash: wallet.TxHash(signed)}

	// A Rotation query answer: a recovery set, a pending rotation.
	req, err := (&accountauth.QueryRotationRequest{Account: account}).Marshal()
	require.NoError(t, err)
	state := &accountauth.RotationState{Nonce: 4, RecoveryNonce: 1,
		Recovery:        &accountauth.RecoverySet{GuardianPubkeys: [][]byte{g1.PubKey, g2.PubKey}, Threshold: 2, WatchtowerPubkeys: [][]byte{wt.PubKey}},
		PendingRotation: &accountauth.PendingRotation{NewPubkey: next.PubKey, ApplyHeight: 15_400}}
	resp, err := (&accountauth.QueryRotationResponse{State: state, CurrentPubkey: owner.PubKey, Active: true, DelayBlocks: accountauth.RotationDelayBlocks}).Marshal()
	require.NoError(t, err)
	v.Query.Request, v.Query.Response = base64.StdEncoding.EncodeToString(req), base64.StdEncoding.EncodeToString(resp)
	v.Query.Expected, _ = json.Marshal(map[string]any{
		"nonce": 4, "recoveryNonce": 1, "recoveryThreshold": 2,
		"recoveryGuardians":   []string{hex.EncodeToString(g1.PubKey), hex.EncodeToString(g2.PubKey)},
		"recoveryWatchtowers": []string{hex.EncodeToString(wt.PubKey)},
		"pendingRotation":     map[string]any{"newPubkey": pubHex, "applyHeight": 15_400},
		"pendingRecovery":     nil, "currentPubkey": hex.EncodeToString(owner.PubKey), "active": true, "delayBlocks": accountauth.RotationDelayBlocks,
	})

	// The signs-for rule, checked here against wallet.SignsFor.
	for _, c := range []signsForVector{
		{"never rotated, no key stored: its own key", account, hex.EncodeToString(owner.PubKey), "", true},
		{"never rotated, no key stored: another key", account, hex.EncodeToString(next.PubKey), "", false},
		{"its own key stored", account, hex.EncodeToString(owner.PubKey), hex.EncodeToString(owner.PubKey), true},
		{"rotated: the new key", account, hex.EncodeToString(next.PubKey), hex.EncodeToString(next.PubKey), true},
		{"rotated: the retired key, whose hash is still the address", account, hex.EncodeToString(owner.PubKey), hex.EncodeToString(next.PubKey), false},
		{"rotated: an unrelated key", account, hex.EncodeToString(g1.PubKey), hex.EncodeToString(next.PubKey), false},
		{"not an ML-DSA-44 key", account, hex.EncodeToString(owner.PubKey[:32]), "", false},
	} {
		pub, _ := hex.DecodeString(c.PubKey)
		stored, _ := hex.DecodeString(c.Stored)
		got, err := wallet.SignsFor(context.Background(), storedKey{c.Account: stored}, c.Account, pub)
		require.NoError(t, err)
		require.Equal(t, c.SignsFor, got, c.Name)
		v.SignsFor = append(v.SignsFor, c)
	}
	return v
}

type storedKey map[string][]byte

func (s storedKey) AccountPubKey(_ context.Context, a string) ([]byte, error) {
	if len(s[a]) == 0 {
		return nil, nil
	}
	return s[a], nil
}

func TestRotationVectors(t *testing.T) {
	v := buildRotation(t)
	bz, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	path := filepath.Join("..", "testdata", "rotation.json")
	if *update {
		require.NoError(t, os.WriteFile(path, append(bz, '\n'), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run: go test ./clients/vectors -update-vectors")
	require.JSONEq(t, string(want), string(bz), "the Go implementation no longer produces the rotation vectors the clients are tested against")
}
