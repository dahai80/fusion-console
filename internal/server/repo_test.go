package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit is a small helper for test-repo setup; failures are fatal.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestSyncRepo(t *testing.T) {
	origin := t.TempDir()
	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "main", ".")
	os.WriteFile(filepath.Join(seed, "hello.txt"), []byte("v1\n"), 0o600)
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "v1")
	runGit(t, origin, "init", "--bare", "-b", "main", ".")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")

	base := t.TempDir()

	// First sync clones the seed repo into <base>/repo.
	target, warn, err := syncRepo(base, "w1", origin, "")
	if err != nil || warn != "" {
		t.Fatalf("clone sync: err=%v warn=%q", err, warn)
	}
	b, err := os.ReadFile(filepath.Join(target, "hello.txt"))
	if err != nil || string(b) != "v1\n" {
		t.Fatalf("cloned content: err=%v body=%q", err, b)
	}

	// A new upstream commit is picked up by a fast-forward pull.
	os.WriteFile(filepath.Join(seed, "hello.txt"), []byte("v2\n"), 0o600)
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "v2")
	runGit(t, seed, "push", "origin", "main")
	target, warn, err = syncRepo(base, "w1", origin, "")
	if err != nil || warn != "" {
		t.Fatalf("pull sync: err=%v warn=%q", err, warn)
	}
	b, _ = os.ReadFile(filepath.Join(target, "hello.txt"))
	if string(b) != "v2\n" {
		t.Fatalf("expected v2 after pull, got %q", b)
	}

	// Dirty workspace: pull is skipped with a warning, files untouched.
	os.WriteFile(filepath.Join(target, "local.txt"), []byte("mine\n"), 0o600)
	_, warn, err = syncRepo(base, "w1", origin, "")
	if err != nil {
		t.Fatalf("dirty sync errored: %v", err)
	}
	if !strings.Contains(warn, "uncommitted changes") {
		t.Fatalf("expected dirty warning, got %q", warn)
	}
	if _, err := os.Stat(filepath.Join(target, "local.txt")); err != nil {
		t.Fatalf("local file was removed: %v", err)
	}

	// Missing URL fails with a clear error.
	if _, _, err := syncRepo(base, "w1", "", ""); err == nil {
		t.Fatal("expected error for empty repo URL")
	}

	// In-place sync: when the base dir itself is a checkout of the repo,
	// syncRepo must use it as-is (no nested repo/ dir) and fast-forward it.
	inplace := t.TempDir()
	runGit(t, inplace, "clone", origin, ".")
	target, warn, err = syncRepo(inplace, "w2", origin, "")
	if err != nil || warn != "" {
		t.Fatalf("inplace sync: err=%v warn=%q", err, warn)
	}
	if target != inplace {
		t.Fatalf("expected in-place target %s, got %s", inplace, target)
	}
	if _, err := os.Stat(filepath.Join(inplace, "repo")); err == nil {
		t.Fatal("nested repo/ dir created despite existing checkout")
	}
	os.WriteFile(filepath.Join(seed, "hello.txt"), []byte("v3\n"), 0o600)
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "v3")
	runGit(t, seed, "push", "origin", "main")
	if _, warn, err = syncRepo(inplace, "w2", origin, ""); err != nil || warn != "" {
		t.Fatalf("inplace pull: err=%v warn=%q", err, warn)
	}
	if b, _ := os.ReadFile(filepath.Join(inplace, "hello.txt")); string(b) != "v3\n" {
		t.Fatalf("expected v3 after in-place pull, got %q", b)
	}

	// URL detection: URLs and scp-like remotes, not local paths.
	for _, u := range []string{"https://github.com/o/r.git", "http://x/r", "git@github.com:o/r.git", "ssh://git@host/r"} {
		if !isRepoURL(u) {
			t.Fatalf("isRepoURL(%q) = false", u)
		}
	}
	for _, p := range []string{"/Users/x/repo", "repo", "./repo", "~/repo", ""} {
		if isRepoURL(p) {
			t.Fatalf("isRepoURL(%q) = true", p)
		}
	}
}
