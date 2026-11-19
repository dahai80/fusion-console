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
	Model    string       `json:"model"`  // default model; legacy single-model field
	Models   []string     `json:"models"` // model pool offered by this provider; Model stays the default
	APIKeyID string       `json:"api_key_id"`
	Enabled  bool         `json:"enabled"`
	// Lifecycle names a console-managed service behind this source:
	// "fusion-gateway" (binary the console installs/starts) or "macfabric"
	// (launchd service, macOS Apple Silicon only). Empty = external/custom,
	// no lifecycle management.
	Lifecycle string `json:"lifecycle,omitempty"`
}

// Lifecycle constants for Provider.Lifecycle (design v2 §4).
const (
	LifecycleFusionGateway = "fusion-gateway"
	LifecycleMacfabric     = "macfabric"
)

// ChatConfig is the global chat default (design v2: replaces the gateway
// brain card as the owner of "which model new conversations use").
type ChatConfig struct {
	DefaultProviderID string `json:"default_provider_id,omitempty"`
	DefaultModel      string `json:"default_model,omitempty"`
}

// DefaultModel returns the provider's default model: the legacy Model field
// when set, otherwise the first pool entry.
func (p *Provider) DefaultModel() string {
	if strings.TrimSpace(p.Model) != "" {
		return p.Model
	}
	if len(p.Models) > 0 {
		return p.Models[0]
	}
	return ""
}

// ValidateProvider normalizes and validates a provider entry before it is
// persisted. It returns a human-readable note for acceptable-but-suspicious
// input (e.g. an OpenAI-compatible URL without /v1) — callers surface it to
// the UI — and an error for input that must not be saved. The entry is
// normalized in place (base URL trailing slash trimmed).
func ValidateProvider(p *Provider) (note string, err error) {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	if p.ID == "" {
		return "", fmt.Errorf("provider id is required")
	}
	for _, r := range p.ID {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", fmt.Errorf("provider id %q: use lowercase letters, digits, - and _ only", p.ID)
		}
	}
	switch p.Type {
	case ProviderOpenAICompat, ProviderAnthropic, ProviderGateway:
	default:
		return "", fmt.Errorf("unknown provider type %q", p.Type)
	}
	if p.BaseURL == "" {
		return "", fmt.Errorf("base_url is required")
	}
	if !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
		return "", fmt.Errorf("base_url must start with http:// or https://")
	}
	p.BaseURL = strings.TrimSuffix(p.BaseURL, "/")
	hasV1 := strings.HasSuffix(p.BaseURL, "/v1")
	switch p.Type {
	case ProviderAnthropic:
		// The Anthropic wire protocol appends /v1/messages itself; a /v1
		// suffix here produces /v1/v1/messages 404s (verified quirk).
		if hasV1 {
			return "", fmt.Errorf("anthropic-compatible base_url must not end with /v1 — the protocol appends the path itself")
		}
	case ProviderOpenAICompat:
		if !hasV1 && p.Lifecycle == "" {
			// Some gateways serve at the bare host, so this stays a note;
			// the testcall decides (design §4.1A).
			note = "openai-compatible base_url does not end with /v1 — most services expect it; the test call will verify"
		}
	}
	seen := map[string]bool{}
	for _, m := range p.Models {
		m = strings.TrimSpace(m)
		if m == "" {
			return "", fmt.Errorf("model pool contains an empty entry")
		}
		if seen[m] {
			return "", fmt.Errorf("model %q appears twice in the pool", m)
		}
		seen[m] = true
	}
	return note, nil
}

