// agent-studio integration bootstrap: status + consented pip install. The
// team capability needs the agent-studio daemon (UDS JSON-RPC); this file
// lets the Team tab detect a missing install and, on explicit user consent,
// pip-install the in-tree packages into the monorepo shared venv and start
// the daemon. Install runs as a background job with the same phase/log shape
// as the fusion-rag installer (ragInstallState is reused verbatim).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// studioDir resolves the fusion-agent-studio project root: env override, then
// the monorepo sibling of the console working directory, then the canonical
// ~/fusion layout. Empty when none exists — the Team tab then reports
// "not installed" without a path to install from.
func studioDir() string {
	if p := os.Getenv("FUSION_AGENT_STUDIO_DIR"); p != "" {
		if st, err := os.Stat(filepath.Join(p, "pyproject.toml")); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, p := range []string{
		filepath.Join("..", "fusion-agent-studio"),
		filepath.Join(os.Getenv("HOME"), "fusion", "fusion-agent-studio"),
	} {
		if st, err := os.Stat(filepath.Join(p, "pyproject.toml")); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
		}
	}
	return ""
}

// studioDaemonRunning reports whether the agent-studio daemon answers on its
// UDS socket right now.
func studioDaemonRunning() bool {
	conn, err := net.DialTimeout("unix", studioSocketPath(), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// studioPython picks the interpreter the install lands in: the monorepo
// shared venv when present (the layout the daemon's start.sh expects), else
// python3.
func studioPython(repoRoot string) string {
	for _, p := range []string{
		filepath.Join(repoRoot, ".venv", "bin", "python3"),
		filepath.Join("..", ".venv", "bin", "python3"),
	} {
		if _, err := os.Stat(p); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
		}
	}
	return "python3"
}

// studioStatusOut is the Team-tab bootstrap payload.
type studioStatusOut struct {
	Installed     bool     `json:"installed"`
	DaemonRunning bool     `json:"daemon_running"`
	Enabled       bool     `json:"enabled"`
	Dir           string   `json:"dir"`
	Socket        string   `json:"socket"`
	Phase         string   `json:"phase"` // install job: idle|installing|starting|done|error
	Logs          []string `json:"logs,omitempty"`
	Error         string   `json:"error,omitempty"`
}

func (s *Server) handleStudioStatus(w http.ResponseWriter, r *http.Request) {
	dir := studioDir()
	// Installed = some candidate interpreter (workspace venv, Homebrew
	// formula venv, or system python3) can import agent_runtime — pip
	// metadata lies after partial installs, the import does not.
	installed := studioInstalled(dir)
	phase, logs, jobErr := s.studioInstall.snapshot()
	writeJSON(w, http.StatusOK, studioStatusOut{
		Installed:     installed,
		DaemonRunning: studioDaemonRunning(),
		Enabled:       s.cfg.AgentStudio.Enabled,
		Dir:           dir,
		Socket:        studioSocketPath(),
		Phase:         phase,
		Logs:          logs,
		Error:         jobErr,
	})
}

// studioPythonFor returns the interpreter for a given studio dir (shared venv
// of the monorepo root that contains it, else python3).
func (s *Server) studioPythonFor(dir string) string {
	if p := filepath.Join(filepath.Dir(dir), ".venv", "bin", "python3"); fileExists(p) {
		return p
	}
	return "python3"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// brewStudioPython returns the Homebrew formula's venv interpreter when the
// dahai80/fusion-mlx tap has fusion-agent-studio installed, else "".
func brewStudioPython() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "brew", "--prefix").Output()
	if err != nil {
		return ""
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "opt", "fusion-agent-studio", "libexec", "bin", "python")
	if fileExists(p) {
		return p
	}
	return ""
}

// studioPythonCandidates lists interpreters that may carry agent_runtime, in
// probe order: workspace shared venv (monorepo layout), Homebrew formula
// venv, system python3 (covers plain `pip install fusion-agent-studio`).
func studioPythonCandidates(dir string) []string {
	out := []string{}
	if dir != "" {
		if p := filepath.Join(filepath.Dir(dir), ".venv", "bin", "python3"); fileExists(p) {
			out = append(out, p)
		}
	}
	if p := brewStudioPython(); p != "" {
		out = append(out, p)
	}
	return append(out, "python3")
}

// studioInstalled reports whether any candidate interpreter can import
// agent_runtime — the authoritative check regardless of install method.
func studioInstalled(dir string) bool {
	for _, py := range studioPythonCandidates(dir) {
		if exec.Command(py, "-c", "import agent_runtime").Run() == nil {
			return true
		}
	}
	return false
}

