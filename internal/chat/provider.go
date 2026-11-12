// Package chat defines the Provider abstraction: one streaming chat call to
// an LLM backend (cloud OpenAI-compatible, Anthropic-compatible, or local
// fusion-gateway). All backends emit the same Event stream.
package chat

import "context"

// EventType enumerates chat stream event kinds.
type EventType string

const (
	EventDelta EventType = "delta" // incremental assistant text
	EventUsage EventType = "usage" // final usage/cost accounting
	EventError EventType = "error"
	EventDone  EventType = "done"
)

// Event is one normalized chat stream event.
type Event struct {
	Type      EventType
	Text      string
	TokensIn  int
	TokensOut int
	CostUSD   float64
}

// Message is a chat transcript entry sent to the provider.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Provider streams one chat completion. Implementations must close the
// channel exactly once and always end with a done or error event.
type Provider interface {
	ChatStream(ctx context.Context, model, apiKey string, messages []Message) (<-chan Event, error)
}
