// cmd/explorer/dto.go
//
// Explicit JSON response shapes for the explorer API, converted from
// whatever the underlying source type actually is. This exists
// because none of the underlying sources share one consistent JSON
// convention: protobuf-generated types (x/pow, x/governance) carry
// snake_case json tags (gogoproto's convention, meant for internal/
// gRPC-gateway use, not a hand-designed REST API), enums serialize as
// bare integers unless explicitly converted to their string name, and
// wallet.Transaction/TransactionDetail/etc. have NO json tags at all
// -- they serialize using their Go field names verbatim (PascalCase),
// which cmd/walletapi's already-shipped desktop frontend
// (web/aether-pay-desktop.html) depends on directly. Changing those
// shared wallet types' JSON shape would silently break that live
// consumer, so this file converts explicitly instead, keeping the
// explorer's own API contract clean without touching shared types.
package main

import (
	"encoding/hex"
	"time"

	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cometbft/cometbft/types"
	"github.com/whoyoujoshin/aether/wallet"
	"github.com/whoyoujoshin/aether/x/governance"
	"github.com/whoyoujoshin/aether/x/pow"
)

type validatorInfoDTO struct {
	Address       string `json:"address"`
	TenureRatio   string `json:"tenureRatio"`
	EnteredAtUnix int64  `json:"enteredAtUnix"`
}

func toValidatorInfoDTOs(vs []*pow.ValidatorInfo) []validatorInfoDTO {
	out := make([]validatorInfoDTO, 0, len(vs))
	for _, v := range vs {
		out = append(out, validatorInfoDTO{
			Address:       v.Address,
			TenureRatio:   v.TenureRatio,
			EnteredAtUnix: v.EnteredAtUnix,
		})
	}
	return out
}

type proposalDTO struct {
	ID              uint64 `json:"id"`
	Recipient       string `json:"recipient"`
	Amount          string `json:"amount"`
	TotalDeposit    string `json:"totalDeposit"`
	Status          string `json:"status"`
	ProposalType    string `json:"proposalType"`
	SubmitTime      int64  `json:"submitTime"`
	DepositEndTime  int64  `json:"depositEndTime"`
	VotingStartTime int64  `json:"votingStartTime"`
	VotingEndTime   int64  `json:"votingEndTime"`
}

func toProposalDTO(p *governance.Proposal) proposalDTO {
	return proposalDTO{
		ID:              p.Id,
		Recipient:       p.Recipient,
		Amount:          p.Amount,
		TotalDeposit:    p.TotalDeposit,
		Status:          p.Status.String(),
		ProposalType:    p.ProposalType.String(),
		SubmitTime:      p.SubmitTime,
		DepositEndTime:  p.DepositEndTime,
		VotingStartTime: p.VotingStartTime,
		VotingEndTime:   p.VotingEndTime,
	}
}

func toProposalDTOs(ps []*governance.Proposal) []proposalDTO {
	out := make([]proposalDTO, 0, len(ps))
	for _, p := range ps {
		out = append(out, toProposalDTO(p))
	}
	return out
}

type voteDTO struct {
	ProposalID uint64 `json:"proposalId"`
	Voter      string `json:"voter"`
	Option     string `json:"option"`
	Weight     string `json:"weight"`
}

func toVoteDTOs(vs []*governance.Vote) []voteDTO {
	out := make([]voteDTO, 0, len(vs))
	for _, v := range vs {
		out = append(out, voteDTO{
			ProposalID: v.ProposalId,
			Voter:      v.Voter,
			Option:     v.Option.String(),
			Weight:     v.Weight,
		})
	}
	return out
}

type tallyDTO struct {
	ValidVoterCount int64  `json:"validVoterCount"`
	YesPower        string `json:"yesPower"`
	NoPower         string `json:"noPower"`
	AbstainPower    string `json:"abstainPower"`
	VetoPower       string `json:"vetoPower"`
	QuorumThreshold int64  `json:"quorumThreshold"`
}

func toTallyDTO(t *governance.QueryTallyResponse) tallyDTO {
	return tallyDTO{
		ValidVoterCount: t.ValidVoterCount,
		YesPower:        t.YesPower,
		NoPower:         t.NoPower,
		AbstainPower:    t.AbstainPower,
		VetoPower:       t.VetoPower,
		QuorumThreshold: t.QuorumThreshold,
	}
}

type recentTransactionDTO struct {
	Hash      string `json:"hash"`
	Height    int64  `json:"height"`
	Code      uint32 `json:"code"`
	MsgType   string `json:"msgType"`
	Timestamp string `json:"timestamp"`
}

func toRecentTransactionDTOs(txs []wallet.RecentTransaction) []recentTransactionDTO {
	out := make([]recentTransactionDTO, 0, len(txs))
	for _, t := range txs {
		out = append(out, recentTransactionDTO{
			Hash:      t.Hash,
			Height:    t.Height,
			Code:      t.Code,
			MsgType:   t.MsgType,
			Timestamp: t.Timestamp,
		})
	}
	return out
}

