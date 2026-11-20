package harness

import (
	"errors"
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
	pre, err := c.PreLLM(1, 1000000, "glm-5", msgs)
	proceed, reason, out := pre.Proceed, pre.Reason, pre.Messages
	if err != nil || !proceed {
		t.Fatalf("pre_llm: proceed=%v reason=%q err=%v", proceed, reason, err)
	}
	if len(out) == 0 {
		t.Fatal("empty messages from pre_llm")
	}

	// Tiny budget: after one usage report the next call must be denied.
	if _, _, err := c.PostLLM(2, 10, 10); err != nil {
		t.Fatalf("post_llm: %v", err)
	}
	pre2, err := c.PreLLM(2, 10, "glm-5", []Message{{Role: "user", Content: "x"}})
	proceed, reason = pre2.Proceed, pre2.Reason
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
	res, err := c.ToolAdmit(3, 0, "run_agent", map[string]any{"agent": "claude"})
	if err != nil {
		t.Fatalf("tool_admit run_agent: %v", err)
	}
	if !res.Allowed {
		t.Fatal("run_agent denied — allowlist broken")
	}
	if res.NeedsApproval {
		t.Fatal("allowlisted run_agent must not request approval")
	}
	res, err = c.ToolAdmit(3, 0, "rm_rf", map[string]any{})
	if err != nil {
		t.Fatalf("tool_admit rogue: %v", err)
	}
	if res.Allowed {
		t.Fatal("rogue tool allowed — deny-first violated")
	}
	if res.Reason == "" {
		t.Fatal("deny reason empty")
	}
}

// TestClientPreLLMBudgetFields (issue #66): pre_llm must carry spent/limit so
// the UI can render the budget meter.
func TestClientPreLLMBudgetFields(t *testing.T) {
	skipIfNoHarness(t)
	wd, _ := os.Getwd()
	t.Setenv("FUSION_CONSOLE_HARNESS_SIDECAR", filepath.Join(wd, "..", "..", "sidecar", "harnessd.py"))

	c := New()
	defer c.Close()
	pre, err := c.PreLLM(4, 100000, "test-model", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("pre_llm: %v", err)
	}
	if !pre.Proceed {
		t.Fatalf("small budget denied: %s", pre.Reason)
	}
	if pre.Limit != 100000 {
		t.Errorf("limit = %d, want 100000", pre.Limit)
	}
	if pre.Spent < 0 {
		t.Errorf("spent = %d, want >= 0", pre.Spent)
	}
	spent, _, err := c.PostLLM(4, 100000, 500)
	if err != nil {
		t.Fatalf("post_llm: %v", err)
	}
	if spent < 500 {
		t.Errorf("post_llm spent = %d, want >= 500", spent)
	}
}

// TestClientResolveDenyAndUnknown (issue #110): the approval round-trip —
// deny yields human_gate, unknown ids are typed state errors.
func TestClientResolveDenyAndUnknown(t *testing.T) {
	skipIfNoHarness(t)
	wd, _ := os.Getwd()
	t.Setenv("FUSION_CONSOLE_HARNESS_SIDECAR", filepath.Join(wd, "..", "..", "sidecar", "harnessd.py"))

	c := New()
	defer c.Close()
	// Unknown id -> typed state error, never a bare string error.
	if _, err := c.Resolve(9, "ghost-id", true); err == nil {
		t.Fatal("unknown approval_id accepted")
	} else {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != "state" {
			t.Fatalf("want state RPCError, got %v", err)
		}
	}
	// needs_approval -> deny round-trip.
	admit, err := c.ToolAdmit(9, 0, "rm_rf", map[string]any{})
	if err != nil {
		t.Fatalf("tool_admit: %v", err)
	}
	if !admit.NeedsApproval || admit.ApprovalID == "" {
		t.Fatalf("expected needs_approval + approval_id, got %+v", admit)
	}
	deny, err := c.Resolve(9, admit.ApprovalID, false)
	if err != nil {
		t.Fatalf("resolve deny: %v", err)
	}
	if deny.Allowed || deny.BlockedBy != "human_gate" {
		t.Fatalf("deny verdict wrong: %+v", deny)
	}
}

// TestClientReport (issue #78): report returns the funnel snapshot plus an
// attribution section; maps may be empty but must be non-nil JSON objects.
func TestClientReport(t *testing.T) {
	skipIfNoHarness(t)
	wd, _ := os.Getwd()
	t.Setenv("FUSION_CONSOLE_HARNESS_SIDECAR", filepath.Join(wd, "..", "..", "sidecar", "harnessd.py"))

	c := New()
	defer c.Close()
	// Drive one turn so the conv has funnel + lifecycle state.
	if _, err := c.PreLLM(11, 100000, "test-model", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("pre_llm: %v", err)
	}
	if _, _, err := c.PostLLM(11, 100000, 300); err != nil {
		t.Fatalf("post_llm: %v", err)
	}
	var meter struct {
		Spent int `json:"budget_spent_tokens"`
	}
	if err := c.call("meter", map[string]any{"conv_id": 11}, &meter); err != nil || meter.Spent != 300 {
		t.Fatalf("meter: spent=%d, want 300, err=%v", meter.Spent, err)
	}
	rep, err := c.Report(11, 100000)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Snapshot.Budget.Spent < 300 {
		t.Errorf("snapshot budget spent = %d, want >= 300", rep.Snapshot.Budget.Spent)
	}
	if rep.Snapshot.Budget.Limit != 100000 {
		t.Errorf("snapshot budget limit = %d, want 100000", rep.Snapshot.Budget.Limit)
	}
	if rep.Snapshot.LLMMeter == nil {
		t.Error("llm_meter missing")
	}
	if rep.Attribution.Runs == nil || rep.Attribution.ModeCounts == nil || rep.Attribution.ClusterCounts == nil {
		t.Error("attribution maps missing (sidecar must always send the three sections)")
	}
}
