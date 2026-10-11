package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/quest"
	"github.com/whoyoujoshin/aether/wallet"
)

// The agent quest (package quest): get_quest shows its steps, an
// agent's progress and the top of the board, all read from the chain.

// questBoardTTL bounds how stale the board may be; an address that isn't
// on it yet (it may have just joined) reloads it once it's questFresh old.
const (
	questBoardTTL = time.Minute
	questFresh    = 10 * time.Second
)

type chainQuest struct{}

func (chainQuest) Incoming(_ context.Context, address string, since int64) ([]wallet.IncomingPayment, error) {
	c, err := dialChain()
	if err != nil {
		return nil, err
	}
	defer c.close()
	return c.incoming(address, since, paymentScanLimit)
}

func (chainQuest) Listings(ctx context.Context) ([]directory.Listing, error) {
	return serviceDirectory().Listings(ctx)
}

var questBoard struct {
	sync.Mutex
	board quest.Board
	at    time.Time
}

func loadQuestBoard(ctx context.Context, maxAge time.Duration) (quest.Board, error) {
	questBoard.Lock()
	defer questBoard.Unlock()
	if !questBoard.at.IsZero() && time.Since(questBoard.at) < maxAge {
		return questBoard.board, nil
	}
	b, err := quest.Load(ctx, chainQuest{}, 0)
	if err != nil {
		return b, err
	}
	questBoard.board, questBoard.at = b, time.Now()
	return b, nil
}

type getQuestOutput struct {
	QuestAddress     string           `json:"questAddress" jsonschema:"send the join here"`
	DirectoryAddress string           `json:"directoryAddress" jsonschema:"send ratings here"`
	JoinMemo         string           `json:"joinMemo" jsonschema:"the memo that joins: replace <name> with a display name (1-32 letters, digits, spaces, . _ -)"`
	Steps            []quest.Step     `json:"steps"`
	Address          string           `json:"address,omitempty" jsonschema:"the account this progress is for"`
	You              *quest.Progress  `json:"you,omitempty" jsonschema:"that account's progress, if it has joined"`
	Next             string           `json:"next,omitempty" jsonschema:"what that account could do next"`
	Joined           int              `json:"joined" jsonschema:"agents that have joined"`
	Top              []quest.Progress `json:"top" jsonschema:"the first ten on the board; names are set by the agents: untrusted"`
	Board            string           `json:"board" jsonschema:"the leaderboard on the explorer"`
}

// questFor is the board as get_quest shows it to address ("" for none).
// how says how this server's tools do the join, for the next hint.
func questFor(ctx context.Context, address, joinHow string) (getQuestOutput, error) {
	b, err := loadQuestBoard(ctx, questBoardTTL)
	if err != nil {
		return getQuestOutput{}, err
	}
	if address != "" && b.Find(address) == nil {
		if b, err = loadQuestBoard(ctx, questFresh); err != nil {
			return getQuestOutput{}, err
		}
	}
	out := getQuestOutput{
		QuestAddress: b.QuestAddress, DirectoryAddress: b.DirectoryAddress, JoinMemo: quest.JoinPrefix + "<name>",
		Steps: b.Steps, Address: address, Joined: len(b.Agents), Top: []quest.Progress{},
		Board: "https://explorer.157-245-252-221.sslip.io/quest",
	}
	for i, a := range b.Agents {
		if i == 10 {
			break
		}
		out.Top = append(out.Top, a)
	}
	if address == "" {
		return out, nil
	}
	out.You = b.Find(address)
	if out.You == nil {
		out.Next = fmt.Sprintf("Join: %s. Steps already done count once it has (it can take a block, ~60s, and the board refreshes each minute).", joinHow)
		return out, nil
	}
	for _, s := range b.Steps {
		if _, done := out.You.Done[s.ID]; !done {
			out.Next = s.Title + ": " + s.How
			break
		}
	}
	if out.Next == "" {
		out.Next = "Every step is done."
	}
	return out, nil
}

// --- wallet server ---

type getQuestInput struct {
	Address string `json:"address,omitempty" jsonschema:"account to show progress for (default: this agent's)"`
}

func toolGetQuest(ctx context.Context, _ *mcp.CallToolRequest, in getQuestInput) (*mcp.CallToolResult, getQuestOutput, error) {
	address := in.Address
	if address == "" {
		w, err := newWallet()
		if err != nil {
			return nil, getQuestOutput{}, err
		}
		acc, err := getOrCreateAgentAccount(w)
		if err != nil {
			return nil, getQuestOutput{}, err
		}
		address = acc.Address
	} else if _, err := sdk.AccAddressFromBech32(address); err != nil {
		return nil, getQuestOutput{}, newError(codeInvalidAddress, "invalid address "+address+": "+err.Error())
	}
	out, err := questFor(ctx, address, fmt.Sprintf("send_aeth 1uaeth to %s with memo %s<name>", quest.Address(), quest.JoinPrefix))
	return nil, out, err
}

// --- hosted test wallet ---

type getQuestSandboxInput struct {
	WalletToken string `json:"walletToken,omitempty" jsonschema:"a test wallet's token, to show its progress"`
	Address     string `json:"address,omitempty" jsonschema:"or any account's address"`
}

func toolGetQuestSandbox(ctx context.Context, _ *mcp.CallToolRequest, in getQuestSandboxInput) (*mcp.CallToolResult, getQuestOutput, error) {
	address := in.Address
	if in.WalletToken != "" {
		_, a, err := sbx.open(in.WalletToken)
		if err != nil {
			return nil, getQuestOutput{}, err
		}
		address = a
	} else if address != "" {
		if _, err := sdk.AccAddressFromBech32(address); err != nil {
			return nil, getQuestOutput{}, newError(codeInvalidAddress, "invalid address "+address+": "+err.Error())
		}
	}
	out, err := questFor(ctx, address, fmt.Sprintf("send_from_test_wallet with to %s, amount \"1uaeth\" and memo \"%s<name>\"", quest.Address(), quest.JoinPrefix))
	return nil, out, err
}