// startStudioDaemon launches the daemon: via the workspace start.sh when the
// checkout exists, otherwise detached under the first interpreter that has
// agent_runtime importable (PyPI / Homebrew installs have no start.sh).
func (s *Server) startStudioDaemon(ctx context.Context, st *ragInstallState, dir string) error {
	if dir != "" && fileExists(filepath.Join(dir, "start.sh")) {
		return runStream(ctx, st, dir, "bash", "start.sh", "start")
	}
	python := "python3"
	for _, py := range studioPythonCandidates(dir) {
		if exec.Command(py, "-c", "import agent_runtime").Run() == nil {
			python = py
			break
		}
	}
	logDir := filepath.Join(os.Getenv("HOME"), ".fusion-agent-studio")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "daemon-stdout.log")
	st.logf("detached start: %s -m agent_runtime.daemon_server", python)
	launch := fmt.Sprintf("nohup %s -m agent_runtime.daemon_server >> %q 2>> %q &", python, logPath, logPath)
	return exec.CommandContext(ctx, "bash", "-c", launch).Run()
}

// studioInstallReq selects the install source. Empty means "auto": skip the
// package step (the daemon binary is already there) and just start it.
type studioInstallReq struct {
	Method string `json:"method"` // auto (default) | workspace | pypi | brew
}

// handleStudioInstall starts the consented background install and daemon
// start. Methods: workspace = editable pip of the in-tree chain into the
// shared venv; pypi = `pip install fusion-agent-studio`; brew = Homebrew
// formula from the dahai80/fusion-mlx tap; auto = start only. Every method
// ends with a bounded socket-wait. Idempotent per phase — a running job
// rejects re-entry.
func (s *Server) handleStudioInstall(w http.ResponseWriter, r *http.Request) {
	method := "auto"
	var req studioInstallReq
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err == nil && req.Method != "" {
			method = req.Method
		}
	}
	dir := studioDir()
	if method == "workspace" && dir == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fusion-agent-studio project directory not found (set FUSION_AGENT_STUDIO_DIR or install via PyPI/Homebrew)"})
		return
	}
	phase, _, _ := s.studioInstall.snapshot()
	if phase == "installing" || phase == "starting" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "install already running"})
		return
	}
	if studioDaemonRunning() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "already-running"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.studioInstall.mu.Lock()
	s.studioInstall.phase = "installing"
	s.studioInstall.logs = nil
	s.studioInstall.err = ""
	s.studioInstall.cancel = cancel
	s.studioInstall.mu.Unlock()
	go s.installStudio(ctx, &s.studioInstall, dir, method)
	log.Printf("server: agent-studio install started dir=%s method=%s", dir, method)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) installStudio(ctx context.Context, st *ragInstallState, dir, method string) {
	st.setPhase("installing")
	switch method {
	case "workspace":
		repoRoot := filepath.Dir(dir)
		python := s.studioPythonFor(dir)
		st.logf("using interpreter %s", python)
		// In-tree dependency chain, order matters: each pip install -e
		// registers the package the next one's dependency resolution needs.
		for _, pkg := range []string{"fusion-core", "fusion-harness", "fusion-agent-studio"} {
			p := filepath.Join(repoRoot, pkg)
			if !fileExists(filepath.Join(p, "pyproject.toml")) {
				st.logf("skip %s (not found at %s)", pkg, p)
				continue
			}
			st.logf("pip install -e %s", pkg)
			if err := runStream(ctx, st, p, python, "-m", "pip", "install", "-e", p); err != nil {
				s.studioInstallFail(st, fmt.Errorf("pip install %s: %v", pkg, err))
				return
			}
		}
	case "pypi":
		python := s.studioPythonFor(dir)
		st.logf("pip install fusion-agent-studio into %s", python)
		if err := runStream(ctx, st, dir, python, "-m", "pip", "install", "fusion-agent-studio"); err != nil {
			s.studioInstallFail(st, fmt.Errorf("pip install fusion-agent-studio: %v", err))
			return
		}
	case "brew":
		st.logf("brew install dahai80/fusion-mlx/fusion-agent-studio")
		if err := runStream(ctx, st, dir, "brew", "install", "dahai80/fusion-mlx/fusion-agent-studio"); err != nil {
			s.studioInstallFail(st, fmt.Errorf("brew install: %v", err))
			return
		}
	default: // auto: package already present, straight to start
		st.logf("method auto: skipping package install")
	}

	st.setPhase("starting")
	if err := s.startStudioDaemon(ctx, st, dir); err != nil {
		s.studioInstallFail(st, fmt.Errorf("daemon start: %v", err))
		return
	}
	// Wait for the UDS socket to answer (bounded — never hangs the job).
	for i := 0; i < 30; i++ {
		if ctx.Err() != nil {
			s.studioInstallFail(st, fmt.Errorf("cancelled"))
			return
		}
		if studioDaemonRunning() {
			st.logf("daemon is answering on %s", studioSocketPath())
			st.setPhase("done")
			return
		}
		time.Sleep(time.Second)
	}
	s.studioInstallFail(st, fmt.Errorf("daemon socket %s did not come up within 30s", studioSocketPath()))
}

func (s *Server) studioInstallFail(st *ragInstallState, err error) {
	st.mu.Lock()
	st.err = err.Error()
	st.phase = "error"
	st.mu.Unlock()
	log.Printf("server: agent-studio install failed: %v", err)
}
