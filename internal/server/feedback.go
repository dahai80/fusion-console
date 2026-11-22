// Feedback pipeline: users (or the brain, on their behalf) file GitHub issues
// from the console. Three entry points converge on one draft:
//  1. manual — the Feedback page form,
//  2. error-triggered — the frontend toasts on chat/agent stream errors and
//     opens the page prefilled,
//  3. brain-suggested — the suggest_feedback orchestrator tool drafts an
//     issue mid-conversation; the user confirms on the Feedback page.
//
// Submission always requires explicit user confirmation (never auto-filed)
// and every text passes secret redaction before it leaves the machine.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
)

// feedbackRepos maps the component picker to the GitHub repo issues are
// filed against. Every fusion-* piece is its own repo, so the issue has to
// land in the right one to be seen.
var feedbackRepos = map[string]string{
	"console": "dahai80/fusion-console",
	"memory":  "dahai80/fusion-memory",
	"rag":     "dahai80/fusion-rag",
	"gateway": "dahai80/fusion-gateway",
	"code":    "dahai80/fusion-code",
}

// feedbackDraft is the structured issue the brain fills from the rough notes.
type feedbackDraft struct {
	Title     string `json:"title"`
	Steps     string `json:"steps"`
	Expect    string `json:"expect"`
	Actual    string `json:"actual"`
	Extra     string `json:"extra"`
	Component string `json:"component"`
}

// feedbackLastDrafts keeps the newest brain draft per conversation so the
// chat suggestion card can pick it up (suggest_feedback tool path).
var feedbackLastDrafts sync.Map

func feedbackComponentRepo(c string) string {
	if repo, ok := feedbackRepos[c]; ok {
		return repo
	}
	return feedbackRepos["console"]
}

// secretPatterns redacts obvious credential material before any text is sent
// to the LLM or GitHub. Best-effort defense in depth, not a sanitizer spec.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{16,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token)["']?\s*[:=]\s*["']?[^\s"'，。]{8,}`),
}

func redactSecrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// handleFeedbackEnv reports the auto-collected environment block shown on
// the Feedback page and appended to filed issues.
func (s *Server) handleFeedbackEnv(w http.ResponseWriter, r *http.Request) {
	brain := "not configured"
	if p, model, err := s.feedbackBrainProvider(); err == nil {
		brain = p.Name + " / " + model
	}
	env := map[string]string{
		"revision": upstreamRevision,
		"os":       runtime.GOOS + " " + runtime.GOARCH,
		"brain":    brain,
	}
	writeJSON(w, http.StatusOK, env)
}

// feedbackBrainProvider resolves the drafting model: brain config first,
// then the first enabled gateway provider. Same precedence as chat.
func (s *Server) feedbackBrainProvider() (*config.Provider, string, error) {
	if bp := strings.TrimSpace(s.cfg.Orchestrator.BrainProviderID); bp != "" {
		if p, ok := s.providerByID(bp); ok {
			model := strings.TrimSpace(s.cfg.Orchestrator.BrainModel)
			if model == "" {
				model = p.DefaultModel()
			}
			return p, model, nil
		}
	}
	if p, ok := s.firstGatewayProvider(); ok {
		return p, p.DefaultModel(), nil
	}
	return nil, "", fmt.Errorf("no usable chat provider — enable one on the Models page")
}

// feedbackDraftPrompt asks for a single strict JSON object; everything else
// in the reply is discarded by extractJSONObject.
const feedbackDraftPrompt = `You turn a user's rough feedback about fusion-console (a local LLM console that dispatches headless coding agents) into a structured GitHub issue draft. Reply with ONLY one JSON object, no markdown fence, matching exactly:
{"title":"...","steps":"...","expect":"...","actual":"...","extra":"","component":"console|memory|rag|gateway|code"}
Field rules: title is one clear sentence (English preferred); steps = numbered reproduction steps or, for feature requests/question types, "n/a"; expect = the expected behavior; actual = what actually happened; extra = useful log lines from the rough notes (may be empty); component = which fusion component the feedback is really about (console is the default; memory/rag/gateway/code when the rough text clearly points there). Keep every field in the user's own language. Do not invent facts that are not in the rough notes.`