// AgentSettings holds per-agent executable path and defaults.
type AgentSettings struct {
	Name           string  `json:"name"`
	Command        string  `json:"command"`
	Protocol       string  `json:"protocol"` // stream protocol family; empty = legacy mapping by name
	PermissionMode string  `json:"permission_mode"`
	Enabled        bool    `json:"enabled"`
	MaxBudgetUSD   float64 `json:"max_budget_usd"`
	// MaxBudgetTokens is the kill switch for CLIs that never report cost
	// (cost cap stays authoritative once cost data flows). 0 = disabled.
	MaxBudgetTokens int    `json:"max_budget_tokens"`
	Model           string `json:"model"`       // per-agent default model override; empty = provider default
	WorkDir         string `json:"work_dir"`    // dedicated workspace; empty = default per-agent dir
	ProviderID      string `json:"provider_id"` // force this provider for the CLI; empty = CLI default
	Builtin         bool   `json:"builtin"`     // built-in entry; re-added by merge when missing from config
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
	Isolation      string  `json:"isolation"` // "github" | "worktree" | "standalone"; empty = worktree
	PermissionMode string  `json:"permission_mode"`
	MaxBudgetUSD   float64 `json:"max_budget_usd"`
	ProviderID     string  `json:"provider_id"` // force this provider for the worker CLI; empty = CLI default
	Model          string  `json:"model"`       // per-worker model override; empty = agent settings, then provider default
	WorkDir        string  `json:"work_dir"`    // dedicated workspace; empty = ~/.fusion-console/agents/<name-or-agent>
	Repo           string  `json:"repo"`        // worktree: local repo path; github: repository URL
	RepoBranch     string  `json:"repo_branch"` // github isolation: branch to track; empty = remote HEAD
	Memory         string  `json:"memory"`      // per-worker memory mode; empty = global worker_memory
}

// OrchestratorConfig captures the onboarding wizard's brain-and-workers
// decisions. The architect session and dispatch loop are NOT implemented by
// this prototype — this section only persists what the wizard collected.
type OrchestratorConfig struct {
	Mode              string         `json:"mode"`       // "off" | "confirm" | "auto"
	BrainMode         string         `json:"brain_mode"` // "chat" (default) | "agent" — brain implementation
	BrainAgent        string         `json:"brain_agent"`
	Repos             []string       `json:"repos"`
	Workers           []WorkerConfig `json:"workers"`
	Configured        bool           `json:"configured"`
	WorkerMemory      string         `json:"worker_memory"`       // "notes" (default) | "fusion-memory" | "off"
	BrainWorkDir      string         `json:"brain_work_dir"`      // brain artifacts + worktree root; empty = <config dir>/agents
	BrainRepo         string         `json:"brain_repo"`          // brain code source: GitHub URL (synced) or local repo path (used as-is)
	BrainRepoBranch   string         `json:"brain_repo_branch"`   // branch for a GitHub brain repo; empty = remote HEAD
	BrainContextFiles []string       `json:"brain_context_files"` // local files (working conventions, repo inventory) injected into the planner prompt
	BrainProviderID   string         `json:"brain_provider_id"`   // provider the chat brain talks to; empty = first enabled gateway provider
	BrainModel        string         `json:"brain_model"`         // model the chat brain uses; empty = the provider default
	// DispatchTimeoutMinutes caps one dispatched agent run (foreground and
	// background). 0 = 5 hours; lower it to reclaim stuck slots sooner.
	DispatchTimeoutMinutes int `json:"dispatch_timeout_minutes"`
	// CavemanDefault injects the caveman skill activation ruleset into every
	// brain session and dispatched worker (issue #30). Pointer so an absent
	// key defaults to on while an explicit false stays off.
	CavemanDefault *bool  `json:"caveman_default"`
	CavemanLevel   string `json:"caveman_level"` // lite | full (default) | ultra | wenyan-*
}

// CavemanEnabled reports whether caveman activation is on: true unless the
// config explicitly sets caveman_default to false.
func (o *OrchestratorConfig) CavemanEnabled() bool {
	return o.CavemanDefault == nil || *o.CavemanDefault
}

// DefaultOrchestrator returns safe wizard defaults.
func DefaultOrchestrator() OrchestratorConfig {
	return OrchestratorConfig{Mode: "confirm", BrainAgent: "claude", Repos: []string{}, Workers: []WorkerConfig{}, WorkerMemory: "notes", CavemanDefault: boolPtr(true), CavemanLevel: "full"}
}

func boolPtr(b bool) *bool { return &b }

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

