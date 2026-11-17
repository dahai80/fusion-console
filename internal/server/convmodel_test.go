package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

func TestSetConversationModelEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), st)
	cookie := loginCookie(t, s)
	convID, err := st.CreateConversation("admin", "conv", "chat", "", "")
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}

	do := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		s.mux.ServeHTTP(rec, req)
		return rec
	}

	s.cfg.Provider = []config.Provider{{ID: "p-direct", Name: "direct", Type: "openai_compat", BaseURL: "https://x.example/v1", Enabled: true}}
	if rec := do("PUT", "/api/conversations/999/model", `{"provider_id":"p-direct","model":"m"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown conv accepted: %d", rec.Code)
	}
	if rec := do("PUT", "/api/conversations/1/model", `{"provider_id":"ghost","model":"m"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider accepted: %d", rec.Code)
	}

	if rec := do("PUT", "/api/conversations/1/model", `{"provider_id":"p-direct","model":"glm-5"}`); rec.Code != http.StatusOK {
		t.Fatalf("binding rejected: %d %s", rec.Code, rec.Body.String())
	}

	rec := do("GET", "/api/conversations", "null")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ProviderID":"p-direct"`) || !strings.Contains(rec.Body.String(), `"Model":"glm-5"`) {
		t.Fatalf("binding not in list: %d %s", rec.Code, rec.Body.String())
	}

	// Clearing: empty provider + model.
	if rec := do("PUT", "/api/conversations/1/model", `{"provider_id":"","model":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clearing rejected: %d", rec.Code)
	}
	conv, err := st.Conversation("admin", convID)
	if err != nil || conv.ProviderID != "" || conv.Model != "" {
		t.Fatalf("binding not cleared: %+v err=%v", conv, err)
	}
}
