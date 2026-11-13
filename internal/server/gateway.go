package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
)

const gatewayRepo = "dahai80/fusion-gateway"

func defaultGatewayBase() string { return "http://127.0.0.1:11432" }

// gatewayBase returns the configured gateway base URL (first enabled
// gateway-type provider) or the default local endpoint.
func (s *Server) gatewayBase() string {
	for _, p := range s.cfg.Provider {
		if p.Type == "gateway" && p.Enabled && p.BaseURL != "" {
			return strings.TrimSuffix(strings.TrimSuffix(p.BaseURL, "/v1"), "/")
		}
	}
	return defaultGatewayBase()
}

// gatewayBinaryPath is where the console installs the gateway binary.
func gatewayBinaryPath() string {
	home, _ := os.UserHomeDir()
	name := "fusion-gateway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".fusion-console", "bin", name)
}

// bundledGatewayPath returns the gateway binary shipped next to the console
// executable in release packages (fusion-console and fusion-gateway sit
// side by side). Empty string when no bundled copy exists.
func bundledGatewayPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	name := "fusion-gateway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// gatewayPortOpen probes the gateway TCP port directly (IPv4 and IPv6
// loopback). Gateways vary across versions in which HTTP routes they expose,
// so the port is the authoritative "is something running" signal.
func gatewayPortOpen(hostBase string) bool {
	host, port := "127.0.0.1", "11432"
	if h, p, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(hostBase, "http://"), "https://")); err == nil && h != "" {
		host, port = h, p
	}
	hosts := []string{host}
	if host == "localhost" {
		hosts = []string{"127.0.0.1", "::1"}
	}
	for _, h := range hosts {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(h, port), 400*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

// handleGatewayStatus probes the gateway health endpoint and reports admin
// GUI reachability.
func (s *Server) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	adminURL := base + "/admin/"
	portOpen := gatewayPortOpen(base)
	reachable := portOpen
	httpOK := false
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/api/health")
	if err == nil {
		resp.Body.Close()
		httpOK = resp.StatusCode == http.StatusOK
	}
	// Health route may 404 or differ across versions; any HTTP response on
	// the root means something is listening.
	if !httpOK {
		resp2, err2 := client.Get(base + "/")
		if err2 == nil {
			resp2.Body.Close()
			httpOK = resp2.StatusCode < 500
		}
	}
	reachable = reachable || httpOK
	installed := false
	bundled := bundledGatewayPath() != ""
	if bundled {
		installed = true
	} else if _, err := os.Stat(gatewayBinaryPath()); err == nil {
		installed = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base_url":  base,
		"admin_url": adminURL,
		"reachable": reachable,
		"port_open": portOpen,
		"http_ok":   httpOK,
		"installed": installed,
		"bundled":   bundled,
		"binary":    gatewayBinaryPath(),
		"admin_gui": true,
	})
}

