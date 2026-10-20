package a2a

import "encoding/json"

// The A2A v1.0 objects the JSON-RPC endpoint reads and writes.

type Role string

const (
	RoleUser  Role = "ROLE_USER"
	RoleAgent Role = "ROLE_AGENT"
)

type TaskState string

const (
	StateCompleted TaskState = "TASK_STATE_COMPLETED"
	StateFailed    TaskState = "TASK_STATE_FAILED"
	StateRejected  TaskState = "TASK_STATE_REJECTED"
)

// Part holds exactly one of Text, Data or URL.
type Part struct {
	Text      *string         `json:"text,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	URL       string          `json:"url,omitempty"`
	MediaType string          `json:"mediaType,omitempty"`
}

func TextPart(s string) Part { return Part{Text: &s, MediaType: ModeText} }

func DataPart(v json.RawMessage) Part { return Part{Data: v, MediaType: ModeJSON} }

type Message struct {
	MessageID string          `json:"messageId"`
	ContextID string          `json:"contextId,omitempty"`
	TaskID    string          `json:"taskId,omitempty"`
	Role      Role            `json:"role"`
	Parts     []Part          `json:"parts"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp string    `json:"timestamp,omitempty"`
}

type Artifact struct {
	ArtifactID string `json:"artifactId"`
	Name       string `json:"name,omitempty"`
	Parts      []Part `json:"parts"`
}

type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId,omitempty"`
	Status    TaskStatus `json:"status"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	History   []Message  `json:"history,omitempty"`
}

type SendMessageConfiguration struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
	HistoryLength       *int32   `json:"historyLength,omitempty"`
	ReturnImmediately   bool     `json:"returnImmediately,omitempty"`
}

type SendMessageRequest struct {
	Message       *Message                  `json:"message"`
	Configuration *SendMessageConfiguration `json:"configuration,omitempty"`
}

// SendMessageResponse holds exactly one of Task or Message.
type SendMessageResponse struct {
	Task    *Task    `json:"task,omitempty"`
	Message *Message `json:"message,omitempty"`
}

type GetTaskRequest struct {
	ID            string `json:"id"`
	HistoryLength *int32 `json:"historyLength,omitempty"`
}

type CancelTaskRequest struct {
	ID string `json:"id"`
}

// JSON-RPC error codes: JSON-RPC 2.0's own, then A2A's (spec §5.4).
const (
	CodeParseError        = -32700
	CodeInvalidRequest    = -32600
	CodeMethodNotFound    = -32601
	CodeInvalidParams     = -32602
	CodeInternal          = -32603
	CodeTaskNotFound      = -32001
	CodeTaskNotCancelable = -32002
	CodePushNotSupported  = -32003
	CodeUnsupported       = -32004
	CodeVersionNotSupport = -32009
)
