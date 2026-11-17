package server

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
)

// normalizeProviderURL validates and normalizes a provider base URL at save
// time. OpenAI-compatible providers (gateway and openai_compat) append
// /chat/completions directly to the base URL, so a base without the /v1 API
// root hits the wrong path at call time — heal it here, before the value is
// persisted, instead of failing on the first chat. Anthropic-compatible
// providers build /v1/messages themselves and accept both forms, so their
// URL is only trimmed.
func normalizeProviderURL(prov *config.Provider) (string, error) {
	u := strings.TrimRight(strings.TrimSpace(prov.BaseURL), "/")
	if u == "" {
		if prov.Type == config.ProviderGateway {
			return "http://127.0.0.1:11432/v1", nil
		}
		return "", fmt.Errorf("base URL required")
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("base URL %q does not parse: %v", u, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("base URL %q must use http or https", u)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("base URL %q is missing a host", u)
	}
	switch prov.Type {
	case config.ProviderGateway, config.ProviderOpenAICompat:
		if parsed.Path == "" || parsed.Path == "/" || !strings.HasSuffix(parsed.Path, "/v1") {
			healed := u + "/v1"
			log.Printf("server: provider %s base URL normalized %q -> %q", prov.ID, prov.BaseURL, healed)
			return healed, nil
		}
	}
	return u, nil
}

// verifyDefaultModel checks the provider's default model against the live
// /v1/models endpoint, so a typo like "iff/small" is rejected at save time
// instead of failing silently on the first chat.
// Verification outcomes:
//   - no default model set, or no stored API key: nothing to verify, save
//     proceeds (the key may legitimately not exist yet on a fresh provider).
//   - endpoint unreachable: cannot verify — save proceeds with a warning so
//     an offline host can still pre-configure a provider.
//   - endpoint reachable and the model is missing from the list: save is
//     rejected with the available models named in the error.
func verifyDefaultModel(ctx context.Context, prov *config.Provider, fetcher *chat.ModelList, apiKey string) (string, error) {
	model := prov.DefaultModel()
	if strings.TrimSpace(model) == "" {
		return "", nil
	}
	if strings.TrimSpace(apiKey) == "" {
		log.Printf("server: provider %s model %q not verified — no stored API key", prov.ID, model)
		return fmt.Sprintf("model %q not verified — no API key stored yet", model), nil
	}
	models, err := fetcher.Fetch(ctx, modelBaseURL(prov), apiKey)
	if err != nil {
		log.Printf("server: provider %s model %q not verified — models endpoint unreachable: %v", prov.ID, model, err)
		return fmt.Sprintf("model %q not verified — provider unreachable (%v)", model, err), nil
	}
	if len(models) == 0 {
		return "", fmt.Errorf("default model %q rejected — %s serves no models", model, prov.Name)
	}
	for _, m := range models {
		if m == model {
			return "", nil
		}
	}
	return "", fmt.Errorf("default model %q not served by %s — available: %s", model, prov.Name, strings.Join(models, ", "))
}
