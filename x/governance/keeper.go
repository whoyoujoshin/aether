package governance

import (
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"cosmossdk.io/store/types"
	storetypes "cosmossdk.io/store/types"
	"cosmossdk.io/math"
	sdkerrors "cosmossdk.io/errors"
	"cosmossdk.io/log"
)

type Keeper struct {
	cdc        codec.BinaryCodec
	storeKey   types.StoreKey
	bankKeeper BankKeeper
	powKeeper  PowKeeper
}

func NewKeeper(cdc codec.BinaryCodec, storeKey types.StoreKey, bankKeeper BankKeeper, powKeeper PowKeeper) Keeper {
	return Keeper{
		cdc:        cdc,
		storeKey:   storeKey,
		bankKeeper: bankKeeper,
		powKeeper:  powKeeper,
	}
}

func (k Keeper) SubmitProposal(ctx sdk.Context, title string) {
	ctx.Logger().Info("Proposal submitted", "title", title)
}

func (k Keeper) SetVotingPeriod(ctx sdk.Context, votingPeriod int64) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	bz := sdk.Uint64ToBigEndian(uint64(votingPeriod))
	store.Set([]byte("voting_period"), bz)
}

func (k Keeper) GetVotingPeriod(ctx sdk.Context) int64 {
	if k.storeKey == nil {
		return 0
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("voting_period"))
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

func (k Keeper) Heartbeat(ctx sdk.Context) {
	if k.storeKey == nil {
		return
	}
	ctx.KVStore(k.storeKey).Set([]byte("last_seen_height"), sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight())))
}

func (k Keeper) SetMinDeposit(ctx sdk.Context, minDeposit int64) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	store.Set([]byte("min_deposit"), sdk.Uint64ToBigEndian(uint64(minDeposit)))
}

func (k Keeper) GetMinDeposit(ctx sdk.Context) int64 {
	if k.storeKey == nil {
		return 0
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("min_deposit"))
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

func (k Keeper) SetDepositPeriod(ctx sdk.Context, depositPeriod int64) {
	if k.storeKey == nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	store.Set([]byte("deposit_period"), sdk.Uint64ToBigEndian(uint64(depositPeriod)))
}

func (k Keeper) GetDepositPeriod(ctx sdk.Context) int64 {
	if k.storeKey == nil {
		return 0
	}
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte("deposit_period"))
	if bz == nil {
		return 0
	}
	return int64(sdk.BigEndianToUint64(bz))
}

func (k Keeper) NextProposalID(ctx sdk.Context) uint64 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(KeyNextProposalID)
	var id uint64 = 1
	if bz != nil {
		id = sdk.BigEndianToUint64(bz)
	}
	store.Set(KeyNextProposalID, sdk.Uint64ToBigEndian(id+1))
	return id
}

// addDeposit transfers amount from depositor into the governance module
// account, accumulates it against the proposal's existing deposit
// (combining with any prior contribution from the same depositor), and
// transitions the proposal into its voting period the moment MinDeposit
// is met.
func (k Keeper) addDeposit(ctx sdk.Context, proposalID uint64, depositor sdk.AccAddress, amount math.Int) error {
	coins := sdk.NewCoins(sdk.NewCoin("aeth", amount))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, depositor, ModuleName, coins); err != nil {
		return sdkerrors.Wrapf(err, "failed to transfer deposit")
	}

	existingAmount := math.ZeroInt()
	if existing, ok := k.GetDeposit(ctx, proposalID, depositor); ok {
		parsed, parseOk := math.NewIntFromString(existing.Amount)
		if parseOk {
			existingAmount = parsed
		}
	}
	newTotal := existingAmount.Add(amount)
	k.SetDeposit(ctx, Deposit{
		ProposalId: proposalID,
		Depositor:  depositor.String(),
		Amount:     newTotal.String(),
	})

	proposal, ok := k.GetProposal(ctx, proposalID)
	if !ok {
		return sdkerrors.Wrapf(ErrProposalNotFound, "proposal %d disappeared mid-deposit", proposalID)
	}

	currentTotal := math.ZeroInt()
	if parsed, parseOk := math.NewIntFromString(proposal.TotalDeposit); parseOk {
		currentTotal = parsed
	}
	newProposalTotal := currentTotal.Add(amount)
	proposal.TotalDeposit = newProposalTotal.String()

	minDeposit := k.GetMinDeposit(ctx)
	if newProposalTotal.GTE(math.NewInt(minDeposit)) && proposal.Status == ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD {
		votingPeriod := k.GetVotingPeriod(ctx)
		proposal.Status = ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD
		proposal.VotingStartTime = ctx.BlockTime().Unix()
		proposal.VotingEndTime = proposal.VotingStartTime + votingPeriod
	}

	k.SetProposal(ctx, proposal)
	return nil
}
func proposalKey(id uint64) []byte {
	return append(KeyProposalPrefix, sdk.Uint64ToBigEndian(id)...)
}

