package pow

import (
	"bytes"
	"context"
	"sort"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/whoyoujoshin/aether/gates"
)

type queryServer struct {
	Keeper
}

func NewQueryServerImpl(keeper Keeper) QueryServer {
	return &queryServer{Keeper: keeper}
}

func (q queryServer) Difficulty(goCtx context.Context, req *QueryDifficultyRequest) (*QueryDifficultyResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	resp := &QueryDifficultyResponse{
		Difficulty: q.Keeper.GetDifficulty(ctx).String(),
	}
	if gates.On(ctx, MergedMiningActivationHeight) {
		resp.AuxDifficulty = q.Keeper.GetAuxDifficulty(ctx).String()
	}
	return resp, nil
}

func (q queryServer) BlockReward(goCtx context.Context, req *QueryBlockRewardRequest) (*QueryBlockRewardResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryBlockRewardResponse{
		BlockReward: q.Keeper.GetBlockReward(ctx).String(),
	}, nil
}

func (q queryServer) Escrow(goCtx context.Context, req *QueryEscrowRequest) (*QueryEscrowResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(req.Miner)
	if err != nil {
		return nil, err
	}

	balance := q.Keeper.GetEscrowBalance(ctx, minerAddr)
	unlockHeight, hasPending := q.Keeper.GetEscrowUnlockHeight(ctx, minerAddr)

	return &QueryEscrowResponse{
		Balance:          balance.String(),
		UnlockHeight:     unlockHeight,
		HasPendingEscrow: hasPending,
	}, nil
}

func (q queryServer) BanStatus(goCtx context.Context, req *QueryBanStatusRequest) (*QueryBanStatusResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	minerAddr, err := sdk.AccAddressFromBech32(req.Miner)
	if err != nil {
		return nil, err
	}

	return &QueryBanStatusResponse{
		Banned: q.Keeper.IsBanned(ctx, minerAddr),
	}, nil
}

func (q queryServer) ActiveValidators(goCtx context.Context, req *QueryActiveValidatorsRequest) (*QueryActiveValidatorsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	addrs := q.Keeper.IterateActiveValidators(ctx)
	validators := make([]string, len(addrs))
	for i, addr := range addrs {
		validators[i] = addr.String()
	}

	return &QueryActiveValidatorsResponse{
		Validators: validators,
	}, nil
}

func (q queryServer) CurrentEpoch(goCtx context.Context, req *QueryCurrentEpochRequest) (*QueryCurrentEpochResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryCurrentEpochResponse{
		Epoch: q.Keeper.CurrentEpoch(ctx),
	}, nil
}

// Params reports every x/pow parameter that has real, live keeper
// storage -- see QueryParamsResponse's proto comment for exactly which
// genesis Params fields are excluded, and why (no live storage to
// report).
func (q queryServer) Params(goCtx context.Context, req *QueryParamsRequest) (*QueryParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	return &QueryParamsResponse{
		TargetBlockTime:      q.Keeper.GetTargetBlockTime(ctx),
		MinDifficulty:        q.Keeper.GetMinDifficulty(ctx).String(),
		MaxDifficulty:        q.Keeper.GetMaxDifficulty(ctx).String(),
		Difficulty:           q.Keeper.GetDifficulty(ctx).String(),
		EpochLength:          q.Keeper.GetEpochLength(ctx),
		TopKSize:             q.Keeper.GetTopKSize(ctx),
		BondCooldown:         q.Keeper.GetBondCooldown(ctx),
		RecencyWindowK:       q.Keeper.GetRecencyWindowK(ctx),
		BeaconRoundsPerBlock: q.Keeper.GetBeaconRoundsPerBlock(ctx),
		// A query, not consensus: reading the stored share (or the
		// default) at any height is fine.
		MergedMiningRewardShareBps: uint32(q.Keeper.GetMergedMiningRewardShareBps(ctx)),
	}, nil
}

func (q queryServer) ValidatorInfo(goCtx context.Context, req *QueryValidatorInfoRequest) (*QueryValidatorInfoResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	addrs := q.Keeper.IterateActiveValidators(ctx)
	infos := make([]*ValidatorInfo, 0, len(addrs))
	for _, addr := range addrs {
		enteredAt, _ := q.Keeper.GetValidatorEnteredAt(ctx, addr)
		infos = append(infos, &ValidatorInfo{
			Address:       addr.String(),
			TenureRatio:   q.Keeper.GetValidatorTenureRatio(ctx, addr).String(),
			EnteredAtUnix: enteredAt,
		})
	}

	return &QueryValidatorInfoResponse{Validators: infos}, nil
}

