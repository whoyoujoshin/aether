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

// ProcessProposalExpiry checks every proposal still in its deposit
// period and expires (burning deposit) any whose deposit window has
// passed without meeting MinDeposit. Called from EndBlock every block.
func (k Keeper) ProcessProposalExpiry(ctx sdk.Context) {
	now := ctx.BlockTime().Unix()
	for _, proposal := range k.IterateProposals(ctx) {
		if proposal.Status != ProposalStatus_PROPOSAL_STATUS_DEPOSIT_PERIOD {
			continue
		}
		if now > proposal.DepositEndTime {
			if err := k.ExpireProposal(ctx, proposal); err != nil {
				k.Logger(ctx).Error("failed to expire proposal", "proposal_id", proposal.Id, "error", err)
			}
		}
	}
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