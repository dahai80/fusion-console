// Optional fusion-rag knowledge base integration. Nothing here runs unless
// the user installs fusion-rag from the Knowledge Base menu: the installer
// downloads a GitHub release tarball, builds a venv and pip-installs it,
// start/stop manage the RAG server process, the console proxies its UI at
// /shell/fusion-rag/ (path-preserving, RAG serves the same prefix), and the
// chat box reaches the KB over MCP (POST /mcp, JSON-RPC 2.0 tools/call) via
// the kb_search / kb_ask tools injected into every conversation.
package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dahai80/fusion-console/internal/chat"
)

// ragBaseURL is the local fusion-rag server address.
func (s *Server) ragBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.cfg.Rag.Port)
}

// ragEnabled reports whether chat should expose the KB tools: installed,
// enabled in config, and the server actually answering.
func (s *Server) ragEnabled() bool {
	if !s.cfg.Rag.Installed || !s.cfg.Rag.Enabled {
		return false
	}
	return s.ragHealthy(1500 * time.Millisecond)
}

// ragHealthy probes the RAG server health endpoint with a short timeout.
func (s *Server) ragHealthy(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ragBaseURL()+"/shell/fusion-rag/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// --- MCP bridge -------------------------------------------------------------

// ragToolDecls declares the two KB tools handed to the model over native
// tool-calls. They execute as MCP tools/call against the RAG /mcp endpoint.
func ragToolDecls() []chat.ToolDecl {
	return []chat.ToolDecl{
		{
			Name:        "kb_search",
			Description: "Search the local fusion-rag knowledge base for document chunks relevant to a query. Returns ranked text snippets with sources.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"query":{"type":"string","description":"search query"},` +
				`"kb_id":{"type":"string","description":"optional knowledge base id; omit to search all"},` +
				`"top_k":{"type":"integer","description":"max snippets to return (default 5)"}` +
				`},"required":["query"]}`),
		},
		{
			Name:        "kb_ask",
			Description: "Ask a question against the local fusion-rag knowledge base; retrieves relevant chunks and returns a grounded answer with citations. Use for questions answerable from the user's documents.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"question":{"type":"string","description":"the question to answer from the knowledge base"},` +
				`"kb_id":{"type":"string","description":"optional knowledge base id; omit to search all"}` +
				`},"required":["question"]}`),
		},
	}
}

// ragToolCall executes one MCP tools/call against the RAG server and returns
// the flattened text content. Best effort: errors carry tool-visible text.
// kb_search / kb_ask require kb_id on the RAG side; when the model omits it,
// the first knowledge base is used so single-KB setups just work.
func (s *Server) ragToolCall(ctx context.Context, name string, args map[string]any) (string, error) {
	if name == "kb_search" || name == "kb_ask" {
		if id, _ := args["kb_id"].(string); id == "" {
			kbID, err := s.ragDefaultKB(ctx)
			if err != nil {
				return "", fmt.Errorf("resolve default knowledge base: %v", err)
			}
			args["kb_id"] = kbID
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, s.ragBaseURL()+"/mcp", strings.NewReader(string(encoded)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("knowledge base unreachable (is fusion-rag running?): %v", err)
	}
	defer resp.Body.Close()
	var rpc struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return "", fmt.Errorf("bad MCP response from knowledge base: %v", err)
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("knowledge base tool error: %s", rpc.Error.Message)
	}
	out := ""
	for _, c := range rpc.Result.Content {
		if c.Text != "" {
			if out != "" {
				out += "\n"
			}
			out += c.Text
		}
	}
	if rpc.Result.IsError {
		return out, fmt.Errorf("knowledge base tool reported failure")
	}
	if out == "" {
		out = "(no results)"
	}
	return out, nil
}

// ragDefaultKB returns the first knowledge base id (cheap UI-level call).
func (s *Server) ragDefaultKB(ctx context.Context) (string, error) {
	out, err := s.ragRawRPCCall(ctx, "kb_list", map[string]any{})
	if err != nil {
		return "", err
	}
	var kbs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &kbs); err != nil {
		return "", err
	}
	if len(kbs) == 0 {
		return "", fmt.Errorf("no knowledge base exists yet — create one in the fusion-rag console")
	}
	return kbs[0].ID, nil
}

// ragRawRPCCall performs one MCP tools/call and returns the raw text result.
func (s *Server) ragRawRPCCall(ctx context.Context, name string, args map[string]any) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, s.ragBaseURL()+"/mcp", strings.NewReader(string(encoded)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return "", err
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("%s", rpc.Error.Message)
	}
	out := ""
	for _, c := range rpc.Result.Content {
		out += c.Text
	}
	return out, nil
}

