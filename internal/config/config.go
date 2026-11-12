// Package config holds fusion-console configuration: server settings, users,
// LLM providers and agent settings. API keys never live in the config file;
// they are stored in the OS keychain (see keyring.go).
package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ProviderType enumerates supported LLM provider kinds.
type ProviderType string

const (
	ProviderOpenAICompat ProviderType = "openai_compat"
	ProviderAnthropic    ProviderType = "anthropic_compat"
	ProviderGateway      ProviderType = "gateway"
)

// Provider is one configured LLM provider entry.
type Provider struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Type     ProviderType `json:"type"`
	BaseURL  string       `json:"base_url"`
	Model    string       `json:"model"`
	APIKeyID string       `json:"api_key_id"`
	Enabled  bool         `json:"enabled"`
}

// AgentSettings holds per-agent executable path and defaults.
type AgentSettings struct {
	Name           string  `json:"name"`
	Command        string  `json:"command"`
	PermissionMode string  `json:"permission_mode"`
	Enabled        bool    `json:"enabled"`
	MaxBudgetUSD   float64 `json:"max_budget_usd"`
}

// ServerConfig is the [server] section.
type ServerConfig struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	DataDir string `json:"data_dir"`
}

// Config is the on-disk config document.
type Config struct {
	Server   ServerConfig    `json:"server"`
	Users    []UserEntry     `json:"users"`
	Provider []Provider      `json:"providers"`
	Agent    []AgentSettings `json:"agents"`
}

// UserEntry is one login account. Only the bcrypt hash is stored.
type UserEntry struct {
	Name         string `json:"name"`
	PasswordHash string `json:"password_hash"`
}

// DefaultDir returns the fusion-console data directory.
func DefaultDir() string {
	if d := os.Getenv("FUSION_CONSOLE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("config: cannot resolve home directory: %v", err)
	}
	return filepath.Join(home, ".fusion-console")
}

// Path returns the config file path inside dir.
func Path(dir string) string { return filepath.Join(dir, "config.json") }

// Load reads config.json, returning a zero-value config with defaults when absent.
func Load(dir string) (*Config, error) {
	cfg := &Config{Server: ServerConfig{Host: "127.0.0.1", Port: 11480, DataDir: dir}}
	data, err := os.ReadFile(Path(dir))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", Path(dir), err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", Path(dir), err)
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 11480
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	return cfg, nil
}

// Save writes config.json atomically with 0600 permissions.
func (c *Config) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return os.Rename(tmp, Path(dir))
}

// DefaultAgents returns the built-in agent registry used on first run.
func DefaultAgents() []AgentSettings {
	return []AgentSettings{
		{Name: "claude", Command: "claude", PermissionMode: "acceptEdits", Enabled: true, MaxBudgetUSD: 5},
		{Name: "fusion-code", Command: "fusion", PermissionMode: "acceptEdits", Enabled: true, MaxBudgetUSD: 5},
		{Name: "codex", Command: "codex", PermissionMode: "dontAsk", Enabled: true, MaxBudgetUSD: 5},
		{Name: "opencode", Command: "opencode", PermissionMode: "dontAsk", Enabled: true, MaxBudgetUSD: 5},
	}
}

// User looks up a user by name.
func (c *Config) User(name string) (*UserEntry, bool) {
	name = strings.TrimSpace(name)
	for i := range c.Users {
		if c.Users[i].Name == name {
			return &c.Users[i], true
		}
	}
	return nil, false
}
