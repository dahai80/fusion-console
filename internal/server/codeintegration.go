// Optional fusion-code integration. Nothing here runs unless the user
// installs fusion-code from the Coding Agent menu: the installer downloads
// the source tarball, runs bun install + builds the CLI and its web GUI,
// start/stop manage the API service (:11441) and the web server (:11482),
// and the console proxies the web GUI at /shell/fusion-code/
// (path-preserving, the web server serves the same prefix).
package server

import (
	"context"
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
	"strconv"
	"strings"
	"syscall"
	"time"
)

// codeBaseURL is the local fusion-code web GUI address.
func (s *Server) codeBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.cfg.Code.WebPort)
}

// codeHealthy probes the fusion-code web GUI with a short timeout.
func (s *Server) codeHealthy(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.codeBaseURL()+"/", nil)
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

// --- Reverse proxy ----------------------------------------------------------

// codeProxy forwards /shell/fusion-code/* to the local fusion-code web GUI
// verbatim — same 1:1 prefix convention as the fusion-rag proxy.
func (s *Server) codeProxy() http.Handler {
	target, _ := url.Parse(s.codeBaseURL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("server: code proxy %s %s: %v", r.Method, r.URL.Path, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "fusion-code web is not running — start it from the Coding Agent menu"})
	}
	return proxy
}

// --- Lifecycle: install / start / stop --------------------------------------

// codeReleaseTarball is the source archive the installer downloads. fusion-code
// releases ship no assets, so the default branch tarball is used; override
// with FUSION_CODE_DOWNLOAD_URL for mirrors or pinned versions.
func codeReleaseTarball() string {
	if u := os.Getenv("FUSION_CODE_DOWNLOAD_URL"); u != "" {
		return u
	}
	return "https://github.com/dahai80/fusion-code/archive/refs/heads/main.tar.gz"
}

// codeSourceMirrors are tried in order when the primary URL fails — GitHub
// codeload connections from CN networks are flaky enough that a single
// source makes installs fail more often than not.
func codeSourceMirrors() []string {
	return []string{
		codeReleaseTarball(),
		"https://ghproxy.net/" + codeReleaseTarball(),
		"https://ghfast.top/" + codeReleaseTarball(),
		"https://gh.llkk.cc/" + codeReleaseTarball(),
	}
}

// installCode runs the full install pipeline in the background:
// download → extract → bun install (CLI) → bun run build (CLI) →
// bun install (web) → vite build (web).
func (s *Server) installCode(ctx context.Context, st *ragInstallState) {
	dir := s.cfg.Code.InstallDir
	src := filepath.Join(dir, "src")

	if _, err := exec.LookPath("bun"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("bun is required to install fusion-code but was not found on PATH"))
		return
	}

	st.setPhase("downloading")
	tarball := filepath.Join(dir, "fusion-code-src.tar.gz")
	var dlErr error
	for _, u := range codeSourceMirrors() {
		st.logf("trying %s", u)
		dlErr = downloadFile(ctx, u, tarball)
		if dlErr == nil {
			break
		}
		st.logf("failed: %v", dlErr)
	}
	if dlErr != nil {
		s.ragInstallFail(st, fmt.Errorf("download (all mirrors): %v", dlErr))
		return
	}
	st.logf("downloaded source archive")

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
	if err := flattenSingleDir(src); err != nil {
		s.ragInstallFail(st, err)
		return
	}
	_ = os.Remove(tarball)
	st.logf("extracted to %s", src)

	st.setPhase("installing")
	if err := runStream(ctx, st, src, "bun", "install"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("bun install (cli): %v", err))
		return
	}
	if err := runStream(ctx, st, src, "bun", "run", "build"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("build cli: %v", err))
		return
	}
	webDir := filepath.Join(src, "web")
	if err := runStream(ctx, st, webDir, "bun", "install"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("bun install (web): %v", err))
		return
	}
	if err := runStream(ctx, st, webDir, "bun", "run", "build"); err != nil {
		s.ragInstallFail(st, fmt.Errorf("build web: %v", err))
		return
	}

	// Persist the decision and enable by default — install success flips the
	// menu into the configured state without a manual step.
	s.cfg.Code.Installed = true
	s.cfg.Code.Enabled = true
	if err := s.cfg.Save(s.cfgDir); err != nil {
		s.ragInstallFail(st, fmt.Errorf("save config: %v", err))
		return
	}
	st.logf("installed and enabled — ready to start")
	st.setPhase("done")
}