// --- Chat tool loop ---------------------------------------------------------

// ragSystemPrompt is the light anchor used when RAG tools are available in a
// plain (non-orchestrated) conversation.
const ragSystemPrompt = "You can consult the user's local knowledge base (fusion-rag) via the kb_search and kb_ask tools. When a question may be answered from the user's documents, call the tools first and ground your answer in the returned snippets, citing their sources."

// maxRAGChatTurns bounds the plain-chat tool loop.
const maxRAGChatTurns = 4

// runRAGChatLoop drives a small tool-calling loop for plain conversations
// once fusion-rag is installed and enabled: every chat turn may call
// kb_search / kb_ask before answering. Mirrors runPlannerLoop minus agent
// dispatch and approvals.
func (s *Server) runRAGChatLoop(ctx context.Context, tp chat.ToolProvider, prov *providerOpts, apiKey string, msgs []chat.Message, send func(map[string]any)) (string, chat.Event) {
	tools := ragToolDecls()
	msgs = append([]chat.Message{{Role: "system", Content: ragSystemPrompt}}, msgs...)
	full := ""
	var total chat.Event
	for turn := 0; turn < maxRAGChatTurns; turn++ {
		evCh, err := tp.ChatStreamTools(ctx, prov.model, apiKey, msgs, tools)
		if err != nil {
			send(map[string]any{"type": "error", "text": err.Error()})
			return full, total
		}
		turnText := ""
		var toolCalls []chat.ToolCall
		iterIn, iterOut := 0, 0
		for ev := range evCh {
			switch ev.Type {
			case chat.EventDelta:
				turnText += ev.Text
				send(map[string]any{"type": "delta", "text": ev.Text})
			case chat.EventUsage:
				// Keep the max per iteration — gateways may emit usage twice.
				if ev.TokensIn > iterIn {
					iterIn = ev.TokensIn
				}
				if ev.TokensOut > iterOut {
					iterOut = ev.TokensOut
				}
			case chat.EventToolCall:
				toolCalls = ev.ToolCalls
			case chat.EventError:
				send(map[string]any{"type": "error", "text": ev.Text})
			case chat.EventDone:
			}
			if ev.Type == chat.EventDone {
				break
			}
		}
		total.TokensIn += iterIn
		total.TokensOut += iterOut
		if iterIn > 0 || iterOut > 0 {
			send(map[string]any{"type": "usage", "tokens_in": total.TokensIn, "tokens_out": total.TokensOut})
		}
		full += turnText
		if len(toolCalls) == 0 {
			return full, total
		}
		msgs = append(msgs, chat.Message{Role: "assistant", Content: turnText, ToolCalls: toolCalls})
		for _, call := range toolCalls {
			var callArgs map[string]any
			_ = json.Unmarshal([]byte(call.Arguments), &callArgs)
			if callArgs == nil {
				callArgs = map[string]any{}
			}
			send(map[string]any{"type": "tool", "tool": call.Name, "status": "running", "detail": toolDetail(call)})
			out, execErr := s.ragToolCall(ctx, call.Name, callArgs)
			status := "done"
			if execErr != nil {
				out = "error: " + execErr.Error()
				status = "error"
				log.Printf("server: rag tool %s failed: %v", call.Name, execErr)
			}
			send(map[string]any{"type": "tool", "tool": call.Name, "status": status, "detail": ""})
			msgs = append(msgs, chat.Message{Role: "tool", ToolCallID: call.ID, Content: out})
		}
	}
	return full, total
}

// --- Reverse proxy ----------------------------------------------------------

// ragProxy forwards /shell/fusion-rag/* to the local RAG server verbatim —
// RAG serves its console UI and REST API under the same prefix, so the map
// is 1:1 with zero rewrite (same convention fusion-gateway uses).
func (s *Server) ragProxy() http.Handler {
	target, _ := url.Parse(s.ragBaseURL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("server: rag proxy %s %s: %v", r.Method, r.URL.Path, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "fusion-rag is not running — start it from the Knowledge Base menu"})
	}
	return proxy
}

// --- Lifecycle: install / start / stop --------------------------------------

// ragInstallState tracks the one background install job. Simple and honest:
// a phase, an append-only log, and an error. Restarting the console during
// an install loses the job (the partial tree is reused/resumed by rerunning).
type ragInstallState struct {
	mu     sync.Mutex
	phase  string // idle | downloading | extracting | installing | done | error
	logs   []string
	err    string
	cancel context.CancelFunc
}

func (st *ragInstallState) snapshot() (string, []string, string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	logs := append([]string(nil), st.logs...)
	return st.phase, logs, st.err
}

