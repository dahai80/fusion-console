package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/config"
)

// The factory-password prefill must only reach a browser on the host
// itself — never a tailnet device coming in through tailscale serve, which
// also connects from 127.0.0.1.
func TestAuthHintIgnoresProxiedLoopback(t *testing.T) {
	hash, err := auth.HashPassword("fusion-admin-2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	s := &Server{cfg: &config.Config{Users: []config.UserEntry{{Name: "admin", PasswordHash: hash}}}}

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    bool
	}{
		{"direct loopback", "127.0.0.1:52000", nil, true},
		{"ipv6 loopback", "[::1]:52000", nil, true},
		{"tailnet address", "100.64.0.7:52000", nil, false},
		{"tailscale serve", "127.0.0.1:52000", map[string]string{"X-Forwarded-For": "100.64.0.7", "Tailscale-User-Login": "me@example.com"}, false},
		{"generic proxy", "127.0.0.1:52000", map[string]string{"Forwarded": "for=192.0.2.1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/auth/hint", nil)
			req.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			s.handleAuthHint(rec, req)
			var got struct {
				FactoryDefault bool `json:"factory_default"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.FactoryDefault != tc.want {
				t.Fatalf("factory_default = %v, want %v", got.FactoryDefault, tc.want)
			}
		})
	}
}
