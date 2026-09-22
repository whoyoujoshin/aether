package testutil

import (
	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// MockMessageRouter is a minimal in-memory stand-in for
// baseapp.MessageRouter -- lets a test register a canned handler (or
// leave a type URL unregistered, simulating "no route exists") for a
// given message type, without needing a real running app.
type MockMessageRouter struct {
	Handlers map[string]baseapp.MsgServiceHandler
}

func NewMockMessageRouter() *MockMessageRouter {
	return &MockMessageRouter{Handlers: make(map[string]baseapp.MsgServiceHandler)}
}

func (m *MockMessageRouter) Handler(msg sdk.Msg) baseapp.MsgServiceHandler {
	return m.Handlers[sdk.MsgTypeURL(msg)]
}

func (m *MockMessageRouter) HandlerByTypeURL(typeURL string) baseapp.MsgServiceHandler {
	return m.Handlers[typeURL]
}
