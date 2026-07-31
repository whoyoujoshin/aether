package testutil

import (
	"time"

	"cosmossdk.io/core/comet"
)

// FakeBlockInfo is a minimal test double for comet.BlockInfo, letting tests
// inject a specific set of misbehavior evidence without needing a live
// CometBFT instance.
type FakeBlockInfo struct {
	Evidence   comet.EvidenceList
	LastCommit comet.CommitInfo
}

func (f FakeBlockInfo) GetEvidence() comet.EvidenceList { return f.Evidence }
func (f FakeBlockInfo) GetValidatorsHash() []byte       { return nil }
func (f FakeBlockInfo) GetProposerAddress() []byte      { return nil }
func (f FakeBlockInfo) GetLastCommit() comet.CommitInfo { return f.LastCommit }

// FakeEvidenceList is a simple slice-backed comet.EvidenceList.
type FakeEvidenceList []comet.Evidence

func (f FakeEvidenceList) Len() int                  { return len(f) }
func (f FakeEvidenceList) Get(i int) comet.Evidence  { return f[i] }

// FakeEvidence is a minimal test double for comet.Evidence.
type FakeEvidence struct {
	MisbehaviorType   comet.MisbehaviorType
	OffendingValidator comet.Validator
	AtHeight          int64
	AtTime            time.Time
	VotingPowerTotal  int64
}

func (f FakeEvidence) Type() comet.MisbehaviorType { return f.MisbehaviorType }
func (f FakeEvidence) Validator() comet.Validator  { return f.OffendingValidator }
func (f FakeEvidence) Height() int64               { return f.AtHeight }
func (f FakeEvidence) Time() time.Time             { return f.AtTime }
func (f FakeEvidence) TotalVotingPower() int64     { return f.VotingPowerTotal }

// FakeValidator is a minimal test double for comet.Validator.
type FakeValidator struct {
	Addr  []byte
	Pow   int64
}

func (f FakeValidator) Address() []byte { return f.Addr }
func (f FakeValidator) Power() int64    { return f.Pow }

// FakeCommitInfo is a minimal test double for comet.CommitInfo.
type FakeCommitInfo struct {
	VoteList comet.VoteInfos
}

func (f FakeCommitInfo) Round() int32          { return 0 }
func (f FakeCommitInfo) Votes() comet.VoteInfos { return f.VoteList }

// FakeVoteInfos is a simple slice-backed comet.VoteInfos.
type FakeVoteInfos []comet.VoteInfo

func (f FakeVoteInfos) Len() int               { return len(f) }
func (f FakeVoteInfos) Get(i int) comet.VoteInfo { return f[i] }

// FakeVoteInfo is a minimal test double for comet.VoteInfo.
type FakeVoteInfo struct {
	Voter comet.Validator
	Flag  comet.BlockIDFlag
}

func (f FakeVoteInfo) Validator() comet.Validator      { return f.Voter }
func (f FakeVoteInfo) GetBlockIDFlag() comet.BlockIDFlag { return f.Flag }