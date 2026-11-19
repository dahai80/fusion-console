// Optional fusion-mlx integration (Apple Silicon only): the console can
// detect, start and stop the local MLX inference engine (OpenAI-compatible,
// registered port 11434) and fall back to it for a chat turn when the cloud
// gateway is unreachable. Fail-open everywhere — the card simply reports
// "unsupported" off-platform, and a dead MLX never blocks a dispatch.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
)

// mlxSupported reports whether this host can run fusion-mlx at all
// (macOS on Apple Silicon).
func mlxSupported() bool {
	return runtime.GOOS == "darwin" && (runtime.GOARCH == "arm64")
}

func (s *Server) mlxBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.cfg.MLX.Port)
}

// mlxHealthy probes the engine's OpenAI-compatible surface. A TCP-level
// check is enough — fusion-mlx answers /v1/models without auth.
func (s *Server) mlxHealthy(timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(s.mlxBaseURL() + "/v1/models")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

// mlxUsable = the user opted in, the platform can run it, and it is alive.
func (s *Server) mlxUsable() bool {
	return s.cfg.MLX.Enabled && mlxSupported() && s.mlxHealthy(500*time.Millisecond)
}

// mlxProvider synthesizes the local provider entry for fallback turns. Not
// persisted — it exists so buildProvider/one turn can target the engine.
func (s *Server) mlxProvider() *config.Provider {
	model := strings.TrimSpace(s.cfg.MLX.Model)
	if model == "" {
		model = "local"
	}
	return &config.Provider{
		ID:      "fusion-mlx",
		Name:    "fusion-mlx (local)",
		Type:    config.ProviderOpenAICompat,
		BaseURL: s.mlxBaseURL() + "/v1",
		Model:   model,
		Enabled: true,
	}
}

// startMLX launches the engine via its own start.sh (detached) and polls
// health for up to 60s — first model load can be slow.
func (s *Server) startMLX(ctx context.Context) (string, error) {
	if !mlxSupported() {
		return "", fmt.Errorf("fusion-mlx requires macOS on Apple Silicon")
	}
	script := filepath.Join(s.cfg.MLX.InstallDir, "start.sh")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("fusion-mlx not installed at %s — install it first", s.cfg.MLX.InstallDir)
	}
	if s.mlxHealthy(500 * time.Millisecond) {
		return "already running", nil
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", script, "start")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("start.sh start failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("mlx start cancelled")
		default:
		}
		if s.mlxHealthy(500 * time.Millisecond) {
			return "started", nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("fusion-mlx did not answer on :%d within 60s — check its log", s.cfg.MLX.Port)
}

// stopMLX delegates to the engine's own lifecycle script.
func (s *Server) stopMLX() (string, error) {
	script := filepath.Join(s.cfg.MLX.InstallDir, "start.sh")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("fusion-mlx not installed at %s", s.cfg.MLX.InstallDir)
	}
	cmd := exec.Command("/bin/bash", script, "stop")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("start.sh stop failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return "stopped", nil
}

func (s *Server) handleMLXStatus(w http.ResponseWriter, r *http.Request) {
	installed := false
	if mlxSupported() {
		_, err := os.Stat(filepath.Join(s.cfg.MLX.InstallDir, "start.sh"))
		installed = err == nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"supported":   mlxSupported(),
		"installed":   installed,
		"enabled":     s.cfg.MLX.Enabled,
		"healthy":     mlxSupported() && s.mlxHealthy(500*time.Millisecond),
		"port":        s.cfg.MLX.Port,
		"model":       s.cfg.MLX.Model,
		"install_dir": s.cfg.MLX.InstallDir,
	})
}

func (s *Server) handleMLXOp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Op    string `json:"op"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	var note string
	var err error
	switch req.Op {
	case "start":
		note, err = s.startMLX(r.Context())
	case "stop":
		note, err = s.stopMLX()
	case "enable", "disable":
		s.cfg.MLX.Enabled = req.Op == "enable"
		if req.Model != "" {
			s.cfg.MLX.Model = req.Model
		}
		err = s.cfg.Save(s.cfgDir)
		note = "fallback " + req.Op + "d"
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown op"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: mlx op=%s: %s", req.Op, note)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
}
