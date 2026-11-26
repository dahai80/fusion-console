// Package agent adapts headless coding-agent CLIs (claude, fusion-code,
// codex, opencode, atlascode) into one normalized event stream. No
// orchestration here — each Run is a single task handed to one CLI subprocess.
package agent

import (
	"context"
	"io"
	"log"
	"os/exec"
	"time"
)

// EventType enumerates normalized agent stream event kinds.
type EventType string

const (
	EventText   EventType = "text"   // assistant text block
	EventTool   EventType = "tool"   // tool call name + one-line summary
	EventResult EventType = "result" // final result text
	EventUsage  EventType = "usage"  // cost/token accounting
	EventError  EventType = "error"
	EventNotice EventType = "notice" // non-fatal visibility signal (drift/version hints)
	EventDone   EventType = "done"
)

// Protocol family identifiers shared with internal/config. Adapters are
// selected by protocol (config) with a legacy fallback by agent name.
const (
	ProtocolClaudeStreamJSON = "claude-stream-json"
	ProtocolCodexJSONL       = "codex-jsonl"
	ProtocolOpencodeJSONL    = "opencode-jsonl"
)

// defaultCommands maps agent names to their CLI executable when the settings
// leave the command empty. fusion-code resolves to the standalone "fusion-code"
// binary (brew / install.sh) — never the app-managed "fusion" shim, which the
// FusionMLX desktop app points at its own bundle and must not be a dependency
// of the console.
var defaultCommands = map[string]string{
	"claude":      "claude",
	"fusion-code": "fusion-code",
	"codex":       "codex",
	"opencode":    "opencode",
	"atlascode":   "atlascode",
}

// protocolForName is the legacy name→protocol mapping used when an agent
// settings entry predates the protocol field.
func protocolForName(name string) string {
	switch name {
	case "codex":
		return ProtocolCodexJSONL
	case "opencode":
		return ProtocolOpencodeJSONL
	default:
		return ProtocolClaudeStreamJSON
	}
}

// ProtocolFor resolves the effective protocol for an agent settings entry:
// the explicit protocol when set, otherwise the legacy name mapping.
func ProtocolFor(name, protocol string) string {
	if protocol != "" {
		return protocol
	}
	return protocolForName(name)
}

// CommandFor resolves the effective executable for an agent settings entry.
func CommandFor(name, command string) string {
	if command != "" {
		return command
	}
	if def, ok := defaultCommands[name]; ok {
		return def
	}
	return name
}

// Event is one normalized agent event.
type Event struct {
	Type       EventType
	Text       string
	ToolName   string
	ToolDetail string
	SessionID  string
	CostUSD    float64
	TokensIn   int
	TokensOut  int
	// Cache token accounting (claude stream-json family). Optional: older
	// rows/CLIs leave both at zero.
	TokensCacheRead  int
	TokensCacheWrite int
}

// RunOpts parameterizes one agent run.
type RunOpts struct {
	Task           string
	Workdir        string
	Model          string
	PermissionMode string
	MaxBudgetUSD   float64
	// MaxBudgetTokens is the fallback kill switch for CLIs that never report
	// cost (e.g. atlascode on endpoints without pricing). It only arms until
	// the first positive CostUSD event arrives; 0 disables it.
	MaxBudgetTokens int
	Command         string            // optional executable override from settings
	Env             map[string]string // extra env vars for the subprocess (provider forcing)
	ExtraArgs       []string          // trailing CLI args (e.g. --mcp-config for the agent brain)
	// AppendSystemPrompt rides --append-system-prompt on claude-family CLIs
	// (caveman activation, issue #30). Other CLI families ignore it; their
	// injection rides the task text instead.
	AppendSystemPrompt string
	// Timeout caps one agent run. 0 = the 30-minute default; the server
	// passes the configured dispatch timeout so long briefs survive.
	Timeout time.Duration
	// ResumeSessionID continues a prior claude session via --resume. The
	// permission mode / model flags of THIS invocation apply to the resumed
	// process — a session started in another mode can be resumed into
	// bypass/plan. claude-family only; other adapters ignore it.
	ResumeSessionID string
}

// Adapter is one coding-agent CLI integration.
type Adapter interface {
	Name() string
	Detect() bool
	Run(ctx context.Context, opts RunOpts) (<-chan Event, error)
}

// detect reports whether an executable is on PATH and actually runs.
// LookPath alone accepts broken wrapper scripts, so probe `--version`
// with a short timeout; a non-zero exit means "not usable".
func detect(command string) bool {
	path, err := exec.LookPath(command)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		log.Printf("agent: %s found at %s but --version failed (marking unusable): %v", command, path, err)
		return false
	}
	return true
}

// NewFor builds the adapter for one agent settings entry. Protocol decides
// the parser family; name/command decide branding and the executable.
func NewFor(name, protocol, command string) Adapter {
	switch ProtocolFor(name, protocol) {
	case ProtocolCodexJSONL:
		return CodexCompatAdapter{AgentName: name, Command: CommandFor(name, command)}
	case ProtocolOpencodeJSONL:
		return OpencodeCompatAdapter{AgentName: name, Command: CommandFor(name, command)}
	default:
		return ClaudeCompatAdapter{AgentName: name, Command: CommandFor(name, command)}
	}
}
