package server

import (
	"strings"
	"testing"
)

func TestParseFeedbackDraftStrictJSON(t *testing.T) {
	reply := "Here is the draft:\n{\"title\":\"hub hangs\",\"steps\":\"1. run\\n2. wait\",\"expect\":\"error\",\"actual\":\"hangs\",\"extra\":\"\",\"component\":\"memory\"}\nDone."
	d, err := parseFeedbackDraft(reply, "rough text", "console")
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "hub hangs" || d.Component != "memory" {
		t.Fatalf("unexpected draft: %+v", d)
	}
}

func TestParseFeedbackDraftFencedJSON(t *testing.T) {
	reply := "```json\n{\"title\":\"t\",\"steps\":\"s\",\"expect\":\"e\",\"actual\":\"a\",\"extra\":\"\",\"component\":\"rag\"}\n```"
	d, err := parseFeedbackDraft(reply, "rough", "rag")
	if err != nil {
		t.Fatal(err)
	}
	if d.Component != "rag" {
		t.Fatalf("component = %q", d.Component)
	}
}

func TestParseFeedbackDegradedFallback(t *testing.T) {
	d, err := parseFeedbackDraft("Everything hangs after reconnect", "rough", "console")
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Everything hangs after reconnect" || d.Actual != "rough" {
		t.Fatalf("degraded draft wrong: %+v", d)
	}
}

func TestParseFeedbackDraftRejectsGarbage(t *testing.T) {
	if _, err := parseFeedbackDraft("line one\nline two\nline three", "rough", "console"); err == nil {
		t.Fatal("expected error for multi-line non-JSON reply")
	}
}

func TestRedactSecrets(t *testing.T) {
	in := "key sk-abcdef1234567890abcdef and ghp_" + strings.Repeat("x", 30) + " and Authorization: Bearer abcdef123456789012"
	out := redactSecrets(in)
	if strings.Contains(out, "sk-abcdef") || strings.Contains(out, "ghp_") || strings.Contains(out, "abcdef123456789012") {
		t.Fatalf("secret survived redaction: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("no redaction marker: %s", out)
	}
}

func TestFeedbackIssueURL(t *testing.T) {
	out := "Creating issue...\n\nhttps://github.com/dahai80/fusion-console/issues/120\n"
	if got := feedbackIssueURL(out); got != "https://github.com/dahai80/fusion-console/issues/120" {
		t.Fatalf("got %q", got)
	}
	if got := feedbackIssueURL("no url here"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestFeedbackComponentRepo(t *testing.T) {
	if got := feedbackComponentRepo("memory"); got != "dahai80/fusion-memory" {
		t.Fatalf("got %q", got)
	}
	if got := feedbackComponentRepo("bogus"); got != "dahai80/fusion-console" {
		t.Fatalf("unknown component should fall back to console, got %q", got)
	}
}