func (k Keeper) SetProposal(ctx sdk.Context, proposal Proposal) {
	bz := k.cdc.MustMarshal(&proposal)
	ctx.KVStore(k.storeKey).Set(proposalKey(proposal.Id), bz)
}

func (k Keeper) GetProposal(ctx sdk.Context, id uint64) (Proposal, bool) {
	bz := ctx.KVStore(k.storeKey).Get(proposalKey(id))
	if bz == nil {
		return Proposal{}, false
	}
	var proposal Proposal
	k.cdc.MustUnmarshal(bz, &proposal)
	return proposal, true
}

func (k Keeper) IterateProposals(ctx sdk.Context) []Proposal {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, KeyProposalPrefix)
	defer iterator.Close()

	var proposals []Proposal
	for ; iterator.Valid(); iterator.Next() {
		var proposal Proposal
		k.cdc.MustUnmarshal(iterator.Value(), &proposal)
		proposals = append(proposals, proposal)
	}
	return proposals
}

func depositKey(proposalID uint64, depositor sdk.AccAddress) []byte {
	key := append(KeyDepositPrefix, sdk.Uint64ToBigEndian(proposalID)...)
	return append(key, depositor.Bytes()...)
}

func depositPrefixForProposal(proposalID uint64) []byte {
	return append(KeyDepositPrefix, sdk.Uint64ToBigEndian(proposalID)...)
}

func (k Keeper) SetDeposit(ctx sdk.Context, deposit Deposit) {
	bz := k.cdc.MustMarshal(&deposit)
	depositorAddr, err := sdk.AccAddressFromBech32(deposit.Depositor)
	if err != nil {
		return
	}
	ctx.KVStore(k.storeKey).Set(depositKey(deposit.ProposalId, depositorAddr), bz)
}

func (k Keeper) GetDeposit(ctx sdk.Context, proposalID uint64, depositor sdk.AccAddress) (Deposit, bool) {
	bz := ctx.KVStore(k.storeKey).Get(depositKey(proposalID, depositor))
	if bz == nil {
		return Deposit{}, false
	}
	var deposit Deposit
	k.cdc.MustUnmarshal(bz, &deposit)
	return deposit, true
}

func (k Keeper) IterateDeposits(ctx sdk.Context, proposalID uint64) []Deposit {
	store := ctx.KVStore(k.storeKey)
	prefix := depositPrefixForProposal(proposalID)
	iterator := types.KVStorePrefixIterator(store, prefix)
	defer iterator.Close()

	var deposits []Deposit
	for ; iterator.Valid(); iterator.Next() {
		var deposit Deposit
		k.cdc.MustUnmarshal(iterator.Value(), &deposit)
		deposits = append(deposits, deposit)
	}
	return deposits
}

// ExpireProposal transitions a deposit-period proposal that never met
// MinDeposit within its deposit window to expired status, burning
// whatever deposit was accumulated -- per the locked design, a deposit
// is refunded on pass, burned on fail OR expiry. There's no "refund on
// expiry" case; failing to attract enough support within the window is
// treated the same as failing outright.
func (k Keeper) ExpireProposal(ctx sdk.Context, proposal Proposal) error {
	deposits := k.IterateDeposits(ctx, proposal.Id)
	for _, d := range deposits {
		amount, ok := math.NewIntFromString(d.Amount)
		if !ok || !amount.IsPositive() {
			continue
		}
		coins := sdk.NewCoins(sdk.NewCoin("aeth", amount))
		if err := k.bankKeeper.BurnCoins(ctx, ModuleName, coins); err != nil {
			return sdkerrors.Wrapf(err, "failed to burn deposit for expired proposal %d", proposal.Id)
		}
	}

	proposal.Status = ProposalStatus_PROPOSAL_STATUS_EXPIRED
	k.SetProposal(ctx, proposal)
	return nil
}

