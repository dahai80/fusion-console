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
			Content string `json:"content"`
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
	body, err := json.Marshal(map[string]any{
		"model":          model,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	})
	if err != nil {
		return nil, fmt.Errorf("chat: encode request: %w", err)
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				if payload == "[DONE]" {
					out <- Event{Type: EventDone}
					return
				}
				continue
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
			}
			if chunk.Usage != nil {
				out <- Event{Type: EventUsage, TokensIn: chunk.Usage.PromptTokens, TokensOut: chunk.Usage.CompletionTokens}
			}
		}
		if err := sc.Err(); err != nil {
			out <- Event{Type: EventError, Text: fmt.Sprintf("stream read failed: %v", err)}
		}
		out <- Event{Type: EventDone}
	}()
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}