// MinerLeaderboard reports one epoch's recorded work, ranked
// highest-first using the identical comparator ComputeValidatorUpdates
// uses for real Top-K selection (work desc, address asc tiebreak) --
// this is a read-only display of that same real ranking, not a
// separate one that could disagree with it.
func (q queryServer) MinerLeaderboard(goCtx context.Context, req *QueryMinerLeaderboardRequest) (*QueryMinerLeaderboardResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	epoch := req.Epoch
	if epoch == 0 {
		epoch = q.Keeper.CurrentEpoch(ctx)
	}

	work := q.Keeper.IterateEpochWork(ctx, epoch)
	sort.Slice(work, func(i, j int) bool {
		if work[i].Work != work[j].Work {
			return work[i].Work > work[j].Work
		}
		return bytes.Compare(work[i].MinerAddr.Bytes(), work[j].MinerAddr.Bytes()) < 0
	})

	entries := make([]*MinerLeaderboardEntry, 0, len(work))
	for _, w := range work {
		entries = append(entries, &MinerLeaderboardEntry{
			Address: w.MinerAddr.String(),
			Work:    w.Work,
		})
	}

	return &QueryMinerLeaderboardResponse{Epoch: epoch, Entries: entries}, nil
}

// SeatWork reports the window the seat rule reads at the end of the
// current epoch: the same candidates, eligibility and 1% bar as
// computeSeatUpdates and computeSeats, in the order they seat in.
func (q queryServer) SeatWork(goCtx context.Context, req *QuerySeatWorkRequest) (*QuerySeatWorkResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	var only sdk.AccAddress
	if req.Address != "" {
		a, err := sdk.AccAddressFromBech32(req.Address)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "address: %v", err)
		}
		only = a
	}

	epoch := q.Keeper.CurrentEpoch(ctx)
	length := q.Keeper.GetEpochLength(ctx)
	if length <= 0 {
		length = 1
	}
	selection := (epoch+1)*length - 1
	first := epoch - SeatWorkWindowEpochs + 1
	if first < 0 {
		first = 0
	}

	cands, current := q.Keeper.seatCandidates(ctx, epoch)
	windowTotal, eligibleTotal := math.ZeroInt(), math.ZeroInt()
	for _, c := range cands {
		windowTotal = windowTotal.Add(c.Work)
		if c.Eligible {
			eligibleTotal = eligibleTotal.Add(c.Work)
		}
	}
	bar := windowTotal
	if selection >= gates.Height(ctx.ChainID(), SeatHardeningHeight) {
		bar = eligibleTotal
	}
	meetsBar := func(c seatCandidate) bool {
		return c.Eligible && c.Work.IsPositive() && c.Work.MulRaw(10_000).GTE(bar.MulRaw(SeatMinWorkShareBps))
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if !cands[i].Work.Equal(cands[j].Work) {
			return cands[i].Work.GT(cands[j].Work)
		}
		return bytes.Compare(cands[i].Addr, cands[j].Addr) < 0
	})

	info := func(c seatCandidate) *SeatWorkInfo {
		cur, ok := current[string(c.Addr)]
		if !ok {
			cur = math.ZeroInt()
		}
		return &SeatWorkInfo{
			Address:    c.Addr.String(),
			WindowWork: c.Work.String(),
			EpochWork:  cur.String(),
			Eligible:   c.Eligible,
			MeetsBar:   meetsBar(c),
		}
	}
	var entries []*SeatWorkInfo
	if only != nil {
		found := false
		for _, c := range cands {
			if c.Addr.Equals(only) {
				entries, found = []*SeatWorkInfo{info(c)}, true
				break
			}
		}
		if !found {
			entries = []*SeatWorkInfo{info(seatCandidate{Addr: only, Work: math.ZeroInt(), Eligible: q.Keeper.seatEligible(ctx, only)})}
		}
	} else {
		entries = make([]*SeatWorkInfo, 0, len(cands))
		for _, c := range cands {
			entries = append(entries, info(c))
		}
	}

	return &QuerySeatWorkResponse{
		Epoch:           epoch,
		FirstEpoch:      first,
		SelectionHeight: selection,
		SeatRule:        selection >= gates.Height(ctx.ChainID(), SeatRuleActivationHeight),
		WindowTotal:     windowTotal.String(),
		BarTotal:        bar.String(),
		MinShareBps:     SeatMinWorkShareBps,
		TopK:            q.Keeper.GetTopKSize(ctx),
		Entries:         entries,
	}, nil
}
