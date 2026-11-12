// Package auth implements password login (bcrypt) and cookie sessions.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "fc_session"
	sessionTTL    = 7 * 24 * time.Hour
)

type session struct {
	user    string
	expires time.Time
}

// Manager issues and validates session cookies. Sessions live in memory;
// a restart logs users in again, which is acceptable for a LAN tool.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]session
}

// NewManager creates an empty session manager.
func NewManager() *Manager {
	return &Manager{sessions: map[string]session{}}
}

// HashPassword bcrypt-hashes a plaintext password.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(h), nil
}

// CheckPassword verifies plaintext against a bcrypt hash.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// Login validates credentials and sets the session cookie. Secure flag is
// enabled automatically when the request arrived over TLS or an HTTPS proxy
// (tailscale serve), so plain-HTTP LAN testing keeps working.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request, user, hash, pw string) error {
	if !CheckPassword(hash, pw) {
		log.Printf("auth: login failed user=%s remote=%s", user, r.RemoteAddr)
		return fmt.Errorf("invalid credentials")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("auth: generate session: %w", err)
	}
	tok := hex.EncodeToString(raw)
	m.mu.Lock()
	m.sessions[tok] = session{user: user, expires: time.Now().Add(sessionTTL)}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	log.Printf("auth: login ok user=%s remote=%s", user, r.RemoteAddr)
	return nil
}

// LoginHash issues a session for a user whose password hash is already
// verified by the caller (e.g. one-time auto-login tokens). It performs no
// password check.
func (m *Manager) LoginHash(w http.ResponseWriter, r *http.Request, user string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("auth: generate session: %w", err)
	}
	tok := hex.EncodeToString(raw)
	m.mu.Lock()
	m.sessions[tok] = session{user: user, expires: time.Now().Add(sessionTTL)}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	log.Printf("auth: session issued user=%s remote=%s", user, r.RemoteAddr)
	return nil
}

// User returns the logged-in user for the request, or empty string.
func (m *Manager) User(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[c.Value]
	if !ok || time.Now().After(s.expires) {
		delete(m.sessions, c.Value)
		return ""
	}
	return s.user
}

// Logout deletes the session and clears the cookie.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		m.mu.Lock()
		delete(m.sessions, c.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// Require wraps handler with the login check; returns 401 when absent.
func (m *Manager) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.User(r) == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if p := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(p, "https") {
		return true
	}
	return false
}

// GeneratePassword returns a random 20-char printable password (first-run admin).
func GeneratePassword() (string, error) {
	buf := make([]byte, 15)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
