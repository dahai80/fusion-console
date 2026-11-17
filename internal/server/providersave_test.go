package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
)

func TestNormalizeProviderURL(t *testing.T) {
	cases := []struct {
		name    string
		ptype   config.ProviderType
		base    string
		want    string
		wantErr bool
	}{
		{"gateway default empty", config.ProviderGateway, "", "http://127.0.0.1:11432/v1", false},
		{"gateway missing /v1", config.ProviderGateway, "http://127.0.0.1:11432", "http://127.0.0.1:11432/v1", false},
		{"gateway trailing slash", config.ProviderGateway, "http://127.0.0.1:11432/", "http://127.0.0.1:11432/v1", false},
		{"gateway already /v1", config.ProviderGateway, "http://127.0.0.1:11432/v1", "http://127.0.0.1:11432/v1", false},
		{"gateway custom path healed", config.ProviderGateway, "http://127.0.0.1:11432/api", "http://127.0.0.1:11432/api/v1", false},
		{"openai missing /v1", config.ProviderOpenAICompat, "https://api.example.com", "https://api.example.com/v1", false},
		{"openai already /v1", config.ProviderOpenAICompat, "https://api.example.com/v1", "https://api.example.com/v1", false},
		{"anthropic left alone", config.ProviderAnthropic, "https://api.anthropic.com", "https://api.anthropic.com", false},
		{"no scheme", config.ProviderOpenAICompat, "api.example.com/v1", "", true},
		{"wrong scheme", config.ProviderOpenAICompat, "ftp://api.example.com", "", true},
		{"no host", config.ProviderOpenAICompat, "http:///v1", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := &config.Provider{ID: "p", Type: tc.ptype, BaseURL: tc.base}
			got, err := normalizeProviderURL(prov)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeProviderURL(%q) = %q, want error", tc.base, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeProviderURL(%q) error: %v", tc.base, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeProviderURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

func TestVerifyDefaultModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5"},{"id":"iff/small"}]}`))
	}))
	defer srv.Close()

	fetcher := chat.NewModelList()
	gw := &config.Provider{ID: "gw", Name: "gw", Type: config.ProviderGateway, BaseURL: srv.URL + "/v1", Model: "glm-5"}
	if _, err := verifyDefaultModel(context.Background(), gw, fetcher, "k"); err != nil {
		t.Fatalf("known model rejected: %v", err)
	}

	bad := &config.Provider{ID: "gw", Name: "gw", Type: config.ProviderGateway, BaseURL: srv.URL + "/v1", Model: "iff/missing"}
	if _, err := verifyDefaultModel(context.Background(), bad, fetcher, "k"); err == nil || !strings.Contains(err.Error(), "iff/missing") {
		t.Fatalf("unknown model accepted, err=%v", err)
	}

	noModel := &config.Provider{ID: "gw", Name: "gw", Type: config.ProviderGateway, BaseURL: srv.URL + "/v1"}
	if _, err := verifyDefaultModel(context.Background(), noModel, fetcher, "k"); err != nil {
		t.Fatalf("empty default should skip verification: %v", err)
	}

	noKey := &config.Provider{ID: "gw", Name: "gw", Type: config.ProviderGateway, BaseURL: srv.URL + "/v1", Model: "glm-5"}
	warning, err := verifyDefaultModel(context.Background(), noKey, fetcher, "")
	if err != nil || warning == "" {
		t.Fatalf("missing key should warn, not fail: warning=%q err=%v", warning, err)
	}

	down := &config.Provider{ID: "gw", Name: "gw", Type: config.ProviderGateway, BaseURL: "http://127.0.0.1:1/v1", Model: "glm-5"}
	warning, err = verifyDefaultModel(context.Background(), down, chat.NewModelList(), "k")
	if err != nil || warning == "" {
		t.Fatalf("unreachable endpoint should warn, not fail: warning=%q err=%v", warning, err)
	}
}

func TestHandleSaveProviderValidation(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_APIKEY_GATEWAY_KEY", "k")
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	cookie := loginCookie(t, s)

	do := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/settings/providers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		s.mux.ServeHTTP(rec, req)
		return rec
	}

	// Bad URL rejected inline.
	rec := do(`{"id":"p1","name":"p1","type":"openai_compat","base_url":"not-a-url","enabled":true}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "base URL") {
		t.Fatalf("bad URL accepted: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Existing default model verified against a live /v1/models endpoint.
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5"}]}`))
	}))
	defer modelsSrv.Close()
	rec = do(`{"id":"p2","name":"p2","type":"gateway","base_url":"` + modelsSrv.URL + `","model":"glm-5","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid gateway provider rejected: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = do(`{"id":"p3","name":"p3","type":"gateway","base_url":"` + modelsSrv.URL + `","model":"iff/missing","enabled":true}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "iff/missing") {
		t.Fatalf("missing model accepted: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Missing /v1 normalized before persisting.
	rec = do(`{"id":"p4","name":"p4","type":"openai_compat","base_url":"http://127.0.0.1:9","model":"","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("normalized provider rejected: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var saved *config.Provider
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].ID == "p4" {
			saved = &s.cfg.Provider[i]
		}
	}
	if saved == nil || saved.BaseURL != "http://127.0.0.1:9/v1" {
		t.Fatalf("normalized URL not persisted: %+v", saved)
	}
}
