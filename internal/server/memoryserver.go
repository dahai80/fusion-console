// fusion-memory hub install + lifecycle: the Agents > Memory tab installs
// fm-server from the dahai80/fusion-memory git repository (shallow clone +
// cargo build of the fm-server crate) and starts/stops it through the
// repository's own start.sh. Nothing downloads until the user clicks
// install; detection after the fact accepts a fm-server binary on PATH too.
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// memoryRepoURL is the shallow-clone source for the console-managed install.
const memoryRepoURL = "https://github.com/dahai80/fusion-memory.git"

// memoryBinaryPath returns the cargo-built fm-server binary inside the
// console-managed install dir.
func (s *Server) memoryBinaryPath() string {
	return filepath.Join(s.cfg.Memory.InstallDir, "target", "release", "fm-server")
}

// memoryBinary resolves the fm-server binary: the console-managed build
// first, then anything on PATH (installed outside the console).
func (s *Server) memoryBinary() string {
	if p := s.memoryBinaryPath(); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("fm-server"); err == nil {
		return p
	}
	return ""
}

// memoryBaseURL is the local fm-server HTTP address.
func (s *Server) memoryBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.cfg.Memory.Port)
}

// memoryHealthy probes the fm-server public /healthz endpoint. The endpoint
// itself verifies persist+store, so a 200 means the hub really answers.
func (s *Server) memoryHealthy(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.memoryBaseURL()+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// installMemory runs the install pipeline in the background: prerequisites
// (git, cargo), shallow clone (reused when the tree already exists), and the
// release build of the fm-server crate. Phases fail loudly.
func (s *Server) installMemory(ctx context.Context, st *ragInstallState) {
	dir := s.cfg.Memory.InstallDir

	st.setPhase("downloading")
	if _, err := exec.LookPath("git"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("git is required to install fusion-memory but was not found on PATH"))
		return
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("cargo (Rust toolchain) is required to build fusion-memory but was not found on PATH"))
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.ragInstallFail(st, fmt.Errorf("install dir: %v", err))
		return
	}
	gitDir := filepath.Join(dir, ".git")
	if fileExists(gitDir) || dirExists(gitDir) {
		st.logf("existing clone found at %s — pulling latest main", dir)
		if err := runStream(ctx, st, dir, "git", "pull", "--ff-only"); err != nil {
			st.logf("git pull failed (%v) — keeping the existing tree", err)
		}
	} else {
		if err := runStream(ctx, st, filepath.Dir(dir), "git", "clone", "--depth", "1", memoryRepoURL, dir); err != nil {
			s.ragInstallFail(st, fmt.Errorf("git clone: %v", err))
			return
		}
	}

	st.setPhase("installing")
	if err := runStream(ctx, st, dir, "cargo", "build", "-p", "fm-server", "--release"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("cargo build fm-server: %v", err))
		return
	}
	if !fileExists(s.memoryBinaryPath()) {
		s.ragInstallFail(st, fmt.Errorf("build finished but %s is missing", s.memoryBinaryPath()))
		return
	}

	s.cfg.Memory.Installed = true
	s.cfg.Memory.Enabled = true
	if err := s.cfg.Save(s.cfgDir); err != nil {
		s.ragInstallFail(st, fmt.Errorf("save config: %v", err))
		return
	}
	st.logf("installed and enabled — ready to start")
	st.setPhase("done")
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// startMemory launches fm-server via the repository's start.sh and waits
// briefly for /healthz to answer.
func (s *Server) startMemory() error {
	if !s.cfg.Memory.Installed {
		return fmt.Errorf("fusion-memory is not installed")
	}
	if s.memoryHealthy(time.Second) {
		return nil
	}
	if s.memoryBinary() == "" {
		return fmt.Errorf("fm-server binary not found — reinstall from the Memory tab")
	}
	script := filepath.Join(s.cfg.Memory.InstallDir, "start.sh")
	if runtime.GOOS == "windows" {
		return fmt.Errorf("fusion-memory start.sh requires a POSIX shell — start fm-server manually on Windows")
	}
	cmd := exec.Command("bash", script, "start")
	cmd.Dir = s.cfg.Memory.InstallDir
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("server: memory start output: %s", strings.TrimSpace(string(out)))
	}
	if err != nil {
		return fmt.Errorf("start script failed: %v", err)
	}
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second)
		if s.memoryHealthy(time.Second) {
			return nil
		}
	}
	return fmt.Errorf("fm-server did not become healthy within 30s")
}

// stopMemory stops fm-server via the repository's start.sh.
func (s *Server) stopMemory() error {
	script := filepath.Join(s.cfg.Memory.InstallDir, "start.sh")
	if runtime.GOOS == "windows" {
		return fmt.Errorf("fusion-memory start.sh requires a POSIX shell — stop fm-server manually on Windows")
	}
	cmd := exec.Command("bash", script, "stop")
	cmd.Dir = s.cfg.Memory.InstallDir
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("server: memory stop output: %s", strings.TrimSpace(string(out)))
	}
	return err
}
