// Command console is fusion-console: a single-binary web console for coding
// agents. It serves the embedded SPA and proxies chat/agent runs server-side.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/server"
	"github.com/dahai80/fusion-console/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetOutput(os.Stdout)

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
	log.Printf("console: listening on http://%s (data dir %s)", addr, cfg.Server.DataDir)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("console: server stopped: %v", err)
	}
}

// ensureFirstAdmin creates the initial admin account with a generated
// password when no users exist, printing it once to the log.
func ensureFirstAdmin(cfg *config.Config, dir string) error {
	if len(cfg.Users) > 0 {
		return nil
	}
	pw, err := auth.GeneratePassword()
	if err != nil {
		return err
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
	log.Printf("console: FIRST RUN: created user 'admin' with generated password: %s", pw)
	log.Printf("console: change it after first login (see README)")
	return nil
}
