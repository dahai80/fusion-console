// Command console is fusion-console: a single-binary web console for coding
// agents. It serves the embedded SPA and proxies chat/agent runs server-side.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/server"
	"github.com/dahai80/fusion-console/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetOutput(os.Stdout)

	openBrowser := flag.Bool("open", false, "open the default browser with one-time auto-login after start")
	flag.Parse()

	dir := config.DefaultDir()
	cfg, err := config.Load(dir)
	if err != nil {
		log.Fatalf("console: %v", err)
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o700); err != nil {
		log.Fatalf("console: create data dir: %v", err)
	}

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
			go openDefaultBrowser(url)
		}
	}
	log.Printf("console: listening on http://%s (data dir %s)", addr, cfg.Server.DataDir)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("console: server stopped: %v", err)
	}
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
