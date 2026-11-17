// agent-studio integration bootstrap: status + consented pip install. The
// team capability needs the agent-studio daemon (UDS JSON-RPC); this file
// lets the Team tab detect a missing install and, on explicit user consent,
// pip-install the in-tree packages into the monorepo shared venv and start
// the daemon. Install runs as a background job with the same phase/log shape
// as the fusion-rag installer (ragInstallState is reused verbatim).
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	installed := false
	if dir != "" {
		// Installed = the entry point imports in the chosen interpreter.
		// Cheap and authoritative (pip metadata lies after partial installs).
		cmd := exec.Command(s.studioPythonFor(dir), "-c", "import agent_runtime")
		installed = cmd.Run() == nil
	}
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

// handleStudioInstall starts the consented background install: editable pip
// installs of the in-tree dependency chain (fusion-core, fusion-harness,
// fusion-agent-studio) into the shared venv, then start.sh start, then a
// socket-wait. Idempotent per phase — a running job rejects re-entry.
func (s *Server) handleStudioInstall(w http.ResponseWriter, r *http.Request) {
	dir := studioDir()
	if dir == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fusion-agent-studio project directory not found (set FUSION_AGENT_STUDIO_DIR)"})
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
	go s.installStudio(ctx, &s.studioInstall, dir)
	log.Printf("server: agent-studio install started dir=%s", dir)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) installStudio(ctx context.Context, st *ragInstallState, dir string) {
	repoRoot := filepath.Dir(dir)
	python := s.studioPythonFor(dir)
	st.logf("using interpreter %s", python)

	st.setPhase("installing")
	// In-tree dependency chain, order matters: each pip install -e registers
	// the package the next one's dependency resolution needs.
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

	st.setPhase("starting")
	st.logf("starting daemon via start.sh")
	if err := runStream(ctx, st, dir, "bash", "start.sh", "start"); err != nil {
		s.studioInstallFail(st, fmt.Errorf("start.sh: %v", err))
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
