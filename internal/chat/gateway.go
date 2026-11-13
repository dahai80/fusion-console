package chat

import (
	"log"
	"net/url"
	"strings"
)

// GatewayProvider targets fusion-gateway (:11432, OpenAI-compatible SSE).
// The gateway is one provider among many — never a hard dependency.
type GatewayProvider struct {
	OpenAICompatProvider
}

// normalizeGatewayBaseURL ensures the base URL carries the /v1 API root.
// OpenAICompatProvider appends /chat/completions directly to the base URL, so
// a base like "http://127.0.0.1:11432" hits /chat/completions and the gateway
// answers 404. Gateway-type providers always speak the /v1 protocol, so a
// missing root is healed here instead of failing at call time.
func normalizeGatewayBaseURL(baseURL string) string {
	u := strings.TrimSpace(baseURL)
	if u == "" {
		return "http://127.0.0.1:11432/v1"
	}
	u = strings.TrimRight(u, "/")
	parsed, err := url.Parse(u)
	if err != nil {
		// Unparseable URL: leave as-is, the HTTP call will surface the error.
		return u
	}
	if parsed.Path == "" || parsed.Path == "/" {
		healed := u + "/v1"
		log.Printf("chat: healed gateway provider base_url %q -> %q", baseURL, healed)
		return healed
	}
	// Custom path already present: only heal when it lacks the /v1 root.
	if !strings.HasSuffix(parsed.Path, "/v1") {
		healed := u + "/v1"
		log.Printf("chat: healed gateway provider base_url %q -> %q", baseURL, healed)
		return healed
	}
	return u
}

// NewGatewayProvider builds a provider pointed at the local fusion-gateway.
func NewGatewayProvider(baseURL string) *GatewayProvider {
	return &GatewayProvider{OpenAICompatProvider{BaseURL: normalizeGatewayBaseURL(baseURL)}}
}
