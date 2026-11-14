// First-open wiring for the fusion-code integration: bind one of the
// console's configured providers to fusion-code by writing its
// ~/.fusion-code/settings.json env block (the same env family the
// orchestrator injects for worker dispatch). The key itself never leaves
// the keychain → settings file path; it is never logged or returned.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dahai80/fusion-console/internal/config"
)

// fusionCodeSettingsPath is the settings file fusion-code reads env from.
func fusionCodeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fusion-code", "settings.json"), nil
}

// codeKeyEnvNames are the env vars that count as "an API key is configured".
var codeKeyEnvNames = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// codeKeyConfigured reports whether fusion-code's settings.json env already
// carries an API key (set by the console or by hand).
func codeKeyConfigured() bool {
	path, err := fusionCodeSettingsPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	for _, name := range codeKeyEnvNames {
		if settings.Env[name] != "" {
			return true
		}
	}
	return false
}

// handleCodeKey binds a console provider to fusion-code: POST
// {"provider_id": "..."} resolves the provider + keychain key and merges the
// matching env family into ~/.fusion-code/settings.json.
func (s *Server) handleCodeKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"provider_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProviderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: provider_id required"})
		return
	}
	var prov *config.Provider
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].ID == req.ProviderID {
			prov = &s.cfg.Provider[i]
		}
	}
	if prov == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider"})
		return
	}
	if !prov.Enabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider is disabled"})
		return
	}
	key, err := config.GetAPIKey(prov.APIKeyID)
	if err != nil || key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider has no stored API key"})
		return
	}

	env := map[string]string{}
	switch prov.Type {
	case config.ProviderAnthropic:
		env["ANTHROPIC_BASE_URL"] = prov.BaseURL
		env["ANTHROPIC_AUTH_TOKEN"] = key
		if prov.Model != "" {
			env["ANTHROPIC_MODEL"] = prov.Model
		}
	case config.ProviderOpenAICompat, config.ProviderGateway:
		env["OPENAI_BASE_URL"] = prov.BaseURL
		env["OPENAI_API_KEY"] = key
		if prov.Model != "" {
			env["OPENAI_MODEL"] = prov.Model
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider type is not injectable"})
		return
	}

	path, err := fusionCodeSettingsPath()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &settings) != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings.json is not valid JSON — fix it by hand first"})
			return
		}
	}
	envBlock, _ := settings["env"].(map[string]any)
	if envBlock == nil {
		envBlock = map[string]any{}
	}
	names := make([]string, 0, len(env))
	for name, value := range env {
		envBlock[name] = value
		names = append(names, name)
	}
	settings["env"] = envBlock
	encoded, err := json.MarshalIndent(settings, "", "    ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: fusion-code key bound to provider %s (env: %v)", prov.ID, names)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "env_vars": names})
}
