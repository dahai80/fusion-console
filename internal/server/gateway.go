package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
)

const gatewayRepo = "dahai80/fusion-gateway"

func defaultGatewayBase() string { return "http://127.0.0.1:11432" }

// gatewayBase returns the configured gateway base URL (first enabled
// gateway-type provider) or the default local endpoint.
func (s *Server) gatewayBase() string {
	for _, p := range s.cfg.Provider {
		if p.Type == "gateway" && p.Enabled && p.BaseURL != "" {
			return strings.TrimSuffix(strings.TrimSuffix(p.BaseURL, "/v1"), "/")
		}
	}
	return defaultGatewayBase()
}

// gatewayBinaryPath is where the console installs the gateway binary.
func gatewayBinaryPath() string {
	home, _ := os.UserHomeDir()
	name := "fusion-gateway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".fusion-console", "bin", name)
}

// bundledGatewayPath returns the gateway binary shipped next to the console
// executable in release packages (fusion-console and fusion-gateway sit
// side by side). Empty string when no bundled copy exists.
func bundledGatewayPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	name := "fusion-gateway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// handleGatewayStatus probes the gateway health endpoint and reports admin
// GUI reachability.
func (s *Server) handleGatewayStatus(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	adminURL := base + "/admin/"
	reachable := false
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/api/health")
	if err == nil {
		resp.Body.Close()
		reachable = resp.StatusCode == http.StatusOK
	}
	// Health route may 404 or differ across versions; any HTTP response on
	// the root means something is listening.
	if !reachable {
		resp2, err2 := client.Get(base + "/")
		if err2 == nil {
			resp2.Body.Close()
			reachable = true
		}
	}
	installed := false
	bundled := bundledGatewayPath() != ""
	if bundled {
		installed = true
	} else if _, err := os.Stat(gatewayBinaryPath()); err == nil {
		installed = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base_url":  base,
		"admin_url": adminURL,
		"reachable": reachable,
		"installed": installed,
		"bundled":   bundled,
		"binary":    gatewayBinaryPath(),
		"admin_gui": true,
	})
}

