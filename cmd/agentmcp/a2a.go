package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/whoyoujoshin/aether/a2a"
)

// The A2A v1.0 JSON-RPC endpoint (/a2a on --http, docs/A2A.md): the
// public server's read-only tools as A2A skills. Like the public MCP
// server it never opens the keyring. Every task finishes inside the
// SendMessage call, so nothing streams and nothing can be canceled;
// finished tasks are kept a while so GetTask can return them.

const (
	a2aMaxBody  = 64 << 10
	a2aKeep     = time.Hour
	a2aMaxTasks = 1000
)

// a2aSkills run each skill from its tool's own input.
var a2aSkills = map[string]func(context.Context, json.RawMessage) (any, error){
	"get_balance":                a2aRun(coded(toolGetBalancePublic)),
	"get_miner_status":           a2aRun(coded(toolGetMinerStatusPublic)),
	"get_account_authenticators": a2aRun(coded(toolGetAccountAuthenticatorsPublic)),
	"get_transaction_status":     a2aRun(coded(toolGetTransactionStatus)),
	"find_services":              a2aRun(coded(toolFindServicesPublic)),
	"quote_swap":                 a2aRun(coded(toolQuoteSwap)),
	"get_inflow_caps":            a2aRun(coded(toolGetInflowCaps)),
}

func a2aRun[In, Out any](h mcp.ToolHandlerFor[In, Out]) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in In
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, newError(codeInvalidArgument, "input: "+err.Error())
			}
		}
		_, out, err := h(ctx, nil, in)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

type a2aTasks struct {
	mu    sync.Mutex
	tasks map[string]a2aKept
	order []string
}

type a2aKept struct {
	task a2a.Task
	at   time.Time
}

var a2aStore = &a2aTasks{tasks: map[string]a2aKept{}}

func (s *a2aTasks) put(t a2a.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for len(s.order) > 0 && (len(s.order) >= a2aMaxTasks || now.Sub(s.tasks[s.order[0]].at) > a2aKeep) {
		delete(s.tasks, s.order[0])
		s.order = s.order[1:]
	}
	s.tasks[t.ID] = a2aKept{t, now}
	s.order = append(s.order, t.ID)
}

func (s *a2aTasks) get(id string) (a2a.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.tasks[id]
	if !ok || time.Since(k.at) > a2aKeep {
		return a2a.Task{}, false
	}
	return k.task, true
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func handleA2A(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "A2A JSON-RPC: POST a request; the Agent Card is at /.well-known/agent-card.json", http.StatusMethodNotAllowed)
		return
	}
	reply := func(id json.RawMessage, result any, e *rpcError) {
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: e})
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, a2aMaxBody+1))
	if err != nil || len(body) > a2aMaxBody {
		reply(nil, nil, &rpcError{Code: a2a.CodeInvalidRequest, Message: "request body too large or unreadable"})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		reply(nil, nil, &rpcError{Code: a2a.CodeParseError, Message: "not JSON: " + err.Error()})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		reply(req.ID, nil, &rpcError{Code: a2a.CodeInvalidRequest, Message: `a JSON-RPC 2.0 request needs "jsonrpc": "2.0" and a method`})
		return
	}
	// The version comes as a header or a query parameter. A missing one
	// is taken as 1.0: version 0.3 clients use other method names, which
	// get CodeMethodNotFound below.
	v := r.Header.Get("A2A-Version")
	if v == "" {
		v = r.URL.Query().Get("A2A-Version")
	}
	if v != "" && v != a2a.ProtocolVersion && v != "1" {
		reply(req.ID, nil, &rpcError{Code: a2a.CodeVersionNotSupport, Message: "this agent speaks A2A " + a2a.ProtocolVersion + ", not " + v})
		return
	}
	result, e := a2aDispatch(r.Context(), req.Method, req.Params)
	reply(req.ID, result, e)
}

