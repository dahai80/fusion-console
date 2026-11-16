package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/dahai80/fusion-console/internal/config"
)

// atlascode provider forcing writes ~/.atlas/settings.json instead of
// injecting env vars: atlascode honors its settings-file providers over
// OPENAI_BASE_URL/OPENAI_API_KEY (verified against 0.1.120 — env is only
// consulted when no provider resolves), and the user ruled the file-modify
// approach the supported path (2026-11-16).
//
// The write is a merge: the console owns a single namespaced provider entry
// (keyed by the console provider ID) and repoints the three model roles at
// it. Everything else in the user's settings is preserved; the first write
// keeps a .fc-backup sibling. Role pools use full "provider/model" refs
// because atlascode does not normalize bare ids against the provider table.

// atlasSettingsPath returns the atlascode settings file location.
func atlasSettingsPath() string {
	if dir := strings.TrimSpace(os.Getenv("ATLAS_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".atlas", "settings.json")
}

// writeAtlasProvider merges the console-forced provider into
// ~/.atlas/settings.json and repoints the model roles at it. modelID selects
// the role pool ("" = all pool models in every role). The returned value is
// the full model ref to pass as --model.
func writeAtlasProvider(prov *config.Provider, modelID string) (string, error) {
	path := atlasSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("atlas config dir: %w", err)
	}
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return "", fmt.Errorf("atlas settings parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("atlas settings read: %w", err)
	}
	if backup := path + ".fc-backup"; !exists(backup) {
		_ = os.WriteFile(backup, mustMarshal(raw), 0o600)
	}

	// Normalize the model list: pool order, with the default model first.
	models := poolModels(prov, modelID)
	entries := make([]map[string]any, 0, len(models))
	refs := make([]string, 0, len(models))
	for _, m := range models {
		entries = append(entries, map[string]any{"id": m})
		refs = append(refs, prov.ID+"/"+m)
	}
	key, err := config.GetAPIKey(providerKeyID(prov))
	if err != nil || key == "" {
		return "", fmt.Errorf("provider %s has no stored API key", prov.ID)
	}

	providers, _ := raw["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[prov.ID] = map[string]any{
		"baseURL": strings.TrimSuffix(prov.BaseURL, "/"),
		"apiKey":  key,
		"api":     "openai-completions",
		"models":  entries,
	}
	raw["providers"] = providers

	roles, _ := raw["modelRoles"].(map[string]any)
	if roles == nil {
		roles = map[string]any{}
	}
	for _, role := range []string{"premium", "fast", "small"} {
		roles[role] = map[string]any{"provider": prov.ID, "models": refs}
	}
	raw["modelRoles"] = roles

	if err := atomicWrite(path, mustMarshal(raw)); err != nil {
		return "", err
	}
	log.Printf("server: atlas settings merged provider=%s models=%v path=%s", prov.ID, models, path)
	return prov.ID + "/" + models[0], nil
}

// poolModels returns the effective model list: requested model first, then
// the configured pool, then the default — deduplicated, order-stable.
func poolModels(prov *config.Provider, modelID string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
	}
	add(modelID)
	for _, m := range prov.Models {
		add(m)
	}
	add(prov.Model)
	if len(out) == 0 {
		out = []string{"default"}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mustMarshal(v any) []byte {
	data, _ := json.MarshalIndent(v, "", "  ")
	return data
}

// atomicWrite writes via temp file + rename so a crash never leaves a
// half-written settings file behind.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("atlas settings write: %w", err)
	}
	return os.Rename(tmp, path)
}
