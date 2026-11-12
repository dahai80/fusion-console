package chat

// GatewayProvider targets fusion-gateway (:11432, OpenAI-compatible SSE).
// The gateway is one provider among many — never a hard dependency.
type GatewayProvider struct {
	OpenAICompatProvider
}

// NewGatewayProvider builds a provider pointed at the local fusion-gateway.
func NewGatewayProvider(baseURL string) *GatewayProvider {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11432/v1"
	}
	return &GatewayProvider{OpenAICompatProvider{BaseURL: baseURL}}
}