// CodeConfig is the optional fusion-code integration. Nothing is downloaded
// or started until the user installs it from the Coding Agent menu; the
// section only records the decision. The web GUI is proxied at
// /shell/fusion-code/ once installed.
type CodeConfig struct {
	Installed  bool   `json:"installed"`
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`        // fusion-code API service port (registered 11441)
	WebPort    int    `json:"web_port"`    // fusion-code web GUI port (registered 11482)
	InstallDir string `json:"install_dir"` // where the source tarball is unpacked and built
}

// DefaultCode returns the opt-in default: nothing installed, nothing enabled.
func DefaultCode() CodeConfig {
	return CodeConfig{Installed: false, Enabled: false, Port: 11441, WebPort: 11482, InstallDir: filepath.Join(DefaultDir(), "fusion-code")}
}

// DefaultHarness returns the M1 default: on, 400k tokens per conversation.
func DefaultHarness() HarnessConfig {
	return HarnessConfig{Enabled: true, BudgetTokens: 400000}
}

// MLXConfig is the optional fusion-mlx integration (macOS Apple Silicon
// only): the console can start/stop the local engine and use it as a
// same-turn chat fallback when the cloud gateway is unreachable. Off by
// default; the Models page card only renders on supported hosts.
type MLXConfig struct {
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`        // registered 11434
	InstallDir string `json:"install_dir"` // fusion-mlx repo checkout
	Model      string `json:"model"`       // chat model id served by the engine
}

// DefaultMLX returns the opt-in default: disabled, standard paths.
func DefaultMLX() MLXConfig {
	home, _ := os.UserHomeDir()
	return MLXConfig{Enabled: false, Port: 11434, InstallDir: filepath.Join(home, "fusion", "fusion-mlx")}
}

// DefaultRag returns the opt-in default: nothing installed, nothing enabled.
func DefaultRag() RagConfig {
	return RagConfig{Installed: false, Enabled: false, Port: 11436, InstallDir: filepath.Join(DefaultDir(), "fusion-rag")}
}

// AgentStudioConfig is the optional team-view integration (T4b, issue #6).
// The console observes and drives the agent-studio daemon over its UDS
// JSON-RPC socket (newline-delimited, _auth JWT enforced). No team state is
// stored in the console. The RPC apikey resolves from env first, then the
// OS keychain — never the config file.
type AgentStudioConfig struct {
	Enabled    bool   `json:"enabled"`
	SocketPath string `json:"socket_path"` // default /tmp/fusion-studio.sock
	APIKeyID   string `json:"api_key_id"`  // keychain/env reference for auth.login
	Team       string `json:"team"`        // default "default"
}

// DefaultAgentStudio returns the opt-in default: disabled.
func DefaultAgentStudio() AgentStudioConfig {
	return AgentStudioConfig{Enabled: false, SocketPath: "/tmp/fusion-studio.sock", APIKeyID: "agent-studio", Team: "default"}
}

// Config is the on-disk config document.
type Config struct {
	Server   ServerConfig    `json:"server"`
	Users    []UserEntry     `json:"users"`
	Provider []Provider      `json:"providers"`
	Agent    []AgentSettings `json:"agents"`
	Harness  HarnessConfig   `json:"harness"`
	Chat     ChatConfig      `json:"chat"`

	// builtinMerged counts built-in agent entries MergeBuiltinAgents appended
	// during Load (not serialized; the entrypoint persists the merge once).
	builtinMerged int
	Orchestrator  OrchestratorConfig `json:"orchestrator"`
	Rag           RagConfig          `json:"rag"`
	Code          CodeConfig         `json:"code"`
	AgentStudio   AgentStudioConfig  `json:"agentstudio"`
	MLX           MLXConfig          `json:"mlx"`
}

