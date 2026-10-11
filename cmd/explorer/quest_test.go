package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/quest"
)

func TestQuestServesTheBoardCached(t *testing.T) {
	agent := sdk.AccAddress("quest_agent_________").String()
	loads := 0
	questCache.load = func(context.Context) (quest.Board, error) {
		loads++
		return quest.Board{QuestAddress: quest.Address(), Steps: quest.Steps,
			Agents: []quest.Progress{{Address: agent, Name: "Ada", Score: 2, Rank: 1, Done: map[string]int64{"joined": 5, "bought": 6}}}}, nil
	}
	questCache.at = time.Time{}
	t.Cleanup(func() { questCache.load, questCache.at = nil, time.Time{} })

	get := func(q string) (int, questResponse) {
		rec := httptest.NewRecorder()
		handleQuest(rec, httptest.NewRequest("GET", "/api/quest"+q, nil))
		var out questResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := get("")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, quest.Address(), out.QuestAddress)
	require.Len(t, out.Steps, len(quest.Steps))
	require.Len(t, out.Agents, 1)
	require.Nil(t, out.Agent)

	code, out = get("?addr=" + agent)
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, out.Agent)
	require.Equal(t, "Ada", out.Agent.Name)
	require.Equal(t, 1, loads, "a minute's cache")

	code, _ = get("?addr=nope")
	require.Equal(t, http.StatusBadRequest, code)
}