// handleGatewaySSO logs into the gateway admin API server-side using the
// stored gateway admin credentials (keychain ids gateway-admin-user /
// gateway-admin-pass) and relays the admin_token session cookie to the
// browser. Cookies are host-scoped (ports are ignored), so after this relay
// opening http://host:11432/admin/ is already authenticated — single login
// via fusion-console, no second gateway login prompt.
func (s *Server) handleGatewaySSO(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	user, err := config.GetAPIKey("gateway-admin-user")
	if err != nil || user == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "gateway admin username not set — save it on the Gateway page first"})
		return
	}
	pass, err := config.GetAPIKey("gateway-admin-pass")
	if err != nil || pass == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "gateway admin password not set — save it on the Gateway page first"})
		return
	}
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		base+"/admin/api/login", bytes.NewReader(body))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		serverError(w, fmt.Errorf("gateway login unreachable: %w", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": fmt.Sprintf("gateway admin login failed (HTTP %d) — check stored credentials", resp.StatusCode)})
		return
	}

	// Relay the gateway session cookie (admin_token) to the browser so the
	// admin GUI on the gateway port is pre-authenticated.
	var token string
	for _, c := range resp.Cookies() {
		if c.Name == "admin_token" && c.Value != "" {
			token = c.Value
		}
	}
	if token == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "gateway login succeeded but returned no session cookie"})
		return
	}
	secure := strings.HasPrefix(base, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	log.Printf("server: gateway SSO login ok user=%s", user)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGatewayKeyProvision creates a fusion-gateway API key for the console
// via the gateway admin API (login with the stored admin credentials, then
// POST /admin/api/keys) and stores the raw key in the OS keychain under
// "gateway-key". This removes the manual "open admin GUI, create a key, copy
// it back" loop from onboarding.
func (s *Server) handleGatewayKeyProvision(w http.ResponseWriter, r *http.Request) {
	base := s.gatewayBase()
	user, err := config.GetAPIKey("gateway-admin-user")
	if err != nil || user == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "gateway admin username not set — save it on the Gateway page first"})
		return
	}
	pass, err := config.GetAPIKey("gateway-admin-pass")
	if err != nil || pass == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "gateway admin password not set — save it on the Gateway page first"})
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/admin/api/login", bytes.NewReader(body))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("gateway unreachable: %v", err)})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": fmt.Sprintf("gateway admin login failed (HTTP %d)", resp.StatusCode)})
		return
	}
	var token string
	for _, c := range resp.Cookies() {
		if c.Name == "admin_token" && c.Value != "" {
			token = c.Value
		}
	}
	if token == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "gateway login returned no session cookie"})
		return
	}

	keyBody, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("fusion-console-%d", time.Now().Unix())})
	req2, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/admin/api/keys", bytes.NewReader(keyBody))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Cookie", "admin_token="+token)
	resp2, err := client.Do(req2)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("key creation request failed: %v", err)})
		return
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("gateway key creation returned HTTP %d", resp2.StatusCode)})
		return
	}
	var created struct {
		RawKey    string `json:"raw_key"`
		KeyPrefix string `json:"key_prefix"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&created); err != nil || created.RawKey == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "gateway key creation returned no raw key"})
		return
	}
	if err := config.StoreAPIKey("gateway-key", created.RawKey); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: gateway api key provisioned (prefix %s)", created.KeyPrefix)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key_id": "gateway-key", "key_prefix": created.KeyPrefix})
}

// handleGatewayInstall downloads the latest fusion-gateway release for the
// current platform into ~/.fusion-console/bin and extracts the binary.
func (s *Server) handleGatewayInstall(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	version := strings.TrimPrefix(r.FormValue("version"), "v")
	// Release packages ship fusion-gateway next to the console binary; use
	// that copy instead of downloading when present.
	if bundled := bundledGatewayPath(); bundled != "" {
		if err := copyGatewayFromBundled(bundled); err != nil {
			serverError(w, err, http.StatusInternalServerError)
			return
		}
		log.Printf("server: gateway installed from bundled copy %s", bundled)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "binary": gatewayBinaryPath(), "version": "bundled",
			"note": "installed from the copy bundled with this package",
		})
		return
	}
	if version == "" {
		latest, err := latestGatewayVersion(r.Context())
		if err != nil {
			serverError(w, err, http.StatusBadGateway)
			return
		}
		version = latest
	}
	asset := gatewayAssetName(runtime.GOOS, runtime.GOARCH)
	log.Printf("server: gateway install: version=%s asset=%s", version, asset)

	path, err := installGateway(r.Context(), version, asset)
	if err != nil {
		serverError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "binary": path, "version": version,
		"note": "installed; start it with your gateway config to finish setup",
	})
}

// copyGatewayFromBundled copies the package-shipped gateway binary into
// ~/.fusion-console/bin so the standard gatewayBinaryPath always resolves.
func copyGatewayFromBundled(src string) error {
	dst := gatewayBinaryPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("gateway: create install dir: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("gateway: open bundled copy: %w", err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("gateway: create temp file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("gateway: copy bundled binary: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("gateway: write temp file: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("gateway: chmod: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("gateway: finalize install: %w", err)
	}
	return nil
}

func latestGatewayVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+gatewayRepo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway: query latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway: latest release query returned %d", resp.StatusCode)
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("gateway: parse release info: %w", err)
	}
	if out.TagName == "" {
		return "", fmt.Errorf("gateway: no releases published")
	}
	return strings.TrimPrefix(out.TagName, "v"), nil
}

func gatewayAssetName(goos, goarch string) string {
	switch goos {
	case "windows":
		return fmt.Sprintf("fusion-gateway-%s-%s.zip", goos, goarch)
	default:
		return fmt.Sprintf("fusion-gateway-%s-%s.tar.gz", goos, goarch)
	}
}

func installGateway(ctx context.Context, version, asset string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", gatewayRepo, version, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway: download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("gateway: release asset %s does not exist for v%s (check the releases page)", asset, version)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway: download %s returned %d", url, resp.StatusCode)
	}

	binPath := gatewayBinaryPath()
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return "", fmt.Errorf("gateway: create install dir: %w", err)
	}
	tmp := binPath + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("gateway: create temp file: %w", err)
	}

	switch {
	case strings.HasSuffix(asset, ".tar.gz"):
		if err := extractTarGz(resp.Body, out, "fusion-gateway"); err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: extract: %w", err)
		}
	case strings.HasSuffix(asset, ".zip"):
		// Need a seekable file for zip: buffer to temp archive first.
		archive := binPath + ".asset.zip"
		af, err := os.Create(archive)
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: buffer asset: %w", err)
		}
		if _, err := io.Copy(af, resp.Body); err != nil {
			af.Close()
			out.Close()
			os.Remove(archive)
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: download asset: %w", err)
		}
		af.Close()
		err = extractZip(archive, out, "fusion-gateway")
		os.Remove(archive)
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: extract: %w", err)
		}
	default:
		// Single raw binary asset.
		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			os.Remove(tmp)
			return "", fmt.Errorf("gateway: download: %w", err)
		}
	}
	out.Close()

	mode := os.FileMode(0o755)
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("gateway: chmod: %w", err)
	}
	if err := os.Rename(tmp, binPath); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("gateway: finalize install: %w", err)
	}
	log.Printf("server: gateway installed at %s (v%s)", binPath, version)
	return binPath, nil
}

// extractTarGz copies the named member out of a tar.gz stream.
func extractTarGz(r io.Reader, out io.Writer, member string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("member %q not found in archive", member)
		}
		if err != nil {
			return err
		}
		name := filepath.Base(hdr.Name)
		if name == member && hdr.Typeflag == tar.TypeReg {
			_, err := io.Copy(out, tr)
			return err
		}
	}
}

// extractZip copies the named member out of a zip file.
func extractZip(path string, out io.Writer, member string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) == member && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			_, cErr := io.Copy(out, rc)
			return cErr
		}
	}
	return fmt.Errorf("member %q not found in archive", member)
}