// handleFeedbackDraft runs the brain-fill: rough text in, structured draft
// out. The draft is only a form fill — submission stays a separate call.
func (s *Server) handleFeedbackDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rough     string `json:"rough"`
		Component string `json:"component"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Rough = strings.TrimSpace(req.Rough)
	if req.Rough == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rough text is required"})
		return
	}
	draft, err := s.feedbackBrainDraft(r.Context(), redactSecrets(req.Rough), req.Component)
	if err != nil {
		serverError(w, err, http.StatusBadGateway)
		return
	}
	// An explicit component choice from the form wins over the model's guess.
	if feedbackRepos[req.Component] != "" {
		draft.Component = req.Component
	}
	writeJSON(w, http.StatusOK, draft)
}

// feedbackBrainDraft calls the chat brain once and parses the structured
// draft out of the reply. Shared by the form path and suggest_feedback.
func (s *Server) feedbackBrainDraft(ctx context.Context, rough, component string) (feedbackDraft, error) {
	prov, model, err := s.feedbackBrainProvider()
	if err != nil {
		return feedbackDraft{}, err
	}
	provider, err := buildProvider(prov)
	if err != nil {
		return feedbackDraft{}, err
	}
	apiKey, err := s.resolveProviderKey(ctx, prov)
	if err != nil {
		return feedbackDraft{}, fmt.Errorf("resolve api key: %v", err)
	}
	userMsg := "Rough feedback:\n" + rough
	if feedbackRepos[component] != "" && component != "console" {
		userMsg += "\n(user pre-selected component: " + component + ")"
	}
	dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ch, err := provider.ChatStream(dctx, model, apiKey, []chat.Message{
		{Role: "system", Content: feedbackDraftPrompt},
		{Role: "user", Content: userMsg},
	})
	if err != nil {
		return feedbackDraft{}, err
	}
	var out strings.Builder
	for ev := range ch {
		switch ev.Type {
		case chat.EventDelta:
			out.WriteString(ev.Text)
		case chat.EventError:
			return feedbackDraft{}, fmt.Errorf("brain error: %s", ev.Text)
		}
	}
	return parseFeedbackDraft(out.String(), rough, component)
}

// parseFeedbackDraft extracts the JSON object from the model reply, falling
// back to a degraded single-field draft instead of failing the whole flow.
func parseFeedbackDraft(reply, rough, component string) (feedbackDraft, error) {
	reply = strings.TrimSpace(reply)
	lo, hi := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if lo >= 0 && hi > lo {
		var d feedbackDraft
		if err := json.Unmarshal([]byte(reply[lo:hi+1]), &d); err == nil && strings.TrimSpace(d.Title) != "" {
			return d, nil
		}
	}
	// Degraded fallback: the model ignored the contract. Still give the user
	// something editable rather than an opaque error.
	if strings.TrimSpace(reply) != "" && len(reply) < 200 && !strings.Contains(reply, "\n") {
		return feedbackDraft{Title: reply, Steps: "n/a", Expect: "n/a", Actual: rough, Component: component}, nil
	}
	return feedbackDraft{}, fmt.Errorf("brain reply was not a valid draft — try again")
}

// handleFeedbackDraftLatest returns the newest suggest_feedback draft for a
// conversation (the chat suggestion card fetches it after the tool fires).
func (s *Server) handleFeedbackDraftLatest(w http.ResponseWriter, r *http.Request) {
	var convID int64
	if v := r.URL.Query().Get("conversation_id"); v != "" {
		fmt.Sscanf(v, "%d", &convID)
	}
	if d, ok := feedbackLastDrafts.Load(convID); ok {
		writeJSON(w, http.StatusOK, d.(feedbackDraft))
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no draft for this conversation"})
}

// handleFeedbackSubmit files the issue with the gh CLI. Fail visible: gh
// missing or unauthenticated is a real error the UI shows, never a silent drop.
func (s *Server) handleFeedbackSubmit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Component string `json:"component"`
		Title     string `json:"title"`
		Body      string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	title := strings.TrimSpace(strings.ReplaceAll(req.Title, "\n", " "))
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}
	if len(title) > 160 {
		title = title[:160]
	}
	body := redactSecrets(strings.TrimSpace(req.Body))
	if body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is required"})
		return
	}
	repo := feedbackComponentRepo(req.Component)
	if _, err := exec.LookPath("gh"); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "gh CLI not found on PATH — install GitHub CLI and run: gh auth login"})
		return
	}
	sctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(sctx, "gh", "issue", "create", "-R", repo, "-t", title, "-F", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("server: feedback submit to %s failed: %v: %s", repo, err, strings.TrimSpace(string(out)))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("gh issue create failed: %v — is gh authenticated? (gh auth login)", err)})
		return
	}
	url := feedbackIssueURL(string(out))
	log.Printf("server: feedback filed to %s: %s", repo, url)
	writeJSON(w, http.StatusOK, map[string]string{"url": url, "repo": repo})
}

// feedbackIssueURL pulls the issue URL out of gh's output (it prints the
// created issue URL as its last line).
func feedbackIssueURL(out string) string {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "https://") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// runSuggestFeedback implements the suggest_feedback brain tool: draft now,
// stash for the UI, and tell the model the user still has to confirm. The
// brain never files anything by itself.
func (s *Server) runSuggestFeedback(ctx context.Context, rawArgs string, convID int64, send func(map[string]any)) (string, error) {
	var args struct {
		Rough     string `json:"rough"`
		Component string `json:"component"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", fmt.Errorf("suggest_feedback: bad arguments: %v", err)
	}
	args.Rough = strings.TrimSpace(args.Rough)
	if args.Rough == "" {
		return "", fmt.Errorf("suggest_feedback: rough description is required")
	}
	draft, err := s.feedbackBrainDraft(ctx, redactSecrets(args.Rough), args.Component)
	if err != nil {
		return "", err
	}
	feedbackLastDrafts.Store(convID, draft)
	if send != nil {
		send(map[string]any{"type": "tool", "tool": "suggest_feedback", "status": "progress",
			"detail": fmt.Sprintf("feedback draft ready (component %s) — confirm on the Feedback page", draft.Component)})
	}
	return fmt.Sprintf("A feedback draft is prepared (component %s, title %q) and shown to the user as a suggestion card. Ask the user to open the Feedback page to review and submit it; you cannot submit issues yourself.", draft.Component, draft.Title), nil
}