func (st *ragInstallState) setPhase(p string) {
	st.mu.Lock()
	st.phase = p
	st.mu.Unlock()
	log.Printf("server: rag install phase=%s", p)
}

func (st *ragInstallState) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	st.mu.Lock()
	st.logs = append(st.logs, line)
	st.mu.Unlock()
	log.Printf("server: rag install: %s", line)
}

// ragReleaseAPI is overridable for tests and restricted networks.
func ragReleaseAPI() string {
	if u := os.Getenv("FUSION_RAG_RELEASE_API"); u != "" {
		return u
	}
	return "https://api.github.com/repos/dahai80/fusion-rag/releases/latest"
}

// coreReleaseAPI is the fusion-core release lookup (fusion-rag's in-tree
// dependency is not published to PyPI, so the installer provisions it first).
func coreReleaseAPI() string {
	if u := os.Getenv("FUSION_CORE_RELEASE_API"); u != "" {
		return u
	}
	return "https://api.github.com/repos/dahai80/fusion-core/releases/latest"
}

func coreReleaseTarball(tag string) string {
	if u := os.Getenv("FUSION_CORE_DOWNLOAD_URL"); u != "" {
		return u
	}
	return "https://github.com/dahai80/fusion-core/archive/refs/tags/" + tag + ".tar.gz"
}

func ragReleaseTarball(tag string) string {
	if u := os.Getenv("FUSION_RAG_DOWNLOAD_URL"); u != "" {
		return u
	}
	return "https://github.com/dahai80/fusion-rag/archive/refs/tags/" + tag + ".tar.gz"
}

const ragPipMirror = "https://mirrors.aliyun.com/pypi/simple/"

// installRAG runs the full install pipeline in the background. Phases fail
// loudly: any error lands in st.err and stops the job.
func (s *Server) installRAG(ctx context.Context, st *ragInstallState) {
	dir := s.cfg.Rag.InstallDir
	src := filepath.Join(dir, "src")

	st.setPhase("downloading")
	tag, err := latestRelease(ctx, ragReleaseAPI())
	if err != nil {
		s.ragInstallFail(st, err)
		return
	}
	st.logf("latest release %s", tag)
	tarball := filepath.Join(dir, "fusion-rag-"+tag+".tar.gz")
	if err := downloadFile(ctx, ragReleaseTarball(tag), tarball); err != nil {
		s.ragInstallFail(st, fmt.Errorf("download: %v", err))
		return
	}
	st.logf("downloaded %s", filepath.Base(tarball))

	st.setPhase("extracting")
	// A previous partial install may have left a partial tree — start clean.
	if err := os.RemoveAll(src); err != nil {
		s.ragInstallFail(st, err)
		return
	}
	if err := extractTreeTarGz(tarball, src); err != nil {
		s.ragInstallFail(st, fmt.Errorf("extract: %v", err))
		return
	}
	// The archive carries a versioned top directory — flatten it.
	if err := flattenSingleDir(src); err != nil {
		s.ragInstallFail(st, err)
		return
	}
	_ = os.Remove(tarball)
	st.logf("extracted to %s", src)

	st.setPhase("installing")
	venv := filepath.Join(dir, ".venv")
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "python"
	}
	if err := runStream(ctx, st, dir, python, "-m", "venv", venv); err != nil {
		s.ragInstallFail(st, fmt.Errorf("venv: %v", err))
		return
	}
	pip := filepath.Join(venv, "bin", "pip")
	if runtime.GOOS == "windows" {
		pip = filepath.Join(venv, "Scripts", "pip.exe")
	}
	// fusion-core is fusion-rag's shared-library dependency but is not on
	// PyPI — provision it from its GitHub release first so the editable
	// install of fusion-rag resolves.
	coreTag, err := latestRelease(ctx, coreReleaseAPI())
	if err != nil {
		s.ragInstallFail(st, fmt.Errorf("fusion-core release lookup: %v", err))
		return
	}
	coreTarball := filepath.Join(dir, "fusion-core-"+coreTag+".tar.gz")
	if err := downloadFile(ctx, coreReleaseTarball(coreTag), coreTarball); err != nil {
		s.ragInstallFail(st, fmt.Errorf("fusion-core download: %v", err))
		return
	}
	coreSrc := filepath.Join(dir, "fusion-core")
	_ = os.RemoveAll(coreSrc)
	if err := extractTreeTarGz(coreTarball, coreSrc); err != nil {
		s.ragInstallFail(st, fmt.Errorf("fusion-core extract: %v", err))
		return
	}
	if err := flattenSingleDir(coreSrc); err != nil {
		s.ragInstallFail(st, fmt.Errorf("fusion-core archive layout: %v", err))
		return
	}
	_ = os.Remove(coreTarball)
	if err := runStream(ctx, st, src, pip, "install", coreSrc, "-i", ragPipMirror); err != nil {
		s.ragInstallFail(st, fmt.Errorf("pip install fusion-core: %v", err))
		return
	}
	if err := runStream(ctx, st, src, pip, "install", "-e", src, "-i", ragPipMirror); err != nil {
		s.ragInstallFail(st, fmt.Errorf("pip install fusion-rag: %v", err))
		return
	}

	// Persist the decision and enable by default — install success flips the
	// menu into the configured state without a manual step.
	s.cfg.Rag.Installed = true
	s.cfg.Rag.Enabled = true
	if err := s.cfg.Save(s.cfgDir); err != nil {
		s.ragInstallFail(st, fmt.Errorf("save config: %v", err))
		return
	}
	st.logf("installed and enabled — ready to start")
	st.setPhase("done")
}

