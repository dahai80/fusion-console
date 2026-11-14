// Package config holds fusion-console configuration: server settings, users,
// LLM providers and agent settings. API keys never live in the config file;
// they are stored in the OS keychain (see keyring.go).
package config

import (
	"bytes"
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
	WorkDir        string  `json:"work_dir"`    // dedicated workspace; empty = default per-agent dir
	ProviderID     string  `json:"provider_id"` // force this provider for the CLI; empty = CLI default
}

// ResolveWorkDir returns the dedicated workspace for one agent: the explicit
// override when set, otherwise ~/.fusion-console/agents/<name>. The directory
// is created on demand.
func (c *Config) ResolveWorkDir(agentName string) string {
	for _, a := range c.Agent {
		if a.Name == agentName && strings.TrimSpace(a.WorkDir) != "" {
			dir := expandHome(a.WorkDir)
			if !filepath.IsAbs(dir) {
				abs, err := filepath.Abs(dir)
				if err == nil {
					dir = abs
				}
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				log.Printf("config: mkdir agent workdir %s: %v", dir, err)
			}
			return dir
		}
	}
	dir := filepath.Join(DefaultDir(), "agents", agentName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("config: mkdir default agent workdir %s: %v", dir, err)
	}
	return dir
}

// WorkerConfig is one worker entry in the guided brain-and-workers setup.
// Several workers may share one agent type; each has its own Name (used for
// @-mention dispatch and log lines) and its own workspace directory.
type WorkerConfig struct {
	Name           string  `json:"name"`  // unique worker name; empty = legacy single worker addressed by agent type
	Agent          string  `json:"agent"` // agent CLI type (claude, fusion-code, ...)
	Enabled        bool    `json:"enabled"`
	Isolation      string  `json:"isolation"` // "worktree" | "standalone"; empty = worktree
	PermissionMode string  `json:"permission_mode"`
	MaxBudgetUSD   float64 `json:"max_budget_usd"`
	ProviderID     string  `json:"provider_id"` // force this provider for the worker CLI; empty = CLI default
	WorkDir        string  `json:"work_dir"`    // dedicated workspace; empty = ~/.fusion-console/agents/<name-or-agent>
	Repo           string  `json:"repo"`        // default repo for worktree isolation; empty = per-run repo argument
}

// OrchestratorConfig captures the onboarding wizard's brain-and-workers
// decisions. The architect session and dispatch loop are NOT implemented by
// this prototype — this section only persists what the wizard collected.
type OrchestratorConfig struct {
	Mode         string         `json:"mode"` // "off" | "confirm" | "auto"
	BrainAgent   string         `json:"brain_agent"`
	Repos        []string       `json:"repos"`
	Workers      []WorkerConfig `json:"workers"`
	Configured   bool           `json:"configured"`
	WorkerMemory string         `json:"worker_memory"` // "notes" (default) | "fusion-memory" | "off"
}

// DefaultOrchestrator returns safe wizard defaults.
func DefaultOrchestrator() OrchestratorConfig {
	return OrchestratorConfig{Mode: "confirm", BrainAgent: "claude", Repos: []string{}, Workers: []WorkerConfig{}, WorkerMemory: "notes"}
}

// ServerConfig is the [server] section.
type ServerConfig struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	DataDir string `json:"data_dir"`
}

// HarnessConfig controls the fusion-harness sidecar guarding the chat
// pipeline (compaction + per-conversation token budget).
type HarnessConfig struct {
	Enabled      bool `json:"enabled"`
	BudgetTokens int  `json:"budget_tokens"` // per conversation; 0 = unlimited
}

// RagConfig is the optional fusion-rag knowledge base integration. Nothing
// is downloaded or started until the user installs it from the Knowledge
// Base menu; the section only records the decision. The RAG UI is proxied
// at /shell/fusion-rag/ and the chat box reaches it over MCP (tools
// kb_search / kb_ask) once installed and enabled.
type RagConfig struct {
	Installed  bool   `json:"installed"`
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`        // fusion-rag HTTP port (registered 11436)
	InstallDir string `json:"install_dir"` // where the release tarball is unpacked and installed
}

// DefaultHarness returns the M1 default: on, 400k tokens per conversation.
func DefaultHarness() HarnessConfig {
	return HarnessConfig{Enabled: true, BudgetTokens: 400000}
}

// DefaultRag returns the opt-in default: nothing installed, nothing enabled.
func DefaultRag() RagConfig {
	return RagConfig{Installed: false, Enabled: false, Port: 11436, InstallDir: filepath.Join(DefaultDir(), "fusion-rag")}
}

// Config is the on-disk config document.
type Config struct {
	Server       ServerConfig       `json:"server"`
	Users        []UserEntry        `json:"users"`
	Provider     []Provider         `json:"providers"`
	Agent        []AgentSettings    `json:"agents"`
	Harness      HarnessConfig      `json:"harness"`
	Orchestrator OrchestratorConfig `json:"orchestrator"`
	Rag          RagConfig          `json:"rag"`
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

// expandHome expands a leading ~ to the user's home directory; filepath.Abs
// does not, so "~/" paths in config would otherwise resolve under cwd.
func expandHome(dir string) string {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	return dir
}

// Load reads config.json, returning a zero-value config with defaults when absent.
func Load(dir string) (*Config, error) {
	cfg := &Config{Server: ServerConfig{Host: "127.0.0.1", Port: 11480, DataDir: dir}, Orchestrator: DefaultOrchestrator()}
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
	// Harness section defaults to on when the config file does not mention
	// it at all (first run); an explicit section is honored verbatim.
	if !bytes.Contains(data, []byte("\"harness\"")) {
		cfg.Harness = DefaultHarness()
	}
	// Orchestrator section defaults likewise (guided setup not yet run).
	if !bytes.Contains(data, []byte("\"orchestrator\"")) {
		cfg.Orchestrator = DefaultOrchestrator()
	}
	// Worker memory defaults to the zero-dependency NOTES.md convention when
	// the config file does not mention it (first run / older configs).
	if !bytes.Contains(data, []byte("\"worker_memory\"")) {
		cfg.Orchestrator.WorkerMemory = "notes"
	}
	// RAG integration defaults to not-installed/not-enabled; an explicit
	// section is honored verbatim, but a missing port/install_dir is filled.
	if !bytes.Contains(data, []byte("\"rag\"")) {
		cfg.Rag = DefaultRag()
	}
	if cfg.Rag.Port == 0 {
		cfg.Rag.Port = 11436
	}
	if strings.TrimSpace(cfg.Rag.InstallDir) == "" {
		cfg.Rag.InstallDir = DefaultRag().InstallDir
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

// ResolveWorkerDir returns the dedicated workspace for one worker entry: the
// explicit override when set, otherwise ~/.fusion-console/agents/<worker-name>
// (falling back to the agent type for legacy entries without a name). The
// directory is created on demand.
func (c *Config) ResolveWorkerDir(w WorkerConfig) string {
	if strings.TrimSpace(w.WorkDir) != "" {
		dir := expandHome(w.WorkDir)
		if !filepath.IsAbs(dir) {
			abs, err := filepath.Abs(dir)
			if err == nil {
				dir = abs
			}
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("config: mkdir worker workdir %s: %v", dir, err)
		}
		return dir
	}
	id := w.Name
	if id == "" {
		id = w.Agent
	}
	dir := filepath.Join(DefaultDir(), "agents", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("config: mkdir default worker workdir %s: %v", dir, err)
	}
	return dir
}