func a2aDispatch(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "SendMessage":
		var p a2a.SendMessageRequest
		if err := json.Unmarshal(params, &p); err != nil || p.Message == nil || len(p.Message.Parts) == 0 {
			return nil, &rpcError{Code: a2a.CodeInvalidParams, Message: "params must be a SendMessageRequest with a message that has parts"}
		}
		return a2aSend(ctx, p), nil
	case "GetTask":
		var p a2a.GetTaskRequest
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &rpcError{Code: a2a.CodeInvalidParams, Message: "params must be a GetTaskRequest with an id"}
		}
		t, ok := a2aStore.get(p.ID)
		if !ok {
			return nil, &rpcError{Code: a2a.CodeTaskNotFound, Message: "no task " + p.ID + " (tasks are kept for an hour)"}
		}
		return trimHistory(t, p.HistoryLength), nil
	case "CancelTask":
		var p a2a.CancelTaskRequest
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &rpcError{Code: a2a.CodeInvalidParams, Message: "params must be a CancelTaskRequest with an id"}
		}
		if _, ok := a2aStore.get(p.ID); !ok {
			return nil, &rpcError{Code: a2a.CodeTaskNotFound, Message: "no task " + p.ID}
		}
		return nil, &rpcError{Code: a2a.CodeTaskNotCancelable, Message: "every task here finishes inside SendMessage, so there is nothing left to cancel"}
	case "SendStreamingMessage", "SubscribeToTask":
		return nil, &rpcError{Code: a2a.CodeUnsupported, Message: "streaming is not supported (capabilities.streaming is false)"}
	case "ListTasks":
		return nil, &rpcError{Code: a2a.CodeUnsupported, Message: "tasks are not listed; GetTask returns a task by id"}
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig", "ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		return nil, &rpcError{Code: a2a.CodePushNotSupported, Message: "push notifications are not supported"}
	case "GetExtendedAgentCard":
		return nil, &rpcError{Code: a2a.CodeUnsupported, Message: "there is no extended agent card"}
	}
	return nil, &rpcError{Code: a2a.CodeMethodNotFound, Message: "unknown method " + method + " (A2A 1.0 names, e.g. SendMessage, GetTask)"}
}

// a2aSend runs the skill a message asks for and returns the finished
// task, or a message explaining how to ask.
func a2aSend(ctx context.Context, p a2a.SendMessageRequest) a2a.SendMessageResponse {
	in := *p.Message
	if in.MessageID == "" {
		in.MessageID = newA2AID()
	}
	skill, input, ok := skillFromMessage(in)
	if !ok {
		help := a2aHelp()
		return a2a.SendMessageResponse{Message: &a2a.Message{MessageID: newA2AID(), ContextID: in.ContextID, Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart(help)}}}
	}
	ctxID := in.ContextID
	if ctxID == "" {
		ctxID = newA2AID()
	}
	task := a2a.Task{ID: newA2AID(), ContextID: ctxID, History: []a2a.Message{in}}
	task.History[0].ContextID, task.History[0].TaskID = ctxID, task.ID
	now := time.Now().UTC().Format(time.RFC3339)

	run, known := a2aSkills[skill]
	if !known {
		task.Status = a2a.TaskStatus{State: a2a.StateRejected, Timestamp: now, Message: agentText(task, "unknown skill "+skill+"; "+a2aHelp())}
		a2aStore.put(task)
		return a2a.SendMessageResponse{Task: &task}
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := run(callCtx, input)
	if err != nil {
		var ae *agentError
		if !errors.As(err, &ae) {
			ae = newError(codeInternal, err.Error())
		}
		bz, _ := json.Marshal(map[string]any{"error": ae})
		task.Status = a2a.TaskStatus{State: a2a.StateFailed, Timestamp: now, Message: agentText(task, ae.Code+": "+ae.Message)}
		task.Artifacts = []a2a.Artifact{{ArtifactID: newA2AID(), Name: skill + " error", Parts: []a2a.Part{a2a.DataPart(bz)}}}
		a2aStore.put(task)
		return a2a.SendMessageResponse{Task: &task}
	}
	bz, _ := json.Marshal(out)
	parts := []a2a.Part{a2a.DataPart(bz)}
	if p.Configuration != nil && len(p.Configuration.AcceptedOutputModes) > 0 && !contains(p.Configuration.AcceptedOutputModes, a2a.ModeJSON) {
		parts = []a2a.Part{a2a.TextPart(string(bz))}
	}
	task.Status = a2a.TaskStatus{State: a2a.StateCompleted, Timestamp: now}
	task.Artifacts = []a2a.Artifact{{ArtifactID: newA2AID(), Name: skill, Parts: parts}}
	a2aStore.put(task)
	t := task
	if p.Configuration != nil {
		t = trimHistory(task, p.Configuration.HistoryLength)
	}
	return a2a.SendMessageResponse{Task: &t}
}

