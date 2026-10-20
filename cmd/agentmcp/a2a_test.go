package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/whoyoujoshin/aether/a2a"
	"github.com/whoyoujoshin/aether/directory"
	"github.com/whoyoujoshin/aether/wallet"
)

// The card's skills, the endpoint's skills and the public MCP tools are
// one list.
func TestA2A_SkillsMatchThePublicTools(t *testing.T) {
	var card []string
	for _, s := range a2a.Skills {
		card = append(card, s.ID)
	}
	var endpoint []string
	for name := range a2aSkills {
		endpoint = append(endpoint, name)
	}
	require.Equal(t, sortedCopy(publicToolNames), sortedCopy(card))
	require.Equal(t, sortedCopy(publicToolNames), sortedCopy(endpoint))
}

type rpcReply struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func a2aCall(t *testing.T, url, version, method string, params any) rpcReply {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, url+"/a2a", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set("A2A-Version", version)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out rpcReply
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

func text(s string) a2a.Part { return a2a.TextPart(s) }

func userMessage(parts ...a2a.Part) map[string]any {
	return map[string]any{"message": a2a.Message{MessageID: "m-1", Role: a2a.RoleUser, Parts: parts}}
}

func taskOf(t *testing.T, r rpcReply) a2a.Task {
	t.Helper()
	require.Nil(t, r.Error)
	var resp a2a.SendMessageResponse
	require.NoError(t, json.Unmarshal(r.Result, &resp))
	require.NotNil(t, resp.Task, "result: %s", r.Result)
	return *resp.Task
}

func TestA2A_JSONRPC(t *testing.T) {
	kr := filepath.Join(t.TempDir(), "keyring")
	prevKR, prevTrust := keyringDir, trustedRaters
	keyringDir, trustedRaters = kr, nil
	dir = &directory.Directory{Scan: func() ([]wallet.IncomingPayment, error) { return nil, nil }}
	dirOnce.Do(func() {})
	t.Cleanup(func() {
		dir, dirOnce = nil, sync.Once{}
		keyringDir, trustedRaters = prevKR, prevTrust
	})
	srv := httptest.NewServer(publicMux(newPublicServer()))
	t.Cleanup(srv.Close)

	// A skill asked for in plain text runs and finishes in the call.
	task := taskOf(t, a2aCall(t, srv.URL, "1.0", "SendMessage", userMessage(text("find services for block pulse"))))
	require.Equal(t, a2a.StateCompleted, task.Status.State)
	require.Len(t, task.Artifacts, 1)
	require.Equal(t, "find_services", task.Artifacts[0].Name)
	require.Equal(t, a2a.ModeJSON, task.Artifacts[0].Parts[0].MediaType)
	require.Contains(t, string(task.Artifacts[0].Parts[0].Data), `"services"`)
	require.Equal(t, "m-1", task.History[0].MessageID)

	// GetTask returns it; CancelTask can't cancel a finished task.
	var got a2a.Task
	r := a2aCall(t, srv.URL, "1.0", "GetTask", map[string]any{"id": task.ID})
	require.Nil(t, r.Error)
	require.NoError(t, json.Unmarshal(r.Result, &got))
	require.Equal(t, task.ID, got.ID)
	require.Equal(t, a2a.CodeTaskNotCancelable, a2aCall(t, srv.URL, "1.0", "CancelTask", map[string]any{"id": task.ID}).Error.Code)
	require.Equal(t, a2a.CodeTaskNotFound, a2aCall(t, srv.URL, "1.0", "GetTask", map[string]any{"id": "nope"}).Error.Code)

	// A data part names the skill and its input; a bad input fails the
	// task with the tool's own error, before any chain call.
	data, _ := json.Marshal(map[string]any{"skill": "get_balance", "input": map[string]string{"address": "not-an-address"}})
	task = taskOf(t, a2aCall(t, srv.URL, "", "SendMessage", userMessage(a2a.DataPart(data))))
	require.Equal(t, a2a.StateFailed, task.Status.State)
	require.Contains(t, *task.Status.Message.Parts[0].Text, codeInvalidAddress)

	// An unknown skill is rejected; a message with no skill gets help.
	data, _ = json.Marshal(map[string]any{"skill": "send_aeth", "input": map[string]string{}})
	require.Equal(t, a2a.StateRejected, taskOf(t, a2aCall(t, srv.URL, "1.0", "SendMessage", userMessage(a2a.DataPart(data)))).Status.State)
	r = a2aCall(t, srv.URL, "1.0", "SendMessage", userMessage(text("hello there")))
	require.Nil(t, r.Error)
	var resp a2a.SendMessageResponse
	require.NoError(t, json.Unmarshal(r.Result, &resp))
	require.Nil(t, resp.Task)
	require.Contains(t, *resp.Message.Parts[0].Text, "quote_swap")

	// The methods it doesn't offer, a version it doesn't speak, and
	// JSON-RPC mistakes, each with the spec's code.
	for method, code := range map[string]int{
		"SendStreamingMessage": a2a.CodeUnsupported, "SubscribeToTask": a2a.CodeUnsupported, "ListTasks": a2a.CodeUnsupported,
		"GetExtendedAgentCard": a2a.CodeUnsupported, "CreateTaskPushNotificationConfig": a2a.CodePushNotSupported,
		"message/send": a2a.CodeMethodNotFound,
	} {
		require.Equal(t, code, a2aCall(t, srv.URL, "1.0", method, map[string]any{}).Error.Code, method)
	}
	require.Equal(t, a2a.CodeVersionNotSupport, a2aCall(t, srv.URL, "0.3", "SendMessage", userMessage(text("hi"))).Error.Code)
	require.Equal(t, a2a.CodeInvalidParams, a2aCall(t, srv.URL, "1.0", "SendMessage", map[string]any{}).Error.Code)

	res, err := http.Post(srv.URL+"/a2a", "application/json", bytes.NewReader([]byte("{not json")))
	require.NoError(t, err)
	var bad rpcReply
	require.NoError(t, json.NewDecoder(res.Body).Decode(&bad))
	res.Body.Close()
	require.Equal(t, a2a.CodeParseError, bad.Error.Code)
	res, err = http.Get(srv.URL + "/a2a")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)

	_, statErr := os.Stat(kr)
	require.True(t, os.IsNotExist(statErr), "the A2A endpoint opened a keyring at %s", kr)
}

func TestA2A_SkillFromText(t *testing.T) {
	addr := "aether1k05ymu3zkf6lrhmufncckth8tafkw8sxytm0f9l7dy78pdmnpjvs8ke7eu"
	hash := "262C069C0000000000000000000000000000000000000000000000000008BBC6"
	for in, want := range map[string]string{
		"balance of " + addr:                  `get_balance {"address":"` + addr + `"}`,
		"is " + addr + " a validator?":        `get_miner_status {"address":"` + addr + `"}`,
		"session keys of " + addr:             `get_account_authenticators {"address":"` + addr + `"}`,
		"status of tx " + hash:                `get_transaction_status {"hash":"` + hash + `"}`,
		"quote swapping 2.5 AETH to OSMO":     `quote_swap {"amount":"2.5 AETH"}`,
		"swap 1000000uaeth":                   `quote_swap {"amount":"1000000 uaeth"}`,
		"find paid services for block pulse?": `find_services {"query":"block pulse"}`,
	} {
		skill, input, ok := skillFromText(in)
		require.True(t, ok, in)
		require.Equal(t, want, skill+" "+string(input), in)
	}
	for _, in := range []string{"hello", "quote me something", "what is aether?"} {
		_, _, ok := skillFromText(in)
		require.False(t, ok, in)
	}
}
