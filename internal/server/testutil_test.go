package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/config"
)

// loginCookie logs into the test server as admin and returns the session cookie.
func loginCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	s.cfg.Users = []config.UserEntry{{Name: "admin", PasswordHash: hash}}
	req := httptest.NewRequest("POST", "/api/login", nil)
	rec := httptest.NewRecorder()
	if err := s.auth.Login(rec, req, "admin", hash, "pw"); err != nil {
		t.Fatalf("login: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie issued")
	}
	return cookies[0]
}