func (s *Server) ragInstallFail(st *ragInstallState, err error) {
	st.mu.Lock()
	st.err = err.Error()
	st.phase = "error"
	st.mu.Unlock()
	log.Printf("server: rag install failed: %v", err)
}

// flattenSingleDir collapses a GitHub-archive layout (one versioned top
// directory) so dest itself is the project root.
func flattenSingleDir(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return fmt.Errorf("unexpected archive layout under %s", dest)
	}
	flat := filepath.Join(dest, entries[0].Name())
	if err := os.Rename(flat, dest+".flat"); err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(dest+".flat", dest)
}

// latestRelease fetches the latest release tag from a GitHub repo API URL.
func latestRelease(ctx context.Context, apiURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup returned %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no tag_name in release response")
	}
	return rel.TagName, nil
}

// downloadFile streams one HTTP resource to disk, retrying up to three
// times — GitHub codeload connections from CN networks time out often
// enough that a single attempt makes installs flaky.
func downloadFile(ctx context.Context, urlStr, dest string) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = downloadOnce(ctx, urlStr, dest)
		if err == nil {
			return nil
		}
		log.Printf("server: rag download attempt %d failed: %v", attempt, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
	}
	return err
}

// downloadOnce performs one streaming download.
func downloadOnce(ctx context.Context, urlStr, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", urlStr, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// extractTreeTarGz unpacks a .tar.gz under dest, rejecting path escapes.
func extractTreeTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Clean(filepath.Join(dest, hdr.Name))
		if !strings.HasPrefix(target, cleanDest+string(filepath.Separator)) && target != cleanDest {
			return fmt.Errorf("archive entry escapes destination: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// runStream runs one command, streaming combined output into the install log.
func runStream(ctx context.Context, st *ragInstallState, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	st.logf("$ %s %s", name, strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		return err
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := pipe.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				idx := strings.IndexByte(string(buf), '\n')
				if idx < 0 {
					break
				}
				st.logf("%s", string(buf[:idx]))
				buf = buf[idx+1:]
			}
		}
		if err != nil {
			break
		}
	}
	return cmd.Wait()
}

// startRAG launches the RAG server via its start.sh (or start.cmd on
// Windows) and waits briefly for the health endpoint.
func (s *Server) startRAG() error {
	if !s.cfg.Rag.Installed {
		return fmt.Errorf("fusion-rag is not installed")
	}
	if s.ragHealthy(time.Second) {
		return nil
	}
	script := filepath.Join(s.cfg.Rag.InstallDir, "src", "start.sh")
	cmd := exec.Command("bash", script, "start")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", filepath.Join(s.cfg.Rag.InstallDir, "src", "start.cmd"), "start")
	}
	cmd.Dir = filepath.Join(s.cfg.Rag.InstallDir, "src")
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("server: rag start output: %s", strings.TrimSpace(string(out)))
	}
	if err != nil {
		return fmt.Errorf("start script failed: %v", err)
	}
	// Wait up to 20s for the health endpoint to answer.
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		if s.ragHealthy(time.Second) {
			return nil
		}
	}
	return fmt.Errorf("fusion-rag did not become healthy within 20s")
}

// stopRAG stops the RAG server via its start script.
func (s *Server) stopRAG() error {
	script := filepath.Join(s.cfg.Rag.InstallDir, "src", "start.sh")
	cmd := exec.Command("bash", script, "stop")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", filepath.Join(s.cfg.Rag.InstallDir, "src", "start.cmd"), "stop")
	}
	cmd.Dir = filepath.Join(s.cfg.Rag.InstallDir, "src")
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("server: rag stop output: %s", strings.TrimSpace(string(out)))
	}
	return err
}
