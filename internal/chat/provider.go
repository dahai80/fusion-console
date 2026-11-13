// Package chat defines the Provider abstraction: one streaming chat call to
// an LLM backend (cloud OpenAI-compatible, Anthropic-compatible, or local
// fusion-gateway). All backends emit the same Event stream.
package chat

import (
	"context"
	"encoding/json"
)

// EventType enumerates chat stream event kinds.
type EventType string

const (
	EventDelta    EventType = "delta" // incremental assistant text
	EventUsage    EventType = "usage" // final usage/cost accounting
	EventError    EventType = "error"
	EventDone     EventType = "done"
	EventToolCall EventType = "tool_calls" // complete tool calls, ready to execute
)

// Event is one normalized chat stream event.
type Event struct {
	Type      EventType
	Text      string
	TokensIn  int
	TokensOut int
	CostUSD   float64
	ToolCalls []ToolCall
}

// ToolCall is one function tool call requested by the model.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON object text
}

// ToolDecl declares one tool in the request (OpenAI function format; the
// Anthropic-compat provider converts it).
type ToolDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Message is a chat transcript entry sent to the provider. Tool fields are
// optional: assistant messages may carry ToolCalls, tool results arrive as
// Role "tool" with ToolCallID set.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolProvider is implemented by providers that support native tool calls.
// Tools nil/empty = plain completion (same event stream).
type ToolProvider interface {
	ChatStreamTools(ctx context.Context, model, apiKey string, messages []Message, tools []ToolDecl) (<-chan Event, error)
}

// Provider streams one chat completion. Implementations must close the
// channel exactly once and always end with a done or error event.
type Provider interface {
	ChatStream(ctx context.Context, model, apiKey string, messages []Message) (<-chan Event, error)
}