// ProcessProposalLifecycle checks every proposal and advances it if its
// current period has ended: deposit-period proposals that missed
// MinDeposit are expired (deposit burned), and voting-period proposals
// whose window has closed are tallied and resolved. Called from
// EndBlock every block.
func (k Keeper) ProcessProposalLifecycle(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()
	quorumThreshold := k.computeQuorumThreshold(ctx)

	for _, proposal := range k.IterateProposals(ctx) {
		switch proposal.Status {
		case ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD:
			if now > proposal.DepositEndTime {
				if err := k.ExpireProposal(ctx, proposal); err != nil {
					k.Logger(ctx).Error("failed to expire proposal", "proposal_id", proposal.Id, "error", err)
				}
			}
		case ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD:
			if now > proposal.VotingEndTime {
				if err := k.ResolveProposal(ctx, proposal, quorumThreshold); err != nil {
					k.Logger(ctx).Error("failed to resolve proposal", "proposal_id", proposal.Id, "error", err)
				}
			}
		}
	}
}

// computeQuorumThreshold returns ceil(0.6 * TopKSize) -- the minimum
// number of validators who must cast a still-valid vote for a proposal's
// resolution to count at all, per the locked 60% quorum spec. Computed
// dynamically from x/pow's current TopKSize rather than hardcoded, so a
// future governance-driven change to TopKSize doesn't silently leave
// this quorum number stale.
func (k Keeper) computeQuorumThreshold(ctx sdk.Context) int64 {
	topK := k.powKeeper.GetTopKSize(ctx)
	// Ceiling division: (topK*6 + 9) / 10 computes ceil(topK * 0.6)
	// using only integer math, avoiding any float/Dec rounding subtlety.
	return (topK*6 + 9) / 10
}

func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", "x/"+ModuleName)
}

func voteKey(proposalID uint64, voter sdk.AccAddress) []byte {
	key := append(KeyVotePrefix, sdk.Uint64ToBigEndian(proposalID)...)
	return append(key, voter.Bytes()...)
}

func votePrefixForProposal(proposalID uint64) []byte {
	return append(KeyVotePrefix, sdk.Uint64ToBigEndian(proposalID)...)
}

// SetVote stores a vote, keyed by (proposalID, voter) -- casting a second
// vote for the same proposal overwrites the first, matching standard
// Cosmos governance behavior (a validator may change their vote any time
// before voting closes).
func (k Keeper) SetVote(ctx sdk.Context, vote Vote) {
	voterAddr, err := sdk.AccAddressFromBech32(vote.Voter)
	if err != nil {
		return
	}
	bz := k.cdc.MustMarshal(&vote)
	ctx.KVStore(k.storeKey).Set(voteKey(vote.ProposalId, voterAddr), bz)
}

func (k Keeper) GetVote(ctx sdk.Context, proposalID uint64, voter sdk.AccAddress) (Vote, bool) {
	bz := ctx.KVStore(k.storeKey).Get(voteKey(proposalID, voter))
	if bz == nil {
		return Vote{}, false
	}
	var vote Vote
	k.cdc.MustUnmarshal(bz, &vote)
	return vote, true
}

func (k Keeper) IterateVotes(ctx sdk.Context, proposalID uint64) []Vote {
	store := ctx.KVStore(k.storeKey)
	iterator := types.KVStorePrefixIterator(store, votePrefixForProposal(proposalID))
	defer iterator.Close()

	var votes []Vote
	for ; iterator.Valid(); iterator.Next() {
		var vote Vote
		k.cdc.MustUnmarshal(iterator.Value(), &vote)
		votes = append(votes, vote)
	}
	return votes
}

// TallyResult holds the intermediate computed values from tallying a
// proposal's votes, useful both for resolution and for future querying
// (e.g. showing users why a proposal passed/failed).
type TallyResult struct {
	ValidVoterCount int64
	YesPower        math.LegacyDec
	NoPower         math.LegacyDec
	AbstainPower    math.LegacyDec
	VetoPower       math.LegacyDec
}