// startCode launches the fusion-code API service via its start.sh, then the
// web server (bun run start) detached with a pid file, and waits briefly for
// the web GUI to answer.
func (s *Server) startCode() error {
	if !s.cfg.Code.Installed {
		return fmt.Errorf("fusion-code is not installed")
	}
	if s.codeHealthy(time.Second) {
		return nil
	}
	src := filepath.Join(s.cfg.Code.InstallDir, "src")

	// 1. API service (:11441). Its start.sh refuses unregistered ports and
	// manages its own pid file.
	script := filepath.Join(src, "start.sh")
	if runtime.GOOS == "windows" {
		// No start.cmd exists for fusion-code; the service path is
		// POSIX-only today. Surface that instead of a cryptic failure.
		return fmt.Errorf("fusion-code service start is not supported on windows yet")
	}
	cmd := exec.Command("bash", script, "start")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), fmt.Sprintf("FUSION_CODE_PORT=%d", s.cfg.Code.Port))
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("server: code service start output: %s", strings.TrimSpace(string(out)))
	}
	if err != nil {
		return fmt.Errorf("api service start script failed: %v", err)
	}

	// 2. Web GUI server (:11482), detached. bun run start = bun run server/index.ts.
	webDir := filepath.Join(src, "web")
	webCmd := exec.Command("bun", "run", "start")
	webCmd.Dir = webDir
	webCmd.Env = append(os.Environ(),
		fmt.Sprintf("FUSION_CODE_WEB_PORT=%d", s.cfg.Code.WebPort),
		"FUSION_CODE_WEB_BIND=127.0.0.1")
	logFile, err := os.OpenFile(filepath.Join(s.cfg.Code.InstallDir, "web.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		webCmd.Stdout = logFile
		webCmd.Stderr = logFile
	}
	if runtime.GOOS != "windows" {
		detachProcess(webCmd)
	}
	if err := webCmd.Start(); err != nil {
		return fmt.Errorf("start web server: %v", err)
	}
	_ = os.WriteFile(filepath.Join(s.cfg.Code.InstallDir, "web.pid"), []byte(strconv.Itoa(webCmd.Process.Pid)), 0o644)
	go func() { _ = webCmd.Wait() }() // reap on exit, keep no zombies

	// Wait up to 20s for the web GUI to answer.
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		if s.codeHealthy(time.Second) {
			return nil
		}
	}
	return fmt.Errorf("fusion-code web did not become healthy within 20s (see web.log in the install dir)")
}

// stopCode stops the web server (pid file) and the API service (start.sh).
func (s *Server) stopCode() error {
	src := filepath.Join(s.cfg.Code.InstallDir, "src")
	if runtime.GOOS != "windows" {
		if pidData, err := os.ReadFile(filepath.Join(s.cfg.Code.InstallDir, "web.pid")); err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(pidData))); perr == nil && pid > 0 {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Signal(syscall.SIGTERM)
				}
			}
		}
		_ = os.Remove(filepath.Join(s.cfg.Code.InstallDir, "web.pid"))
	}
	script := filepath.Join(src, "start.sh")
	if runtime.GOOS == "windows" {
		return nil // nothing to stop: windows has no service process
	}
	cmd := exec.Command("bash", script, "stop")
	cmd.Dir = src
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("server: code service stop output: %s", strings.TrimSpace(string(out)))
	}
	return err
}
