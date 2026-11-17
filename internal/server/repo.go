package server

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// isRepoURL reports whether repo looks like a URL rather than a local path.
func isRepoURL(repo string) bool {
	r := strings.TrimSpace(repo)
	return strings.HasPrefix(r, "http://") || strings.HasPrefix(r, "https://") ||
		strings.HasPrefix(r, "git@") || strings.HasPrefix(r, "ssh://")
}

// syncRepo prepares a workspace from a git remote. Used for worker "github"
// isolation and for the brain's own code source. The sync strategy is:
//
//   - repo not present in the base dir  -> git clone (branch pinned when set)
//   - repo present and clean/behind     -> fast-forward pull to origin
//   - local commits or a dirty tree     -> no pull; a warning is returned so
//     the dispatch feed can surface it. Local state is never overwritten.
//
// Returns the directory to work in plus a best-effort warning.
func syncRepo(base, label, url, branch string) (string, string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", "", fmt.Errorf("%s: repository URL is empty", label)
	}
	target := filepath.Join(base, "repo")
	branch = strings.TrimSpace(branch)

	if _, err := os.Stat(filepath.Join(target, ".git")); os.IsNotExist(err) {
		args := []string{"clone"}
		if branch != "" {
			args = append(args, "--branch", branch)
		}
		args = append(args, "--", url, target)
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			return "", "", fmt.Errorf("git clone %s failed: %v: %s", url, err, tailOut(out))
		}
		log.Printf("repo sync: %s cloned %s into %s", label, url, target)
		return target, "", nil
	}

	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", target}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := git("fetch", "origin"); err != nil {
		return target, fmt.Sprintf("git fetch failed (%s) — continuing with local state", tailOut([]byte(out))), nil
	}
	name := branch
	if name == "" {
		b, err := git("rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || b == "" || b == "HEAD" {
			return target, "could not determine current branch — skipping pull", nil
		}
		name = b
	}
	dirty, _ := git("status", "--porcelain")
	if dirty != "" {
		return target, "workspace has uncommitted changes — skipped pull to latest", nil
	}
	ahead, _ := git("rev-list", "--count", "HEAD.."+name)
	if ahead != "" && ahead != "0" {
		return target, fmt.Sprintf("local branch is %s commit(s) ahead of origin/%s — skipped pull", ahead, name), nil
	}
	behind, _ := git("rev-list", "--count", name+"..origin/"+name)
	if behind == "" || behind == "0" {
		return target, "", nil // already up to date
	}
	if out, err := git("merge", "--ff-only", "origin/"+name); err != nil {
		return target, fmt.Sprintf("fast-forward to origin/%s failed (%s)", name, tailOut([]byte(out))), nil
	}
	log.Printf("repo sync: %s fast-forwarded %s (%s commit(s))", label, name, behind)
	return target, "", nil
}

// tailOut keeps the last line of combined git output for error messages —
// git writes progress lines that would otherwise flood logs.
func tailOut(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if n := len(lines); n > 0 {
		return lines[n-1]
	}
	return ""
}
