// Command console is fusion-console: a single-binary web console for coding
// agents. It serves the embedded SPA and proxies chat/agent runs server-side.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/server"
	"github.com/dahai80/fusion-console/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetOutput(os.Stdout)

	openBrowser := flag.Bool("open", true, "open the default browser with one-time auto-login after start (use -open=false on headless hosts)")
	flag.Parse()

	dir := config.DefaultDir()
	cfg, err := config.Load(dir)
	if err != nil {
		log.Fatalf("console: %v", err)
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o700); err != nil {
		log.Fatalf("console: create data dir: %v", err)
	}

	// Desktop convenience: double-clicking the binary should bring up the
	// full stack. Start the bundled sibling gateway when a config exists and
	// :11432 is still free (start.sh does the same for script launches).
	maybeStartGateway()

	st, err := store.Open(filepath.Join(cfg.Server.DataDir, "console.db"))
	if err != nil {
		log.Fatalf("console: %v", err)
	}
	defer st.Close()

	if err := ensureFirstAdmin(cfg, dir); err != nil {
		log.Fatalf("console: %v", err)
	}
	if len(cfg.Agent) == 0 {
		cfg.Agent = config.DefaultAgents()
		if err := cfg.Save(dir); err != nil {
			log.Fatalf("console: save default agents: %v", err)
		}
		log.Printf("console: initialized default agent settings")
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := server.New(cfg, dir, st)
	if *openBrowser {
		url, err := armAutoLogin(srv, cfg)
		if err != nil {
			log.Printf("console: auto-login setup failed: %v", err)
		} else {
			url = fmt.Sprintf("http://%s/?fc_token=%s", addr, url)
			if !hasEnabledProvider(cfg) {
				// First run: land directly in the guided setup wizard.
				url += "&onboard=1"
				log.Printf("console: no LLM provider configured — opening onboarding wizard")
			}
			go openDefaultBrowser(url)
		}
	}
	log.Printf("console: listening on http://%s (data dir %s)", addr, cfg.Server.DataDir)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("console: server stopped: %v", err)
	}
}

// hasEnabledProvider reports whether at least one LLM provider is configured
// and enabled — false means first-run guided setup should open.
func hasEnabledProvider(cfg *config.Config) bool {
	for i := range cfg.Provider {
		if cfg.Provider[i].Enabled {
			return true
		}
	}
	return false
}

// maybeStartGateway launches a fusion-gateway binary sitting next to the
// console executable (the package layout) when a config.yaml is found and
// nothing listens on :11432. Best-effort: any failure just logs.
func maybeStartGateway() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	gwName := "fusion-gateway"
	if runtime.GOOS == "windows" {
		gwName += ".exe"
	}
	gw := filepath.Join(filepath.Dir(exe), gwName)
	if fi, err := os.Stat(gw); err != nil || fi.IsDir() {
		return // not bundled / not installed next to us
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:11432", 300*time.Millisecond); err == nil {
		conn.Close()
		log.Printf("console: gateway already running on :11432")
		return
	}
	home, _ := os.UserHomeDir()
	cfgCandidates := []string{
		filepath.Join(filepath.Dir(exe), "config.yaml"),
		filepath.Join(home, ".fusion-gateway", "config.yaml"),
	}
	gwCfg := ""
	for _, c := range cfgCandidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			gwCfg = c
			break
		}
	}
	if gwCfg == "" {
		log.Printf("console: gateway binary found but no config.yaml — start it manually or via the Gateway menu")
		return
	}
	logDir := filepath.Join(config.DefaultDir(), "log")
	_ = os.MkdirAll(logDir, 0o755)
	logf, err := os.OpenFile(filepath.Join(logDir, "gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("console: gateway log file: %v", err)
		return
	}
	cmd := exec.Command(gw, "-config", gwCfg)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = sysProcDetach()
	if err := cmd.Start(); err != nil {
		log.Printf("console: gateway start failed: %v", err)
		return
	}
	log.Printf("console: gateway started (pid %d, config %s, log %s)", cmd.Process.Pid, gwCfg, filepath.Join(logDir, "gateway.log"))
}

// armAutoLogin generates a one-time token and registers it with the server.
// Returns the raw token for the browser URL.
func armAutoLogin(srv *server.Server, cfg *config.Config) (string, error) {
	if len(cfg.Users) == 0 {
		return "", fmt.Errorf("no users configured")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	srv.SetAutoLoginToken(tok)
	return tok, nil
}

// openDefaultBrowser opens the OS default browser (macOS: open, Windows:
// rundll32 url.dll handler, Linux: xdg-open). Best-effort; failures log only.
func openDefaultBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.SysProcAttr = sysProcDetach()
	if err := cmd.Run(); err != nil {
		log.Printf("console: open browser: %v (open %s manually)", err, url)
	}
}

// defaultAdminPassword is the factory default for the first-run admin
// account. Override per deployment with FUSION_CONSOLE_ADMIN_PASSWORD.
const defaultAdminPassword = "fusion-admin-2026"

// ensureFirstAdmin creates the initial admin account when no users exist,
// using FUSION_CONSOLE_ADMIN_PASSWORD if set, otherwise the factory default,
// and prints the effective source to the log.
func ensureFirstAdmin(cfg *config.Config, dir string) error {
	if len(cfg.Users) > 0 {
		return nil
	}
	pw := os.Getenv("FUSION_CONSOLE_ADMIN_PASSWORD")
	source := "factory default"
	if pw == "" {
		pw = defaultAdminPassword
	} else {
		source = "FUSION_CONSOLE_ADMIN_PASSWORD"
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	cfg.Users = append(cfg.Users, config.UserEntry{Name: "admin", PasswordHash: hash})
	if cfg.Server.DataDir == "" {
		cfg.Server.DataDir = dir
	}
	if err := cfg.Save(dir); err != nil {
		return err
	}
	log.Printf("console: FIRST RUN: created user 'admin' with %s password", source)
	log.Printf("console: change it after first login (see README)")
	return nil
}