type transactionDTO struct {
	Hash      string `json:"hash"`
	Height    int64  `json:"height"`
	Code      uint32 `json:"code"`
	Direction string `json:"direction"`
	Amount    string `json:"amount"`
	Timestamp string `json:"timestamp"`
}

func toTransactionDTOs(txs []wallet.Transaction) []transactionDTO {
	out := make([]transactionDTO, 0, len(txs))
	for _, t := range txs {
		out = append(out, transactionDTO{
			Hash:      t.Hash,
			Height:    t.Height,
			Code:      t.Code,
			Direction: t.Direction,
			Amount:    t.Amount,
			Timestamp: t.Timestamp,
		})
	}
	return out
}

type transferDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

type auxPowInfoDTO struct {
	ParentHeaderBase64 string `json:"parentHeaderBase64"`
	CoinbaseTxBase64   string `json:"coinbaseTxBase64"`
	AuxBlockHashBase64 string `json:"auxBlockHashBase64"`
}

type transactionDetailDTO struct {
	Hash      string         `json:"hash"`
	Height    int64          `json:"height"`
	Code      uint32         `json:"code"`
	RawLog    string         `json:"rawLog"`
	GasUsed   int64          `json:"gasUsed"`
	GasWanted int64          `json:"gasWanted"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Amount    string         `json:"amount"`
	Timestamp string         `json:"timestamp"`
	Transfers []transferDTO  `json:"transfers"`
	AuxPow    *auxPowInfoDTO `json:"auxPow"`
}

func toTransactionDetailDTO(d *wallet.TransactionDetail) transactionDetailDTO {
	transfers := make([]transferDTO, 0, len(d.Transfers))
	for _, t := range d.Transfers {
		transfers = append(transfers, transferDTO{From: t.From, To: t.To, Amount: t.Amount})
	}

	var auxPow *auxPowInfoDTO
	if d.AuxPow != nil {
		auxPow = &auxPowInfoDTO{
			ParentHeaderBase64: d.AuxPow.ParentHeaderBase64,
			CoinbaseTxBase64:   d.AuxPow.CoinbaseTxBase64,
			AuxBlockHashBase64: d.AuxPow.AuxBlockHashBase64,
		}
	}

	return transactionDetailDTO{
		Hash:      d.Hash,
		Height:    d.Height,
		Code:      d.Code,
		RawLog:    d.RawLog,
		GasUsed:   d.GasUsed,
		GasWanted: d.GasWanted,
		From:      d.From,
		To:        d.To,
		Amount:    d.Amount,
		Timestamp: d.Timestamp,
		Transfers: transfers,
		AuxPow:    auxPow,
	}
}

type blockSummaryDTO struct {
	Height          int64  `json:"height"`
	Hash            string `json:"hash"`
	Time            string `json:"time"`
	NumTxs          int    `json:"numTxs"`
	ProposerAddress string `json:"proposerAddress"`
}

func toBlockSummaryDTOs(metas []*types.BlockMeta) []blockSummaryDTO {
	out := make([]blockSummaryDTO, 0, len(metas))
	for _, m := range metas {
		out = append(out, blockSummaryDTO{
			Height:          m.Header.Height,
			Hash:            m.BlockID.Hash.String(),
			Time:            m.Header.Time.Format(time.RFC3339),
			NumTxs:          m.NumTxs,
			ProposerAddress: hex.EncodeToString(m.Header.ProposerAddress),
		})
	}
	return out
}

type blockDetailDTO struct {
	Height          int64    `json:"height"`
	Hash            string   `json:"hash"`
	Time            string   `json:"time"`
	ProposerAddress string   `json:"proposerAddress"`
	AppHash         string   `json:"appHash"`
	LastCommitHash  string   `json:"lastCommitHash"`
	DataHash        string   `json:"dataHash"`
	NumTxs          int      `json:"numTxs"`
	TxHashes        []string `json:"txHashes"`
}

func toBlockDetailDTO(r *ctypes.ResultBlock) blockDetailDTO {
	txHashes := make([]string, 0, len(r.Block.Data.Txs))
	for _, tx := range r.Block.Data.Txs {
		txHashes = append(txHashes, hex.EncodeToString(tx.Hash()))
	}
	return blockDetailDTO{
		Height:          r.Block.Header.Height,
		Hash:            r.BlockID.Hash.String(),
		Time:            r.Block.Header.Time.Format(time.RFC3339),
		ProposerAddress: hex.EncodeToString(r.Block.Header.ProposerAddress),
		AppHash:         r.Block.Header.AppHash.String(),
		LastCommitHash:  r.Block.Header.LastCommitHash.String(),
		DataHash:        r.Block.Header.DataHash.String(),
		NumTxs:          len(r.Block.Data.Txs),
		TxHashes:        txHashes,
	}
}
