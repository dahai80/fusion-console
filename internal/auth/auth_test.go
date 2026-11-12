package auth

import (
	"net/http/httptest"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(h, "s3cret") {
		t.Error("correct password rejected")
	}
	if CheckPassword(h, "wrong") {
		t.Error("wrong password accepted")
	}
}

func TestLoginAndSession(t *testing.T) {
	m := NewManager()
	hash, _ := HashPassword("pw")
	req := httptest.NewRequest("POST", "/api/login", nil)
	rec := httptest.NewRecorder()
	if err := m.Login(rec, req, "admin", hash, "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie || cookies[0].Value == "" {
		t.Fatalf("session cookie missing: %v", cookies)
	}
	if cookies[0].Secure {
		t.Error("plain HTTP request must not set Secure cookie")
	}

	// Authenticated request reuses the cookie.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(cookies[0])
	if got := m.User(req2); got != "admin" {
		t.Errorf("User = %q, want admin", got)
	}

	// Wrong password rejected.
	if err := m.Login(httptest.NewRecorder(), req, "admin", hash, "bad"); err == nil {
		t.Error("wrong password accepted")
	}
}

func TestSessionInvalidationOnLogout(t *testing.T) {
	m := NewManager()
	hash, _ := HashPassword("pw")
	req := httptest.NewRequest("POST", "/api/login", nil)
	rec := httptest.NewRecorder()
	_ = m.Login(rec, req, "admin", hash, "pw")
	cookie := rec.Result().Cookies()[0]

	req2 := httptest.NewRequest("POST", "/api/logout", nil)
	req2.AddCookie(cookie)
	m.Logout(httptest.NewRecorder(), req2)

	req3 := httptest.NewRequest("GET", "/", nil)
	req3.AddCookie(cookie)
	if m.User(req3) != "" {
		t.Error("session survived logout")
	}
}
