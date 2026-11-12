package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
)

func newGatewayTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 11480, DataDir: dir}}
	cfg.Users = []config.UserEntry{{Name: "admin", PasswordHash: "x"}}
	return New(cfg, dir, nil)
}

func TestGatewayStatus(t *testing.T) {
	// Backend that answers /api/health.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := newGatewayTestServer(t)
	s.cfg.Provider = []config.Provider{{ID: "gw", Type: config.ProviderGateway, BaseURL: srv.URL, Enabled: true}}

	req := httptest.NewRequest("GET", "/api/gateway/status", nil)
	req.AddCookie(loginCookie(t, s))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"reachable":true`) || !contains(body, `/admin/`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestGatewayStatusDown(t *testing.T) {
	s := newGatewayTestServer(t)
	// Port 1 on 127.0.0.1: guaranteed closed.
	s.cfg.Provider = []config.Provider{{ID: "gw", Type: config.ProviderGateway, BaseURL: "http://127.0.0.1:1", Enabled: true}}

	req := httptest.NewRequest("GET", "/api/gateway/status", nil)
	req.AddCookie(loginCookie(t, s))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !contains(rec.Body.String(), `"reachable":false`) {
		t.Fatalf("expected reachable=false: %s", rec.Body.String())
	}
}

func TestGatewayAssetName(t *testing.T) {
	if got := gatewayAssetName("darwin", "arm64"); got != "fusion-gateway-darwin-arm64.tar.gz" {
		t.Errorf("darwin asset = %q", got)
	}
	if got := gatewayAssetName("windows", "amd64"); got != "fusion-gateway-windows-amd64.zip" {
		t.Errorf("windows asset = %q", got)
	}
}

// TestGatewaySSO pins the credential relay: stored admin credentials log into
// the gateway admin API and the admin_token cookie reaches the browser.
func TestGatewaySSO(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/api/login" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "admin_token", Value: "jwt-x", Path: "/"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("FUSION_CONSOLE_APIKEY_GATEWAY_ADMIN_USER", "admin")
	t.Setenv("FUSION_CONSOLE_APIKEY_GATEWAY_ADMIN_PASS", "pw")
	s := newGatewayTestServer(t)
	s.cfg.Provider = []config.Provider{{ID: "gw", Type: config.ProviderGateway, BaseURL: srv.URL, Enabled: true}}

	req := httptest.NewRequest("POST", "/api/gateway/sso", nil)
	req.AddCookie(loginCookie(t, s))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "admin_token" && c.Value == "jwt-x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin_token not relayed: %v", rec.Result().Cookies())
	}
}

// TestGatewaySSOBadCreds: gateway 401 must surface as 401, fail visible.
func TestGatewaySSOBadCreds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("FUSION_CONSOLE_APIKEY_GATEWAY_ADMIN_USER", "admin")
	t.Setenv("FUSION_CONSOLE_APIKEY_GATEWAY_ADMIN_PASS", "wrong")
	s := newGatewayTestServer(t)
	s.cfg.Provider = []config.Provider{{ID: "gw", Type: config.ProviderGateway, BaseURL: srv.URL, Enabled: true}}

	req := httptest.NewRequest("POST", "/api/gateway/sso", nil)
	req.AddCookie(loginCookie(t, s))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
