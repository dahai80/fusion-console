package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleUpdateDrain(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "updater"), 0700); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 32)
	if err := os.WriteFile(filepath.Join(dir, "updater", "drain.key"), []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfgDir: dir, mux: http.NewServeMux()}
	drain := func() int {
		r := httptest.NewRequest(http.MethodPost, "/api/console-update/drain", nil)
		r.RemoteAddr = "127.0.0.1:10000"
		r.Header.Set("X-Console-Drain", key)
		w := httptest.NewRecorder()
		s.drainForUpdate(w, r)
		return w.Code
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.mux.HandleFunc("POST /api/chat", func(http.ResponseWriter, *http.Request) { close(entered); <-release })
	go func() {
		s.serveWithUpdateGate(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/chat", nil))
		close(done)
	}()
	<-entered
	if got := drain(); got != http.StatusConflict {
		t.Errorf("active chat returned %d", got)
	}
	close(release)
	<-done
	s.bg.enqueue(&bgRun{ID: 1, Status: "queued"})
	if got := drain(); got != http.StatusConflict {
		t.Errorf("queued dispatch returned %d", got)
	}
	s.bg.runs[0].Status = "done"
	if got := drain(); got != http.StatusNoContent {
		t.Fatalf("idle drain returned %d", got)
	}
	w := httptest.NewRecorder()
	s.serveWithUpdateGate(w, httptest.NewRequest(http.MethodPost, "/api/agent/run", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("new dispatch during restart returned %d", w.Code)
	}
}
