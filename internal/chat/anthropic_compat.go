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
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
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
	body, err := json.Marshal(map[string]any{
		"model":      model,
		"messages":   messages,
		"stream":     true,
		"max_tokens": 8192,
	})
	if err != nil {
		return nil, fmt.Errorf("chat: encode request: %w", err)
	}
	// Accept both https://host and https://host/v1 — never produce /v1/v1.
	base := strings.TrimRight(p.BaseURL, "/")
	url := base + "/v1/messages"
	if strings.HasSuffix(base, "/v1") {
		url = base + "/messages"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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
			case "content_block_delta":
				if ev.Delta.Text != "" {
					out <- Event{Type: EventDelta, Text: ev.Delta.Text}
				}
			case "message_start":
				if ev.Message.Usage.InputTokens > 0 {
					out <- Event{Type: EventUsage, TokensIn: ev.Message.Usage.InputTokens}
				}
			case "message_delta":
				if ev.Usage.OutputTokens > 0 {
					out <- Event{Type: EventUsage, TokensOut: ev.Usage.OutputTokens}
				}
			case "error":
				out <- Event{Type: EventError, Text: ev.Error.Message}
				out <- Event{Type: EventDone}
				return
			case "message_stop":
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
