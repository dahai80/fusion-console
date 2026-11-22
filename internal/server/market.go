// Skills Market: serve the fusion-skills catalog (registry.json) to the
// Agents -> Skills -> Market tab. The console is a catalog consumer: fetch
// upstream (raw.githubusercontent, gh CLI fallback when raw is unreachable —
// common on CN networks), cache in memory for 10 minutes, and keep the last
// good copy on disk so browsing still works offline. Install v1 links to the
// entry's Releases page; one-click install arrives when the registry carries
// pinned artifacts + sha256 (fusion-skills#1).
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// httpGetBody performs a plain GET with a size cap; the market registry is
// small, so 4 MB is a generous ceiling.
func httpGetBody(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d from %s", resp.StatusCode, url)
	}
	const maxRegistry = 4 << 20
	return io.ReadAll(io.LimitReader(resp.Body, maxRegistry))
}

const (
	marketRegistryURL  = "https://raw.githubusercontent.com/dahai80/fusion-skills/main/registry.json"
	marketRepoSlug     = "dahai80/fusion-skills"
	marketCacheTTL     = 10 * time.Minute
	marketFetchTimeout = 8 * time.Second
)

// marketEntry is the subset the console renders; unknown upstream fields are
// preserved per entry in Raw so schema additions flow through untouched.
type marketEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Category    string `json:"category"`
	Source      string `json:"source"`
	Description struct {
		Zh string `json:"zh"`
		En string `json:"en"`
	} `json:"description"`
	Doc      string `json:"doc"`
	Releases string `json:"releases,omitempty"`
	// Normalized by the console (not upstream fields):
	ReleasesURL string `json:"releases_url"`
	DocURL      string `json:"doc_url"`
}

type marketRegistry struct {
	Version   int           `json:"version"`
	Repo      string        `json:"repo"`
	Entries   []marketEntry `json:"entries"`
	FetchedAt time.Time     `json:"fetched_at"`
	Cached    bool          `json:"cached"` // true = served from memory/disk cache
}

// diskCachePath keeps the last good registry copy for offline browsing.
func (s *Server) diskCachePath() string {
	return filepath.Join(s.cfgDir, "market-registry.json")
}

// handleMarketRegistry serves the catalog: memory cache -> raw fetch ->
// gh CLI fallback -> disk cache. Always returns 200 with a body when any
// copy exists; a plain error only when nothing at all is available.
func (s *Server) handleMarketRegistry(w http.ResponseWriter, r *http.Request) {
	s.marketMu.Lock()
	defer s.marketMu.Unlock()
	if s.marketCache != nil && time.Since(s.marketCache.FetchedAt) < marketCacheTTL {
		out := *s.marketCache
		out.Cached = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), marketFetchTimeout)
	defer cancel()
	body, err := fetchMarketRegistry(ctx)
	if err != nil {
		log.Printf("server: market registry fetch failed: %v — trying disk cache", err)
		if reg := s.loadMarketDiskCache(); reg != nil {
			out := *reg
			out.Cached = true
			writeJSON(w, http.StatusOK, out)
			return
		}
		serverError(w, fmt.Errorf("registry unreachable and no cached copy: %v", err), http.StatusBadGateway)
		return
	}
	reg, err := parseMarketRegistry(body)
	if err != nil {
		serverError(w, err, http.StatusBadGateway)
		return
	}
	s.marketCache = reg
	_ = s.saveMarketDiskCache(reg) // best-effort offline copy
	writeJSON(w, http.StatusOK, *reg)
}

// fetchMarketRegistry tries raw.githubusercontent first, then the gh CLI
// (authenticated API access survives DNS/TLS blocks on the raw host).
func fetchMarketRegistry(ctx context.Context) ([]byte, error) {
	body, rawErr := httpGetBody(ctx, marketRegistryURL)
	if rawErr == nil {
		return body, nil
	}
	log.Printf("server: market raw fetch failed (%v) — falling back to gh api", rawErr)
	out, err := exec.CommandContext(ctx, "gh", "api",
		"repos/"+marketRepoSlug+"/contents/registry.json", "--jq", ".content").Output()
	if err != nil {
		return nil, fmt.Errorf("raw: %v; gh api: %v", rawErr, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("gh api: decode base64 content: %v", err)
	}
	return decoded, nil
}

// parseMarketRegistry decodes and normalizes entries: every entry gets a
// resolvable releases_url (upstream field, else the source repo's Releases
// page) and doc_url (GitHub blob link into fusion-skills).
func parseMarketRegistry(body []byte) (*marketRegistry, error) {
	var reg marketRegistry
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, fmt.Errorf("registry.json: %v", err)
	}
	if len(reg.Entries) == 0 {
		return nil, fmt.Errorf("registry.json: no entries")
	}
	reg.FetchedAt = time.Now()
	for i := range reg.Entries {
		e := &reg.Entries[i]
		if e.Releases != "" {
			e.ReleasesURL = e.Releases
		} else if e.Source != "" {
			e.ReleasesURL = "https://github.com/dahai80/" + e.Source + "/releases"
		}
		if e.Doc != "" {
			e.DocURL = "https://github.com/" + marketRepoSlug + "/blob/main/" + strings.TrimPrefix(e.Doc, "/")
		}
	}
	return &reg, nil
}

func (s *Server) loadMarketDiskCache() *marketRegistry {
	data, err := os.ReadFile(s.diskCachePath())
	if err != nil {
		return nil
	}
	var reg marketRegistry
	if err := json.Unmarshal(data, &reg); err != nil || len(reg.Entries) == 0 {
		return nil
	}
	return &reg
}

func (s *Server) saveMarketDiskCache(reg *marketRegistry) error {
	data, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfgDir, 0o700); err != nil {
		return err
	}
	tmp := s.diskCachePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.diskCachePath())
}
