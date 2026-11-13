package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// skipIfNoHarness skips when no python with fusion_harness is available
// (CI hosts without the monorepo venv).
func skipIfNoHarness(t *testing.T) {
	t.Helper()
	py := os.Getenv("FUSION_CONSOLE_HARNESS_PYTHON")
	if py == "" {
		wd, _ := os.Getwd()
		for _, cand := range []string{
			// Monorepo layout: fusion-console/<pkg> -> ../.. = fusion-console,
			// monorepo venv sits one level above the repo root.
			filepath.Join(wd, "..", "..", "..", ".venv", "bin", "python"),
			"python3",
		} {
			if _, err := os.Stat(cand); err == nil || cand == "python3" {
				cmd := exec.Command(cand, "-c", "import fusion_harness")
				if cmd.Run() == nil {
					py = cand
					break
				}
			}
		}
	}
	if py == "" {
		t.Skip("no python with fusion_harness available")
	}
	t.Setenv("FUSION_CONSOLE_HARNESS_PYTHON", py)
}

func TestClientPreLLMCompactionAndBudget(t *testing.T) {
	skipIfNoHarness(t)
	// Resolve sidecar from the repo layout (test runs in internal/harness).
	wd, _ := os.Getwd()
	t.Setenv("FUSION_CONSOLE_HARNESS_SIDECAR", filepath.Join(wd, "..", "..", "sidecar", "harnessd.py"))

	c := New()
	if c.disabled {
		t.Fatal("client disabled despite available python")
	}
	defer c.Close()

	msgs := []Message{
		{Role: "user", Content: strings.Repeat("hello ", 9000)},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "final question"},
	}
	proceed, reason, out, err := c.PreLLM(1, 1000000, "glm-5", msgs)
	if err != nil || !proceed {
		t.Fatalf("pre_llm: proceed=%v reason=%q err=%v", proceed, reason, err)
	}
	if len(out) == 0 {
		t.Fatal("empty messages from pre_llm")
	}

	// Tiny budget: after one usage report the next call must be denied.
	if err := c.PostLLM(2, 10, 10); err != nil {
		t.Fatalf("post_llm: %v", err)
	}
	proceed, reason, _, err = c.PreLLM(2, 10, "glm-5", []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("pre_llm: %v", err)
	}
	if proceed {
		t.Fatal("budget breach was not denied")
	}
	if reason == "" {
		t.Fatal("deny reason empty")
	}
}

func TestClientToolAdmit(t *testing.T) {
	skipIfNoHarness(t)
	wd, _ := os.Getwd()
	t.Setenv("FUSION_CONSOLE_HARNESS_SIDECAR", filepath.Join(wd, "..", "..", "sidecar", "harnessd.py"))

	c := New()
	defer c.Close()
	// Orchestrator tools are allowlisted; anything else is denied.
	allowed, _, err := c.ToolAdmit(3, 0, "run_agent", map[string]any{"agent": "claude"})
	if err != nil {
		t.Fatalf("tool_admit run_agent: %v", err)
	}
	if !allowed {
		t.Fatal("run_agent denied — allowlist broken")
	}
	allowed, reason, err := c.ToolAdmit(3, 0, "rm_rf", map[string]any{})
	if err != nil {
		t.Fatalf("tool_admit rogue: %v", err)
	}
	if allowed {
		t.Fatal("rogue tool allowed — deny-first violated")
	}
	if reason == "" {
		t.Fatal("deny reason empty")
	}
}
