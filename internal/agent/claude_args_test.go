package agent

import "testing"

func TestClaudeArgsAppendSystemPrompt(t *testing.T) {
	args := claudeArgs("claude", RunOpts{Task: "do work", AppendSystemPrompt: "CAVEMAN MODE ACTIVE"})
	found := false
	for i, a := range args {
		if a == "--append-system-prompt" {
			found = true
			if i+1 >= len(args) || args[i+1] != "CAVEMAN MODE ACTIVE" {
				t.Fatalf("flag value missing or wrong: %v", args)
			}
		}
	}
	if !found {
		t.Errorf("--append-system-prompt not in args: %v", args)
	}
	if args[1] != "do work" {
		t.Errorf("task position changed: %v", args)
	}
}

func TestClaudeArgsNoSystemPromptByDefault(t *testing.T) {
	args := claudeArgs("claude", RunOpts{Task: "do work"})
	for _, a := range args {
		if a == "--append-system-prompt" {
			t.Errorf("flag must be absent without AppendSystemPrompt: %v", args)
		}
	}
}

func TestClaudeArgsFullComposition(t *testing.T) {
	args := claudeArgs("fusion-code", RunOpts{
		Task: "t", PermissionMode: "bypassPermissions", Model: "glm-5",
		AppendSystemPrompt: "rules",
	})
	want := []string{"-p", "t", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions", "--model", "glm-5",
		"--append-system-prompt", "rules"}
	if len(args) != len(want) {
		t.Fatalf("arg count mismatch: %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}
