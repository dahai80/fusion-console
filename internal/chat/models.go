package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ModelList fetches /v1/models from an OpenAI-compatible endpoint.
// Results are not cached server-side beyond a short in-process TTL cache
// keyed by base URL; the UI calls this on demand.
type ModelList struct {
	cache map[string]cachedModels
}

type cachedModels struct {
	models []string
	at     time.Time
}

// NewModelList creates a model list fetcher with cache.
func NewModelList() *ModelList {
	return &ModelList{cache: map[string]cachedModels{}}
}

// Fetch returns model IDs from baseURL (OpenAI-compatible /v1/models).
func (m *ModelList) Fetch(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	key := baseURL + "|" + apiKey
	if c, ok := m.cache[key]; ok && time.Since(c.at) < 5*time.Minute {
		return c.models, nil
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("chat: build models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat: models request %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chat: models request %s returned %d", url, resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("chat: parse models response: %w", err)
	}
	models := make([]string, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.ID != "" {
			models = append(models, d.ID)
		}
	}
	m.cache[key] = cachedModels{models: models, at: time.Now()}
	return models, nil
}