// TallyVotes re-checks each voter's CURRENT active-validator status
// (not their status at cast time) and sums tenure-weighted power by
// option. A voter who has since fallen out of Top-K or been slashed is
// excluded entirely -- not counted toward quorum, not counted toward
// any option's power. This is deliberate: a validator slashed for
// misbehavior after voting should not retain influence over the
// outcome via a vote cast before their misbehavior was caught.
func (k Keeper) TallyVotes(ctx sdk.Context, proposalID uint64) TallyResult {
	result := TallyResult{
		YesPower:     math.LegacyZeroDec(),
		NoPower:      math.LegacyZeroDec(),
		AbstainPower: math.LegacyZeroDec(),
		VetoPower:    math.LegacyZeroDec(),
	}

	for _, vote := range k.IterateVotes(ctx, proposalID) {
		voterAddr, err := sdk.AccAddressFromBech32(vote.Voter)
		if err != nil {
			continue
		}
		if !k.powKeeper.IsActiveValidator(ctx, voterAddr) {
			continue
		}

		weight, err := math.LegacyNewDecFromStr(vote.Weight)
if err != nil {
	continue
}

		result.ValidVoterCount++
		switch vote.Option {
		case VoteOption_VOTE_OPTION_YES:
			result.YesPower = result.YesPower.Add(weight)
		case VoteOption_VOTE_OPTION_NO:
			result.NoPower = result.NoPower.Add(weight)
		case VoteOption_VOTE_OPTION_ABSTAIN:
			result.AbstainPower = result.AbstainPower.Add(weight)
		case VoteOption_VOTE_OPTION_NO_WITH_VETO:
			result.VetoPower = result.VetoPower.Add(weight)
		}
	}

	return result
}

// ResolveProposal tallies votes for a proposal whose voting period has
// closed and transitions it to its final status:
//   - FAILED_QUORUM if fewer than QuorumThreshold validators cast a
//     still-valid vote (deposit burned)
//   - REJECTED if veto power reaches at least 1/3 of non-abstain power
//     cast, regardless of the yes/no ratio (deposit burned)
//   - PASSED if yes power reaches at least 2/3 of non-abstain power
//     cast (deposit refunded)
//   - REJECTED otherwise, including an exact tie (deposit burned) --
//     "ties fail to pass, status quo wins"
func (k Keeper) ResolveProposal(ctx sdk.Context, proposal Proposal, quorumThreshold int64) error {
	result := k.TallyVotes(ctx, proposal.Id)

	if result.ValidVoterCount < quorumThreshold {
		proposal.Status = ProposalStatus_PROPOSAL_STATUS_FAILED_QUORUM
		k.SetProposal(ctx, proposal)
		return k.burnDeposits(ctx, proposal.Id)
	}

	nonAbstainPower := result.YesPower.Add(result.NoPower).Add(result.VetoPower)

	if !nonAbstainPower.IsZero() {
		vetoRatio := result.VetoPower.Quo(nonAbstainPower)
		if vetoRatio.GTE(math.LegacyNewDec(1).QuoInt64(3)) {
			proposal.Status = ProposalStatus_PROPOSAL_STATUS_REJECTED
			k.SetProposal(ctx, proposal)
			return k.burnDeposits(ctx, proposal.Id)
		}

		yesRatio := result.YesPower.Quo(nonAbstainPower)
		
		if yesRatio.GTE(math.LegacyNewDec(2).QuoInt64(3)) {
			proposal.Status = ProposalStatus_PROPOSAL_STATUS_PASSED
			k.SetProposal(ctx, proposal)
			return k.refundDeposits(ctx, proposal.Id)
		}
	}

	proposal.Status = ProposalStatus_PROPOSAL_STATUS_REJECTED
	k.SetProposal(ctx, proposal)
	return k.burnDeposits(ctx, proposal.Id)
}

func (k Keeper) burnDeposits(ctx sdk.Context, proposalID uint64) error {
	for _, d := range k.IterateDeposits(ctx, proposalID) {
		amount, ok := math.NewIntFromString(d.Amount)
		if !ok || !amount.IsPositive() {
			continue
		}
		coins := sdk.NewCoins(sdk.NewCoin("aeth", amount))
		if err := k.bankKeeper.BurnCoins(ctx, ModuleName, coins); err != nil {
			return sdkerrors.Wrapf(err, "failed to burn deposit for proposal %d", proposalID)
		}
	}
	return nil
}

func (k Keeper) refundDeposits(ctx sdk.Context, proposalID uint64) error {
	for _, d := range k.IterateDeposits(ctx, proposalID) {
		depositorAddr, err := sdk.AccAddressFromBech32(d.Depositor)
		if err != nil {
			continue
		}
		amount, ok := math.NewIntFromString(d.Amount)
		if !ok || !amount.IsPositive() {
			continue
		}
		coins := sdk.NewCoins(sdk.NewCoin("aeth", amount))
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, ModuleName, depositorAddr, coins); err != nil {
			return sdkerrors.Wrapf(err, "failed to refund deposit for proposal %d", proposalID)
		}
	}
	return nil
}