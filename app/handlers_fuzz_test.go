package app

import (
	"sort"
	"strings"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	"github.com/whoyoujoshin/aether/gates"
)

// aetherMsgTypes lists every message type Aether's own modules register
// (type URLs under /aether.), sorted so a fuzz input picks the same one
// on every run.
func aetherMsgTypes(t testing.TB, app *App) []string {
	t.Helper()
	var urls []string
	for _, url := range app.interfaceRegistry.ListImplementations(sdk.MsgInterfaceProtoName) {
		if strings.HasPrefix(url, "/aether.") {
			urls = append(urls, url)
		}
	}
	sort.Strings(urls)
	if len(urls) == 0 {
		t.Fatal("no aether message types registered")
	}
	return urls
}

// FuzzAetherMsgHandlers runs any bytes, decoded as one of Aether's own
// messages, through that message's handler on a fresh chain with every
// gate open, and fails on a panic. Inside a transaction the SDK turns a
// handler's panic into a failed transaction, but a message a passed
// governance proposal carries runs in EndBlock, where nothing does: a
// handler must refuse bad input with an error. Run it for longer with:
//
//	go test ./app -run '^$' -fuzz '^FuzzAetherMsgHandlers$' -fuzztime 10m
func FuzzAetherMsgHandlers(f *testing.F) {
	const chainID = gates.RehearsalPrefix + "fuzz"
	app := newChainApp(f, dbm.NewMemDB(), chainID)
	genesisOn(f, app, chainID)
	finalizeAndCommit(f, app, 1)
	finalizeAndCommit(f, app, 2)
	types := aetherMsgTypes(f, app)
	base := app.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 3, Time: testGenesisTime.Add(3 * time.Minute)})

	for i := range types {
		f.Add(uint8(i), []byte{})
	}

	f.Fuzz(func(t *testing.T, pick uint8, raw []byte) {
		url := types[int(pick)%len(types)]
		msg, err := app.interfaceRegistry.Resolve(url)
		if err != nil {
			t.Fatalf("resolve %s: %v", url, err)
		}
		if err := proto.Unmarshal(raw, msg); err != nil {
			return
		}
		handler := app.MsgServiceRouter().Handler(msg)
		if handler == nil {
			return
		}
		ctx, _ := base.CacheContext()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s handler panicked on %x: %v", url, raw, r)
			}
		}()
		_, _ = handler(ctx, msg)
	})
}
