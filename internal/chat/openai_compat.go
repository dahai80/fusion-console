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

// OpenAICompatProvider calls any OpenAI-compatible /v1/chat/completions
// endpoint with stream=true (SSE). Covers cloud providers and fusion-gateway.
type OpenAICompatProvider struct {
	BaseURL string // e.g. https://api.example.com/v1
	HTTP    *http.Client
}

type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatStream implements Provider.
func (p *OpenAICompatProvider) ChatStream(ctx context.Context, model, apiKey string, messages []Message) (<-chan Event, error) {
	return p.ChatStreamTools(ctx, model, apiKey, messages, nil)
}

// ChatStreamTools implements ToolProvider. Wire format (verified through
// fusion-gateway): first tool-call delta carries id + name, arguments stream
// incrementally by index; finish_reason is "tool_calls" when the model wants
// tool execution.
func (p *OpenAICompatProvider) ChatStreamTools(ctx context.Context, model, apiKey string, messages []Message, tools []ToolDecl) (<-chan Event, error) {
	body := map[string]any{
		"model":          model,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(tools) > 0 {
		// OpenAI wire format nests the spec: {"type":"function","function":{...}}.
		// A flat entry is silently ignored by gateways — model then hallucinates.
		wire := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			wire = append(wire, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  schema,
				},
			})
		}
		body["tools"] = wire
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("chat: encode request: %w", err)
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("chat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
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
		// tool_calls accumulate by index across deltas.
		var toolBuf map[int]*ToolCall
		var toolOrder []int
		finish := ""
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				if len(toolBuf) > 0 {
					out <- Event{Type: EventToolCall, ToolCalls: finalizeToolCalls(toolBuf, toolOrder)}
				}
				out <- Event{Type: EventDone}
				return
			}
			var chunk openaiStreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				log.Printf("chat: openai_compat: skip unparsable SSE payload: %v", err)
				continue
			}
			if chunk.Error != nil {
				out <- Event{Type: EventError, Text: chunk.Error.Message}
				out <- Event{Type: EventDone}
				return
			}
			for _, ch := range chunk.Choices {
				if ch.Delta.Content != "" {
					out <- Event{Type: EventDelta, Text: ch.Delta.Content}
				}
				for _, tc := range ch.Delta.ToolCalls {
					if toolBuf == nil {
						toolBuf = map[int]*ToolCall{}
					}
					cur, ok := toolBuf[tc.Index]
					if !ok {
						cur = &ToolCall{ID: tc.ID, Name: tc.Function.Name}
						toolBuf[tc.Index] = cur
						toolOrder = append(toolOrder, tc.Index)
					}
					if tc.ID != "" {
						cur.ID = tc.ID
					}
					if tc.Function.Name != "" {
						cur.Name = tc.Function.Name
					}
					cur.Arguments += tc.Function.Arguments
				}
				if ch.FinishReason != nil {
					finish = *ch.FinishReason
				}
			}
			if chunk.Usage != nil {
				out <- Event{Type: EventUsage, TokensIn: chunk.Usage.PromptTokens, TokensOut: chunk.Usage.CompletionTokens}
			}
		}
		if err := sc.Err(); err != nil {
			out <- Event{Type: EventError, Text: fmt.Sprintf("stream read failed: %v", err)}
		}
		if finish == "tool_calls" && len(toolBuf) > 0 {
			out <- Event{Type: EventToolCall, ToolCalls: finalizeToolCalls(toolBuf, toolOrder)}
		} else if len(toolBuf) > 0 {
			log.Printf("chat: openai_compat: stream ended without finish_reason=tool_calls but %d partial tool call(s) buffered — dropped", len(toolBuf))
		}
		out <- Event{Type: EventDone}
	}()
	return out, nil
}

// finalizeToolCalls drops calls with no name or empty id (defensive), in
// first-appearance order.
func finalizeToolCalls(buf map[int]*ToolCall, order []int) []ToolCall {
	var out []ToolCall
	for _, idx := range order {
		tc := buf[idx]
		if tc.Name == "" || tc.ID == "" {
			continue
		}
		out = append(out, *tc)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}