// handleGatewaySSO logs into the gateway admin API server-side using the
// stored gateway admin credentials (keychain ids gateway-admin-user /
// gateway-admin-pass) and relays the admin_token session cookie to the
// browser. Cookies are host-scoped (ports are ignored), so after this relay
// opening http://host:11432/admin/ is already authenticated — single login
// via fusion-console, no second gateway login prompt.
func (s *Server) handleGatewaySSO(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	user, err := config.GetAPIKey("gateway-admin-user")
	if err != nil || user == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "gateway admin username not set — save it on the Gateway page first"})
		return
	}
	pass, err := config.GetAPIKey("gateway-admin-pass")
	if err != nil || pass == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "gateway admin password not set — save it on the Gateway page first"})
		return
	}
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		base+"/admin/api/login", bytes.NewReader(body))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		serverError(w, fmt.Errorf("gateway login unreachable: %w", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": fmt.Sprintf("gateway admin login failed (HTTP %d) — check stored credentials", resp.StatusCode)})
		return
	}

	// Relay the gateway session cookie (admin_token) to the browser so the
	// admin GUI on the gateway port is pre-authenticated.
	var token string
	for _, c := range resp.Cookies() {
		if c.Name == "admin_token" && c.Value != "" {
			token = c.Value
		}
	}
	if token == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "gateway login succeeded but returned no session cookie"})
		return
	}
	secure := strings.HasPrefix(base, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	log.Printf("server: gateway SSO login ok user=%s", user)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// provisionGatewayKey creates a fusion-gateway API key for the console via
// the gateway admin API (login with the stored admin credentials, then POST
// /admin/api/keys) and stores the raw key in the OS keychain under
// "gateway-key". When no admin credentials are stored, the packaged gateway's
// factory account (admin / fusion-admin-2026) is tried — nothing to type for
// a stock install. Used by the wizard's one-click button and by the chat/test
// paths as auto-healing when the stored key is missing.
func (s *Server) provisionGatewayKey(ctx context.Context) (rawKey, keyPrefix string, err error) {
	base := s.gatewayBase()
	user, _ := config.GetAPIKey("gateway-admin-user")
	pass, _ := config.GetAPIKey("gateway-admin-pass")
	if user == "" || pass == "" {
		user, pass = "admin", "fusion-admin-2026"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/api/login", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("gateway admin login failed (HTTP %d)", resp.StatusCode)
	}
	var token string
	for _, c := range resp.Cookies() {
		if c.Name == "admin_token" && c.Value != "" {
			token = c.Value
		}
	}
	if token == "" {
		return "", "", fmt.Errorf("gateway login returned no session cookie")
	}

	keyBody, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("fusion-console-%d", time.Now().Unix())})
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/api/keys", bytes.NewReader(keyBody))
	if err != nil {
		return "", "", err
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Cookie", "admin_token="+token)
	resp2, err := client.Do(req2)
	if err != nil {
		return "", "", fmt.Errorf("key creation request failed: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("gateway key creation returned HTTP %d", resp2.StatusCode)
	}
	var created struct {
		RawKey    string `json:"raw_key"`
		KeyPrefix string `json:"key_prefix"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&created); err != nil || created.RawKey == "" {
		return "", "", fmt.Errorf("gateway key creation returned no raw key")
	}
	if err := config.StoreAPIKey("gateway-key", created.RawKey); err != nil {
		return "", "", err
	}
	// Auto-create the console provider entry for the gateway so the brain
	// (chat) can use it immediately — one less manual step in the wizard.
	hasGateway := false
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].Type == "gateway" && s.cfg.Provider[i].Enabled {
			hasGateway = true
		}
	}
	if !hasGateway {
		model := gatewayDefaultModel(base, created.RawKey)
		s.cfg.Provider = append(s.cfg.Provider, config.Provider{
			ID:       fmt.Sprintf("gateway-%d", time.Now().Unix()),
			Name:     "fusion-gateway",
			Type:     "gateway",
			BaseURL:  base + "/v1",
			Model:    model,
			APIKeyID: "gateway-key",
			Enabled:  true,
		})
		if err := s.cfg.Save(s.cfgDir); err != nil {
			log.Printf("server: gateway provider auto-create save failed: %v", err)
		} else {
			log.Printf("server: gateway provider auto-created (base %s, model %s)", base+"/v1", model)
		}
	} else {
		// Backfill the model on an existing gateway provider created by an
		// older console version without one.
		for i := range s.cfg.Provider {
			p := &s.cfg.Provider[i]
			if p.Type == "gateway" && p.Enabled && p.Model == "" {
				p.Model = gatewayDefaultModel(base, created.RawKey)
				if err := s.cfg.Save(s.cfgDir); err != nil {
					log.Printf("server: gateway provider model backfill save failed: %v", err)
				} else {
					log.Printf("server: gateway provider model backfilled: %s", p.Model)
				}
			}
		}
	}
	log.Printf("server: gateway api key provisioned (prefix %s)", created.KeyPrefix)
	return created.RawKey, created.KeyPrefix, nil
}

// handleGatewayKeyProvision exposes provisionGatewayKey to the wizard/user
// menu. raw_key is returned once so it can be displayed for copy/save; it is
// never readable again afterwards.
func (s *Server) handleGatewayKeyProvision(w http.ResponseWriter, r *http.Request) {
	rawKey, keyPrefix, err := s.provisionGatewayKey(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key_id": "gateway-key", "key_prefix": keyPrefix, "raw_key": rawKey})
}

// gatewayAdminLogin logs into the gateway admin API with the stored admin
// credentials (keychain ids gateway-admin-user / gateway-admin-pass) or the
// packaged factory account when nothing is stored, and returns the
// admin_token session value for server-side admin API calls.
func (s *Server) gatewayAdminLogin(r *http.Request, base string) (string, error) {
	user, _ := config.GetAPIKey("gateway-admin-user")
	pass, _ := config.GetAPIKey("gateway-admin-pass")
	if user == "" || pass == "" {
		user, pass = "admin", "fusion-admin-2026"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/admin/api/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway admin login failed (HTTP %d) — check the admin credentials", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "admin_token" && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("gateway login returned no session cookie")
}

// gatewayAdminDo performs an authenticated gateway admin API request with
// the stored admin credentials and returns the upstream status code and
// body. Shared by the backend/key management proxies.
func (s *Server) gatewayAdminDo(r *http.Request, base, method, path string, payload any) (int, []byte, error) {
	token, err := s.gatewayAdminLogin(r, base)
	if err != nil {
		return 0, nil, err
	}
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Cookie", "admin_token="+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gateway request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("gateway response read failed: %w", err)
	}
	return resp.StatusCode, data, nil
}

// gatewayErrMsg extracts the gateway admin API's error message from a
// response body. The gateway writes {"error":{"message":..., "type":...}};
// some paths return a flat {"error": "..."} — accept both so the UI shows
// the real reason (e.g. "cannot delete the last enabled backend") instead
// of a bare HTTP code.
func gatewayErrMsg(body []byte, code int, what string) string {
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &nested) == nil && nested.Error.Message != "" {
		return fmt.Sprintf("gateway %s: %s (HTTP %d)", what, nested.Error.Message, code)
	}
	var flat struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &flat) == nil && flat.Error != "" {
		return fmt.Sprintf("gateway %s: %s (HTTP %d)", what, flat.Error, code)
	}
	return fmt.Sprintf("gateway %s returned HTTP %d", what, code)
}

// handleGatewayKeys lists the API keys registered in the gateway
// (GET /admin/api/keys proxied server-side).
func (s *Server) handleGatewayKeys(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	code, data, err := s.gatewayAdminDo(r, base, http.MethodGet, "/admin/api/keys", nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if code != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("gateway key list returned HTTP %d", code)})
		return
	}
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "gateway key list returned invalid JSON"})
		return
	}
	// Normalize field names across gateway versions (older builds use
	// id/key, newer use name/key_prefix) and mark the key the console
	// itself uses (keychain "gateway-key") so the UI can block deleting it.
	raw, rawErr := config.GetAPIKey("gateway-key")
	for _, k := range list {
		if v, ok := k["id"].(string); ok && k["name"] == nil {
			k["name"] = v
		}
		if v, ok := k["key"].(string); ok && k["key_prefix"] == nil {
			k["key_prefix"] = v
		}
		if rawErr == nil && raw != "" {
			if prefix, _ := k["key_prefix"].(string); prefix != "" && strings.HasPrefix(raw, prefix) {
				k["in_use"] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": list})
}

// handleGatewayKeyDelete removes an API key from the gateway
// (DELETE /admin/api/keys/{name} proxied server-side).
func (s *Server) handleGatewayKeyDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key name required"})
		return
	}
	base := s.gatewayBase()
	// Block deleting the key the console itself uses — chat, brain and
	// workers would all lose their gateway credentials.
	if raw, err := config.GetAPIKey("gateway-key"); err == nil && raw != "" {
		code, data, derr := s.gatewayAdminDo(r, base, http.MethodGet, "/admin/api/keys", nil)
		if derr == nil && code == http.StatusOK {
			var list []map[string]any
			if json.Unmarshal(data, &list) == nil {
				for _, k := range list {
					if kname, _ := k["name"].(string); kname == name {
						prefix, _ := k["key_prefix"].(string)
						if prefix == "" {
							prefix, _ = k["key"].(string)
						}
						if prefix != "" && strings.HasPrefix(raw, prefix) {
							writeJSON(w, http.StatusBadRequest, map[string]string{
								"error": "this key is used by the console itself (chat, brain, workers) — it cannot be deleted here"})
							return
						}
					}
				}
			}
		}
	}
	code, data, err := s.gatewayAdminDo(r, base, http.MethodDelete, "/admin/api/keys/"+url.PathEscape(name), nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if code != http.StatusOK && code != http.StatusNoContent {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": gatewayErrMsg(data, code, "key delete")})
		return
	}
	log.Printf("server: gateway api key %s deleted", name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGatewayBackendDelete removes a cloud backend from the gateway config
// (DELETE /admin/api/config/backends/{name} proxied server-side).
func (s *Server) handleGatewayBackendDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backend name required"})
		return
	}
	base := s.gatewayBase()
	code, data, err := s.gatewayAdminDo(r, base, http.MethodDelete, "/admin/api/config/backends/"+url.PathEscape(name), nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if code != http.StatusOK && code != http.StatusNoContent {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": gatewayErrMsg(data, code, "backend delete")})
		return
	}
	// Prune the backend's models from routing.cloud_models so the pickers
	// do not keep dead entries. The frontend passes them as ?models=; only
	// entries this console added for this backend are pruned. Failure is
	// logged inside, never fatal.
	if modelsParam := strings.Split(r.URL.Query().Get("models"), ","); len(modelsParam) > 0 && modelsParam[0] != "" {
		s.gatewayCloudModelsRemove(r, base, name, modelsParam)
	}
	log.Printf("server: gateway backend %s deleted", name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// gatewayModelItem is one selectable model for the brain pickers. Value is
// what the client must send: bare id for local (fusion-*) backends, qualified
// "backend/id" for cloud backends — the gateway pins a qualified id to its
// backend, while a bare cloud id with no models.supply entry falls through
// to the cloud_default backend (wrong target).
type gatewayModelItem struct {
	ID      string `json:"id"`
	Backend string `json:"backend"`
	Value   string `json:"value"`
	Local   bool   `json:"local"`
}

// isLocalBackend reports whether a gateway backend name is a local engine
// (fusion-mlx, fusion-model-hub, ...) rather than a cloud provider.
func isLocalBackend(name string) bool {
	return strings.HasPrefix(name, "fusion-")
}

// gatewayModelItems fetches /v1/models plus the admin backend list and
// returns the merged picker entries. Cloud models that only exist as
// routing.cloud_models entries (no backend attribution in /v1/models) are
// attributed via the backend's configured models list.
func (s *Server) gatewayModelItems(r *http.Request) ([]gatewayModelItem, error) {
	base := s.gatewayBase()
	key, err := config.GetAPIKey("gateway-key")
	if err != nil || key == "" {
		return nil, fmt.Errorf("no gateway API key stored — create one first")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID      string `json:"id"`
			Backend string `json:"backend"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil, fmt.Errorf("gateway models returned HTTP %d", resp.StatusCode)
	}

	// backend -> configured models (admin list), used to attribute
	// cloud_models-synthesized entries and to know which backend serves what.
	backendModels := map[string]string{} // model id -> backend name
	enabled := map[string]bool{}
	code, data, derr := s.gatewayAdminDo(r, base, http.MethodGet, "/admin/api/config/backends", nil)
	if derr == nil && code == http.StatusOK {
		var list []struct {
			Name    string   `json:"name"`
			Enabled bool     `json:"enabled"`
			Models  []string `json:"models"`
		}
		if json.Unmarshal(data, &list) == nil {
			for _, b := range list {
				enabled[b.Name] = b.Enabled
				for _, m := range b.Models {
					if _, taken := backendModels[m]; !taken {
						backendModels[m] = b.Name
					}
				}
			}
		}
	}

	items := make([]gatewayModelItem, 0, len(out.Data))
	seen := map[string]bool{}
	for _, m := range out.Data {
		backend := m.Backend
		if backend == "" {
			backend = backendModels[m.ID]
		}
		value := m.ID
		local := isLocalBackend(backend)
		if backend != "" && !local {
			value = backend + "/" + m.ID
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		items = append(items, gatewayModelItem{ID: m.ID, Backend: backend, Value: value, Local: local})
	}
	return items, nil
}

func (s *Server) handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	items, err := s.gatewayModelItems(r)
	if err != nil {
		if strings.Contains(err.Error(), "no gateway API key") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	values := make([]string, 0, len(items))
	for _, it := range items {
		values = append(values, it.Value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": values, "items": items})
}

// handleGatewayBackends lists the backends configured in the gateway
// (GET /admin/api/config/backends proxied server-side so the browser never
// talks to the gateway port directly). API keys come back masked.
func (s *Server) handleGatewayBackends(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	token, err := s.gatewayAdminLogin(r, base)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/admin/api/config/backends", nil)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Cookie", "admin_token="+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("backend list request failed: %v", err)})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("gateway backend list returned HTTP %d", resp.StatusCode)})
		return
	}
	var list any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "gateway backend list returned invalid JSON"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backends": list})
}

