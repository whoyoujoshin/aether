package wallet

import (
	"context"
	"net"
	"strings"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store/iavl"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/whoyoujoshin/aether/x/pow"
)

type seatWorkServer struct {
	pow.UnimplementedQueryServer
	res *pow.QuerySeatWorkResponse
	err error
}

func (s seatWorkServer) SeatWork(context.Context, *pow.QuerySeatWorkRequest) (*pow.QuerySeatWorkResponse, error) {
	return s.res, s.err
}

// storeServer answers ABCI store queries from an IAVL store, as a node does.
type storeServer struct {
	cmtservice.UnimplementedServiceServer
	store *iavl.Store
}

func (s storeServer) ABCIQuery(_ context.Context, req *cmtservice.ABCIQueryRequest) (*cmtservice.ABCIQueryResponse, error) {
	res, err := s.store.Query(&storetypes.RequestQuery{Path: strings.TrimPrefix(req.Path, "/store/"+pow.StoreKey), Data: req.Data})
	if err != nil {
		return nil, err
	}
	return &cmtservice.ABCIQueryResponse{Code: res.Code, Log: res.Log, Value: res.Value}, nil
}

func bufClient(t *testing.T, register func(*grpc.Server)) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{conn: conn}
}

// A node with the SeatWork query: the window, eligibility and bar come
// from it, with no store reads.
func TestSeatWindow_FromQuery(t *testing.T) {
	a, b := sdk.AccAddress("seat_status_miner_a_").String(), sdk.AccAddress("seat_status_miner_b_").String()
	c := bufClient(t, func(s *grpc.Server) {
		pow.RegisterQueryServer(s, &seatWorkServer{res: &pow.QuerySeatWorkResponse{
			Epoch: 330, WindowTotal: "1300", BarTotal: "1000",
			Entries: []*pow.SeatWorkInfo{
				{Address: b, WindowWork: "1000", EpochWork: "400", Eligible: true, MeetsBar: true},
				{Address: a, WindowWork: "300", EpochWork: "0", Eligible: false},
			},
		}})
	})
	w, err := c.seatWindow(context.Background(), 475_300, 330)
	require.NoError(t, err)
	require.True(t, w.fromQuery)
	require.Equal(t, []string{b, a}, w.order)
	require.Equal(t, "1300", w.total.String())
	require.Equal(t, "1000", w.bar.String())
	require.Equal(t, map[string]bool{a: false, b: true}, w.eligible)
	require.Equal(t, "400", w.current[b].String())
	_, ok := w.current[a]
	require.False(t, ok, "no work this epoch")

	// Any error but Unimplemented is the node's, not a reason to fall back.
	broken := bufClient(t, func(s *grpc.Server) {
		pow.RegisterQueryServer(s, &seatWorkServer{err: status.Error(codes.Internal, "boom")})
	})
	_, err = broken.seatWindow(context.Background(), 475_300, 330)
	require.ErrorContains(t, err, "boom")
}

// A node from before the query answers Unimplemented: the window is read
// from the store, eligibility left to the caller.
func TestSeatWindow_FallsBackToTheStore(t *testing.T) {
	st, err := iavl.LoadStore(dbm.NewMemDB(), log.NewNopLogger(), storetypes.NewKVStoreKey(pow.StoreKey), storetypes.CommitID{}, iavl.DefaultIAVLCacheSize, false, metrics.NewNoOpMetrics())
	require.NoError(t, err)
	a := sdk.AccAddress("seat_status_miner_a_")
	for epoch, n := range map[uint64]int64{328: 1_000, 330: 25, 327: 9} { // 327 is before the window
		bz, err := math.NewInt(n).Marshal()
		require.NoError(t, err)
		st.Set(append(append(append([]byte{}, pow.KeySeatWorkPrefix...), sdk.Uint64ToBigEndian(epoch)...), a...), bz)
	}
	st.Commit()

	c := bufClient(t, func(s *grpc.Server) {
		cmtservice.RegisterServiceServer(s, &storeServer{store: st.(*iavl.Store)})
	})
	w, err := c.seatWindow(context.Background(), 0, 330)
	require.NoError(t, err)
	require.False(t, w.fromQuery)
	require.Equal(t, []string{a.String()}, w.order)
	require.Equal(t, "1025", w.work[a.String()].String())
	require.Equal(t, "25", w.current[a.String()].String())
	require.Equal(t, "1025", w.total.String())
}
