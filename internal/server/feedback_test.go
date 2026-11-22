package server

import (
	"encoding/json"
	"io"
	"net/http"

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

type issueTransport func(*http.Request) (*http.Response, error)

func (fn issueTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestListFeedbackIssues(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: issueTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Host != "api.github.com" || r.URL.Path != "/search/issues" ||
			q.Get("q") != "repo:dahai80/fusion-console is:issue" || q.Get("sort") != "created" ||
			q.Get("order") != "desc" || q.Get("per_page") != "3" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected public query: %v", r.URL)
		}
		body := `{"items":[{"number":105,"body":"` + strings.Repeat("a", 2600) + `"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	out, err := listFeedbackIssues(t.Context(), "{}", client)
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "105") || strings.Contains(out, strings.Repeat("a", 2401)) {
		t.Fatalf("unexpected public issue result: %v", err)
	}
	for _, input := range []string{`{"component":"unknown"}`, `{"limit":11}`} {
		if _, err := listFeedbackIssues(t.Context(), input, client); err == nil {
			t.Fatalf("invalid input accepted: %s", input)
		}
	}
	if calls != 1 {
		t.Fatalf("made %d requests", calls)
	}
}
