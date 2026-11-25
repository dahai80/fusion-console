package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The workspace method needs the monorepo checkout; without it the request
// must fail fast with a pointer to the PyPI/Homebrew alternatives — never
// fall through into the background job.
func TestStudioInstallWorkspaceRequiresDir(t *testing.T) {
	t.Setenv("FUSION_AGENT_STUDIO_DIR", "")
	if studioDir() != "" {
		t.Skip("workspace checkout present on this host; the 400 path is unreachable")
	}
	s := &Server{}
	req := httptest.NewRequest("POST", "/api/studio/install", strings.NewReader(`{"method":"workspace"}`))
	rec := httptest.NewRecorder()
	s.handleStudioInstall(rec, req)
	if rec.Code != 400 {
		t.Fatalf("workspace install without project dir: got %d, want 400", rec.Code)
	}
}