// handleGatewayBackendAdd creates a cloud backend in the gateway config via
// POST /admin/api/config/backends (server-side proxy, same admin session).
func (s *Server) handleGatewayBackendAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		BaseURL string   `json:"base_url"`
		APIKey  string   `json:"api_key"`
		Models  []string `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Type = strings.TrimSpace(req.Type)
	if req.Name == "" || req.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and type are required"})
		return
	}
	if len(req.Models) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one model name is required — the gateway can only advertise models it is told about"})
		return
	}
	base := s.gatewayBase()
	token, err := s.gatewayAdminLogin(r, base)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	payload := map[string]any{"name": req.Name, "type": req.Type, "enabled": true}
	if req.BaseURL != "" {
		payload["base_url"] = strings.TrimSpace(req.BaseURL)
	}
	if strings.TrimSpace(req.APIKey) != "" {
		payload["api_key"] = strings.TrimSpace(req.APIKey)
	}
	if len(req.Models) > 0 {
		payload["models"] = req.Models
	}
	body, _ := json.Marshal(payload)
	req2, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/admin/api/config/backends", bytes.NewReader(body))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Cookie", "admin_token="+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req2)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("backend create request failed: %v", err)})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		msg := gatewayErrMsg(body, resp.StatusCode, "backend create")
		// The gateway rejects duplicate base URLs — the usual cause is
		// re-adding a backend that already exists (e.g. without models).
		if strings.Contains(msg, "base_url conflict") || strings.Contains(msg, "already exists") {
			msg += " — delete the existing backend on the list above, then add it again with its models filled in"
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	log.Printf("server: gateway backend %s (%s) created", req.Name, req.Type)

	// Wire the models so they are callable and visible:
	// 1. routing.cloud_models — the gateway's allowlist for cloud model ids.
	//    Anthropic-type backends only ever advertise the standard claude-*
	//    catalog via ListModels, so a custom id is invisible AND blocked by
	//    the allowlist until it is listed here. Merge (never replace) with
	//    the current list.
	// 2. Verification: /v1/models aggregates each backend's LIVE ListModels
	//    (3s timeout) — if the id still does not show up, the upstream is
	//    unreachable or the key is wrong and the note says so.
	if err := s.gatewayCloudModelsMerge(r, base, token, req.Name, req.Models); err != nil {
		log.Printf("server: cloud_models merge for %s failed: %v", req.Name, err)
	}
	note := ""
	if items, merr := s.gatewayModelItems(r); merr == nil {
		found := false
		for _, m := range req.Models {
			for _, it := range items {
				if it.ID == m {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			note = "backend saved, but none of its models appear in /v1/models yet — check the base URL, API key and network reachability of " + req.BaseURL
			log.Printf("server: backend %s models %v not visible in /v1/models", req.Name, req.Models)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "note": note})
}

// gatewayCloudModelsMerge appends model ids to the gateway's
// routing.cloud_models via the admin routing endpoint (GET then PUT, so the
// existing entries — including the "claude-*" wildcard — are preserved). Only
// the ids this call actually added are recorded as owned by the backend, so a
// later delete never prunes pre-existing (user-curated) entries.
func (s *Server) gatewayCloudModelsMerge(r *http.Request, base, token, name string, models []string) error {
	code, data, err := s.gatewayAdminDo(r, base, http.MethodGet, "/admin/api/config/routing", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("routing read returned HTTP %d", code)
	}
	var cur struct {
		CloudModels []string `json:"cloud_models"`
	}
	_ = json.Unmarshal(data, &cur)
	merged := cur.CloudModels
	changed := false
	var added []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		exists := false
		for _, c := range merged {
			if c == m {
				exists = true
				break
			}
		}
		if !exists {
			merged = append(merged, m)
			added = append(added, m)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"cloud_models": merged})
	code, data, err = s.gatewayAdminDo(r, base, http.MethodPut, "/admin/api/config/routing", json.RawMessage(payload))
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("routing update returned HTTP %d: %s", code, gatewayErrMsg(data, code, "cloud_models update"))
	}
	if err := s.ownedModelsRecord(name, added); err != nil {
		log.Printf("server: recording owned cloud models for %s failed: %v", name, err)
	}
	log.Printf("server: routing.cloud_models now %d entries (added %v)", len(merged), added)
	return nil
}

// gatewayCloudModelsRemove drops model ids from routing.cloud_models when
// their backend is deleted, so the picker does not keep dead entries. Only ids
// previously added by this console for this backend are pruned — entries the
// user curated by hand (or another tool added) are never touched.
func (s *Server) gatewayCloudModelsRemove(r *http.Request, base, name string, models []string) {
	owned, err := s.ownedModelsTake(name)
	if err != nil {
		log.Printf("server: reading owned cloud models for %s failed: %v", name, err)
		return
	}
	if len(owned) == 0 {
		log.Printf("server: no console-added cloud models recorded for %s — cloud_models left untouched", name)
		return
	}
	own := map[string]bool{}
	for _, m := range owned {
		own[m] = true
	}
	var drop []string
	for _, m := range models {
		if own[m] {
			drop = append(drop, m)
		}
	}
	if len(drop) == 0 {
		return
	}
	code, data, err := s.gatewayAdminDo(r, base, http.MethodGet, "/admin/api/config/routing", nil)
	if err != nil || code != http.StatusOK {
		return
	}
	var cur struct {
		CloudModels []string `json:"cloud_models"`
	}
	if json.Unmarshal(data, &cur) != nil || len(cur.CloudModels) == 0 {
		return
	}
	dropSet := map[string]bool{}
	for _, m := range drop {
		dropSet[m] = true
	}
	merged := make([]string, 0, len(cur.CloudModels))
	for _, c := range cur.CloudModels {
		if !dropSet[c] {
			merged = append(merged, c)
		}
	}
	if len(merged) == len(cur.CloudModels) {
		return
	}
	payload, _ := json.Marshal(map[string]any{"cloud_models": merged})
	if code, _, err = s.gatewayAdminDo(r, base, http.MethodPut, "/admin/api/config/routing", json.RawMessage(payload)); err == nil && code == http.StatusOK {
		log.Printf("server: routing.cloud_models pruned after backend delete (removed %v)", drop)
	}
}

// ownedModelsPath is the state file recording which cloud_models entries this
// console added per backend — the delete path prunes only those.
func (s *Server) ownedModelsPath() string {
	return filepath.Join(s.cfgDir, "gateway-added-models.json")
}

func (s *Server) ownedModelsLoad() (map[string][]string, error) {
	data, err := os.ReadFile(s.ownedModelsPath())
	if os.IsNotExist(err) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string][]string{}, nil
	}
	return out, nil
}

func (s *Server) ownedModelsSave(m map[string][]string) error {
	data, err := json.MarshalIndent(m, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.ownedModelsPath(), data, 0o600)
}

// ownedModelsRecord appends ids to the backend's owned set (dedup).
func (s *Server) ownedModelsRecord(name string, ids []string) error {
	if name == "" || len(ids) == 0 {
		return nil
	}
	all, err := s.ownedModelsLoad()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range all[name] {
		seen[e] = true
	}
	for _, id := range ids {
		if !seen[id] {
			all[name] = append(all[name], id)
		}
	}
	return s.ownedModelsSave(all)
}

// ownedModelsTake returns and clears the backend's owned set.
func (s *Server) ownedModelsTake(name string) ([]string, error) {
	all, err := s.ownedModelsLoad()
	if err != nil || len(all[name]) == 0 {
		return nil, err
	}
	owned := all[name]
	delete(all, name)
	if err := s.ownedModelsSave(all); err != nil {
		log.Printf("server: clearing owned cloud models for %s failed: %v", name, err)
	}
	return owned, nil
}

// gatewayDefaultModel queries /v1/models with the fresh key and returns the
// first model id, so the auto-created provider has a usable model for test
// calls. Falls back to the shipped config's alias when the query fails.
func gatewayDefaultModel(base, rawKey string) string {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return "glm-5"
	}
	req.Header.Set("Authorization", "Bearer "+rawKey)
	resp, err := client.Do(req)
	if err != nil {
		return "glm-5"
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Data) == 0 {
		return "glm-5"
	}
	// Prefer a chat-capable model: skip embedding/reranker entries first.
	for _, m := range out.Data {
		low := strings.ToLower(m.ID)
		if !strings.Contains(low, "embed") && !strings.Contains(low, "bge") && !strings.Contains(low, "rerank") {
			return m.ID
		}
	}
	return out.Data[0].ID
}

// handleGatewayInstall downloads the latest fusion-gateway release for the
// current platform into ~/.fusion-console/bin and extracts the binary.
func (s *Server) handleGatewayInstall(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	// A gateway already answering on the port makes install pointless — and
	// the download path must never hang the UI when offline.
	if gatewayPortOpen(s.gatewayBase()) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "binary": gatewayBinaryPath(), "version": "running",
			"note": "gateway already running on :11432 — no install needed",
		})
		return
	}
	version := strings.TrimPrefix(r.FormValue("version"), "v")
	// Release packages ship fusion-gateway next to the console binary; use
	// that copy instead of downloading when present.
	if bundled := bundledGatewayPath(); bundled != "" {
		if err := copyGatewayFromBundled(bundled); err != nil {
			serverError(w, err, http.StatusInternalServerError)
			return
		}
		log.Printf("server: gateway installed from bundled copy %s", bundled)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "binary": gatewayBinaryPath(), "version": "bundled",
			"note": "installed from the copy bundled with this package",
		})
		return
	}
	if version == "" {
		latest, err := latestGatewayVersion(r.Context())
		if err != nil {
			serverError(w, err, http.StatusBadGateway)
			return
		}
		version = latest
	}
	asset := gatewayAssetName(runtime.GOOS, runtime.GOARCH)
	log.Printf("server: gateway install: version=%s asset=%s", version, asset)

	path, err := installGateway(r.Context(), version, asset)
	if err != nil {
		serverError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "binary": path, "version": version,
		"note": "installed; start it with your gateway config to finish setup",
	})
}

// copyGatewayFromBundled copies the package-shipped gateway binary into
// ~/.fusion-console/bin so the standard gatewayBinaryPath always resolves.
func copyGatewayFromBundled(src string) error {
	dst := gatewayBinaryPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("gateway: create install dir: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("gateway: open bundled copy: %w", err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("gateway: create temp file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("gateway: copy bundled binary: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("gateway: write temp file: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("gateway: chmod: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("gateway: finalize install: %w", err)
	}
	return nil
}

func latestGatewayVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+gatewayRepo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway: query latest release (network unreachable or GitHub blocked): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway: latest release query returned %d", resp.StatusCode)
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("gateway: parse release info: %w", err)
	}
	if out.TagName == "" {
		return "", fmt.Errorf("gateway: no releases published")
	}
	return strings.TrimPrefix(out.TagName, "v"), nil
}

func gatewayAssetName(goos, goarch string) string {
	switch goos {
	case "windows":
		return fmt.Sprintf("fusion-gateway-%s-%s.zip", goos, goarch)
	default:
		return fmt.Sprintf("fusion-gateway-%s-%s.tar.gz", goos, goarch)
	}
}

func installGateway(ctx context.Context, version, asset string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", gatewayRepo, version, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway: download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("gateway: release asset %s does not exist for v%s (check the releases page)", asset, version)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway: download %s returned %d", url, resp.StatusCode)
	}

	binPath := gatewayBinaryPath()
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return "", fmt.Errorf("gateway: create install dir: %w", err)
	}
	tmp := binPath + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("gateway: create temp file: %w", err)
	}

	switch {
	case strings.HasSuffix(asset, ".tar.gz"):
		if err := extractTarGz(resp.Body, out, "fusion-gateway"); err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: extract: %w", err)
		}
	case strings.HasSuffix(asset, ".zip"):
		// Need a seekable file for zip: buffer to temp archive first.
		archive := binPath + ".asset.zip"
		af, err := os.Create(archive)
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: buffer asset: %w", err)
		}
		if _, err := io.Copy(af, resp.Body); err != nil {
			af.Close()
			out.Close()
			os.Remove(archive)
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: download asset: %w", err)
		}
		af.Close()
		err = extractZip(archive, out, "fusion-gateway")
		os.Remove(archive)
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: extract: %w", err)
		}
	default:
		// Single raw binary asset.
		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: download: %w", err)
		}
	}
	out.Close()

	mode := os.FileMode(0o755)
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("gateway: chmod: %w", err)
	}
	if err := os.Rename(tmp, binPath); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("gateway: finalize install: %w", err)
	}
	log.Printf("server: gateway installed at %s (v%s)", binPath, version)
	return binPath, nil
}

// extractTarGz copies the named member out of a tar.gz stream.
func extractTarGz(r io.Reader, out io.Writer, member string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("member %q not found in archive", member)
		}
		if err != nil {
			return err
		}
		name := filepath.Base(hdr.Name)
		if name == member && hdr.Typeflag == tar.TypeReg {
			_, err := io.Copy(out, tr)
			return err
		}
	}
}

// extractZip copies the named member out of a zip file.
func extractZip(path string, out io.Writer, member string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) == member && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			_, cErr := io.Copy(out, rc)
			return cErr
		}
	}
	return fmt.Errorf("member %q not found in archive", member)
}
