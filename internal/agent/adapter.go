// Package agent adapts headless coding-agent CLIs (claude, fusion-code,
// codex, opencode) into one normalized event stream. No orchestration here —
// each Run is a single task handed to one CLI subprocess.
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
	EventDone   EventType = "done"
)

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
}

// RunOpts parameterizes one agent run.
type RunOpts struct {
	Task           string
	Workdir        string
	Model          string
	PermissionMode string
	MaxBudgetUSD   float64
	Command        string // optional executable override from settings
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
