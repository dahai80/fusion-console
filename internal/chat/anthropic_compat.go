package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// AnthropicCompatProvider calls an Anthropic-compatible /v1/messages endpoint
// with stream=true (SSE).
type AnthropicCompatProvider struct {
	BaseURL    string // e.g. https://api.anthropic.com
	APIVersion string
	HTTP       *http.Client
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		StopReason  string `json:"stop_reason"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Message struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatStream implements Provider.
func (p *AnthropicCompatProvider) ChatStream(ctx context.Context, model, apiKey string, messages []Message) (<-chan Event, error) {
	return p.ChatStreamTools(ctx, model, apiKey, messages, nil)
}

// anthropicBlock is one content block in a request message.
type anthropicBlock struct {
	Type      string          `json:"type"` // text | tool_use | tool_result
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   string          `json:"content,omitempty"`     // tool_result
}

type anthropicReqMsg struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

// toAnthropicMessages converts the unified Message list (OpenAI-style tool
// fields) into Anthropic content blocks. System messages move to the
// top-level system field.
func toAnthropicMessages(messages []Message) (msgs []anthropicReqMsg, system string) {
	for _, m := range messages {
		switch m.Role {
		case "system":
			if system != "" {
				system += "\n"
			}
			system += m.Content
		case "tool":
			msgs = append(msgs, anthropicReqMsg{Role: "user", Content: []anthropicBlock{{
				Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content,
			}}})
		default:
			var blocks []anthropicBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := json.RawMessage(tc.Arguments)
				if !json.Valid(args) {
					args = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: args})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: ""})
			}
			role := m.Role
			if role != "user" {
				role = "assistant"
			}
			msgs = append(msgs, anthropicReqMsg{Role: role, Content: blocks})
		}
	}
	return msgs, system
}

// ChatStreamTools implements ToolProvider. Streaming tool calls arrive as
// content_block_start (tool_use, id+name) + content_block_delta (partial_json
// input arguments); stop_reason "tool_use" on message_delta.
func (p *AnthropicCompatProvider) ChatStreamTools(ctx context.Context, model, apiKey string, messages []Message, tools []ToolDecl) (<-chan Event, error) {
	msgs, system := toAnthropicMessages(messages)
	body := map[string]any{
		"model":      model,
		"messages":   msgs,
		"stream":     true,
		"max_tokens": 8192,
	}
	if system != "" {
		body["system"] = system
	}
	if len(tools) > 0 {
		conv := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			conv = append(conv, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		body["tools"] = conv
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("chat: encode request: %w", err)
	}
	// Accept both https://host and https://host/v1 — never produce /v1/v1.
	base := strings.TrimRight(p.BaseURL, "/")
	url := base + "/v1/messages"
	if strings.HasSuffix(base, "/v1") {
		url = base + "/messages"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("chat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", orDefault(p.APIVersion, "2023-06-01"))
	req.Header.Set("Accept", "text/event-stream")

	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat: request %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("chat: provider %s returned %d: %s", url, resp.StatusCode, truncate(buf.String(), 300))
	}

	out := make(chan Event, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var toolCalls []ToolCall
		stopReason := ""
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			var ev anthropicStreamEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				log.Printf("chat: anthropic_compat: skip unparsable SSE payload: %v", err)
				continue
			}
			switch ev.Type {
			case "content_block_start":
				if ev.ContentBlock.Type == "tool_use" {
					toolCalls = append(toolCalls, ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name})
				}
			case "content_block_delta":
				if ev.Delta.Text != "" {
					out <- Event{Type: EventDelta, Text: ev.Delta.Text}
				}
				if ev.Delta.PartialJSON != "" && len(toolCalls) > 0 {
					toolCalls[len(toolCalls)-1].Arguments += ev.Delta.PartialJSON
				}
			case "message_start":
				if ev.Message.Usage.InputTokens > 0 {
					out <- Event{Type: EventUsage, TokensIn: ev.Message.Usage.InputTokens}
				}
			case "message_delta":
				if ev.Delta.StopReason != "" {
					stopReason = ev.Delta.StopReason
				}
				if ev.Usage.OutputTokens > 0 {
					out <- Event{Type: EventUsage, TokensOut: ev.Usage.OutputTokens}
				}
			case "error":
				out <- Event{Type: EventError, Text: ev.Error.Message}
				out <- Event{Type: EventDone}
				return
			case "message_stop":
				if stopReason == "tool_use" && len(toolCalls) > 0 {
					out <- Event{Type: EventToolCall, ToolCalls: toolCalls}
				} else if len(toolCalls) > 0 {
					log.Printf("chat: anthropic_compat: stream ended with stop_reason=%q but %d tool call(s) buffered — dropped", stopReason, len(toolCalls))
				}
				out <- Event{Type: EventDone}
				return
			}
		}
		if err := sc.Err(); err != nil {
			out <- Event{Type: EventError, Text: fmt.Sprintf("stream read failed: %v", err)}
		}
		out <- Event{Type: EventDone}
	}()
	return out, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
