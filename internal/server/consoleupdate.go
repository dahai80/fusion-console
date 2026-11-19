// Local macOS installs can attach a separately supervised updater. Its Unix
// socket stays available while launchd restarts the actual console.
package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var upstreamRevision = ""

// Hold new writes briefly between the final idle check and the restart.
// The deadline releases the gate even if the updater exits unexpectedly.
type consoleUpdateGate struct {
	mu     sync.Mutex
	active int
	until  time.Time
}

func (s *Server) serveWithUpdateGate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasPrefix(r.URL.Path, "/api/console-update") {
		g := &s.updates
		g.mu.Lock()
		if time.Now().Before(g.until) {
			g.mu.Unlock()
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Console 正在更新，请稍后重试"})
			return
		}
		g.active++
		g.mu.Unlock()
		defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) consoleUpdateRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/console-update", s.auth.Require(s.consoleUpdate))
	m.HandleFunc("POST /api/console-update", s.auth.Require(s.consoleUpdate))
	m.HandleFunc("POST /api/console-update/drain", s.drainForUpdate)
}

func (s *Server) drainForUpdate(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	key, err := os.ReadFile(filepath.Join(s.cfgDir, "updater", "drain.key"))
	if !net.ParseIP(ip).IsLoopback() || err != nil || len(key) < 32 || subtle.ConstantTimeCompare(key, []byte(r.Header.Get("X-Console-Drain"))) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	g := &s.updates
	g.mu.Lock()
	defer g.mu.Unlock()
	s.runs.mu.Lock()
	busy := g.active > 0 || len(s.runs.live) > 0 || len(s.agentSema) > 0
	s.runs.mu.Unlock()
	for _, run := range s.bg.list(0) {
		if run.Status == "queued" || run.Status == "running" {
			busy = true
			break
		}
	}
	if busy {
		w.WriteHeader(http.StatusConflict)
		return
	}
	g.until = time.Now().Add(30 * time.Second)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) consoleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		origin := r.Header.Get("Origin")
		u, err := url.Parse(origin)
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" || (origin != "" && (err != nil || !strings.EqualFold(u.Host, r.Host))) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	socket := filepath.Join(s.cfgDir, "updater", "control.sock")
	if _, err := os.Stat(socket); os.IsNotExist(err) && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
		return
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: tr, Timeout: 4 * time.Second}
	var body io.Reader
	if r.Method == http.MethodPost {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
		if err != nil || !json.Valid(data) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的更新请求"})
			return
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, "http://localhost/update", body)
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "本机更新服务暂时不可用"})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 16384))
}