func agentText(t a2a.Task, s string) *a2a.Message {
	return &a2a.Message{MessageID: newA2AID(), ContextID: t.ContextID, TaskID: t.ID, Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart(s)}}
}

func trimHistory(t a2a.Task, n *int32) a2a.Task {
	if n != nil && int(*n) < len(t.History) {
		if *n <= 0 {
			t.History = nil
		} else {
			t.History = t.History[len(t.History)-int(*n):]
		}
	}
	return t
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func newA2AID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// skillFromMessage reads which skill a message asks for: a data part
// {"skill": ..., "input": {...}} (or the skill in the message's metadata
// and the input as the data part), else a plain-text request.
func skillFromMessage(m a2a.Message) (string, json.RawMessage, bool) {
	var meta struct {
		Skill string `json:"skill"`
	}
	_ = json.Unmarshal(m.Metadata, &meta)
	var text []string
	for _, p := range m.Parts {
		if len(p.Data) > 0 {
			var d struct {
				Skill string          `json:"skill"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(p.Data, &d) == nil && d.Skill != "" {
				return d.Skill, d.Input, true
			}
			if meta.Skill != "" {
				return meta.Skill, p.Data, true
			}
		}
		if p.Text != nil {
			text = append(text, *p.Text)
		}
	}
	if len(text) == 0 {
		return "", nil, false
	}
	return skillFromText(strings.Join(text, " "))
}

var (
	textAddress = regexp.MustCompile(`aether1[02-9ac-hj-np-z]{38,90}`)
	textHash    = regexp.MustCompile(`\b[0-9A-Fa-f]{64}\b`)
	textAmount  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(u?aeth)\b`)
	// The words a directory request is made of, dropped to leave its query.
	textServiceWords = regexp.MustCompile(`\b(find|search|list|services|service|directory|apis|api|paid|for|the|me|show|any)\b`)
)

// skillFromText maps a plain request onto a skill. It only recognizes
// what it is sure of; anything else gets the help message.
func skillFromText(s string) (string, json.RawMessage, bool) {
	low := strings.ToLower(s)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}
	input := func(v any) json.RawMessage { bz, _ := json.Marshal(v); return bz }
	if h := textHash.FindString(s); h != "" {
		return "get_transaction_status", input(map[string]string{"hash": h}), true
	}
	if has("inflow", "onto aether", "room left") {
		return "get_inflow_caps", input(map[string]string{}), true
	}
	if has("quote", "swap", "osmo") {
		if m := textAmount.FindStringSubmatch(s); m != nil {
			unit := "AETH"
			if strings.EqualFold(m[2], "uaeth") {
				unit = "uaeth"
			}
			return "quote_swap", input(map[string]string{"amount": m[1] + " " + unit}), true
		}
		return "", nil, false
	}
	if a := textAddress.FindString(s); a != "" {
		switch {
		case has("authenticator", "session key", "guardian"):
			return "get_account_authenticators", input(map[string]string{"address": a}), true
		case has("miner", "mining", "validator", "seat", "epoch", "escrow"):
			return "get_miner_status", input(map[string]string{"address": a}), true
		}
		return "get_balance", input(map[string]string{"address": a}), true
	}
	if has("service", "directory", "api") {
		q := textServiceWords.ReplaceAllString(low, " ")
		q = strings.Join(strings.Fields(strings.Trim(q, " ?.!,")), " ")
		return "find_services", input(map[string]string{"query": q}), true
	}
	return "", nil, false
}

func a2aHelp() string {
	var b strings.Builder
	b.WriteString(`Ask for one of these skills, in plain text or as a data part {"skill": "<id>", "input": {...}}:`)
	for _, s := range a2a.Skills {
		b.WriteString("\n- " + s.ID + ": " + s.Description)
	}
	b.WriteString("\nThis agent is read-only: it holds no key and cannot pay or send.")
	return b.String()
}