// BuiltinMergedCount reports how many built-in agent entries Load appended
// (the entrypoint persists the merge once after startup).
func (c *Config) BuiltinMergedCount() int { return c.builtinMerged }

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
	return filepath.Join(home, ".fusion", "fusion-console")
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
	// Code integration mirrors the RAG defaults: absent section = opt-in
	// defaults, present section gets missing ports/dirs filled.
	if !bytes.Contains(data, []byte("\"code\"")) {
		cfg.Code = DefaultCode()
	}
	if cfg.Code.Port == 0 {
		cfg.Code.Port = 11441
	}
	if cfg.Code.WebPort == 0 {
		cfg.Code.WebPort = 11482
	}
	if strings.TrimSpace(cfg.Code.InstallDir) == "" {
		cfg.Code.InstallDir = DefaultCode().InstallDir
	}
	// Team view (T4b): absent section = opt-in defaults; never enable by
	// default since the daemon holds no console-visible state.
	if !bytes.Contains(data, []byte("\"agentstudio\"")) {
		cfg.AgentStudio = DefaultAgentStudio()
	}
	if strings.TrimSpace(cfg.AgentStudio.SocketPath) == "" {
		cfg.AgentStudio.SocketPath = "/tmp/fusion-studio.sock"
	}
	if strings.TrimSpace(cfg.AgentStudio.Team) == "" {
		cfg.AgentStudio.Team = "default"
	}
	// fusion-mlx (optional, Apple Silicon): absent section = defaults; the
	// card only shows on macOS/arm64 and stays disabled until opted in.
	if !bytes.Contains(data, []byte("\"mlx\"")) {
		cfg.MLX = DefaultMLX()
	}
	if cfg.MLX.Port == 0 {
		cfg.MLX.Port = 11434
	}
	if strings.TrimSpace(cfg.MLX.InstallDir) == "" {
		cfg.MLX.InstallDir = DefaultMLX().InstallDir
	}
	// Built-in agents missing from older configs are appended additively so
	// new CLIs (e.g. atlascode) appear without a config migration. The count
	// is kept for the entrypoint to persist the merge once.
	cfg.builtinMerged = cfg.MergeBuiltinAgents()
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

// DefaultAgents returns the built-in agent registry used on first run. All
// entries carry Builtin so MergeBuiltinAgents can re-add missing ones to
// older configs. atlascode arms the token budget cap because its result
// events carry no cost when the endpoint has no pricing (cost cap only works
// once cost data flows).
func DefaultAgents() []AgentSettings {
	return []AgentSettings{
		{Name: "claude", Command: "claude", PermissionMode: "acceptEdits", Enabled: true, MaxBudgetUSD: 5, Builtin: true},
		{Name: "fusion-code", Command: "fusion-code", PermissionMode: "acceptEdits", Enabled: true, MaxBudgetUSD: 5, Builtin: true},
		{Name: "codex", Command: "codex", PermissionMode: "dontAsk", Enabled: true, MaxBudgetUSD: 5, Builtin: true},
		{Name: "opencode", Command: "opencode", PermissionMode: "dontAsk", Enabled: true, MaxBudgetUSD: 5, Builtin: true},
		{Name: "atlascode", Command: "atlascode", PermissionMode: "acceptEdits", Enabled: true, MaxBudgetUSD: 5, MaxBudgetTokens: 2_000_000, Builtin: true},
	}
}

// MergeBuiltinAgents appends built-in agent entries missing from the config
// (additive only — user-edited fields and disabled entries are never
// touched). Called after Load so older configs pick up new built-ins without
// a migration. Returns how many entries were added.
func (c *Config) MergeBuiltinAgents() int {
	added := 0
	for _, def := range DefaultAgents() {
		found := false
		for i := range c.Agent {
			if c.Agent[i].Name == def.Name {
				// One-time legacy migration: the old fusion-code seed pointed
				// at the "fusion" CLI wrapper; the current product ships the
				// "fusion-code" binary (brew/install.sh). Only the untouched
				// legacy value is rewritten — an explicit user override stays.
				if def.Name == "fusion-code" && c.Agent[i].Command == "fusion" {
					c.Agent[i].Command = def.Command
					log.Printf("config: migrated legacy fusion-code command \"fusion\" to %q", def.Command)
				}
				found = true
				break
			}
		}
		if !found {
			c.Agent = append(c.Agent, def)
			added++
			log.Printf("config: added built-in agent %q from defaults", def.Name)
		}
	}
	return added
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
