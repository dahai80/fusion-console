package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initPushedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitIn(t, ".", "init", "--bare", remote)
	clone := filepath.Join(root, "clone")
	gitIn(t, root, "clone", remote, clone)
	os.WriteFile(filepath.Join(clone, "file.txt"), []byte("one\n"), 0o644)
	gitIn(t, clone, "add", "file.txt")
	gitIn(t, clone, "commit", "-m", "one")
	gitIn(t, clone, "push", "origin", "HEAD")
	return clone
}

func TestAuditPublishStateClean(t *testing.T) {
	clone := initPushedRepo(t)
	if note := auditPublishState(clone); note != "" {
		t.Fatalf("expected no note for clean pushed repo, got: %s", note)
	}
}

func TestAuditPublishStateUncommitted(t *testing.T) {
	clone := initPushedRepo(t)
	os.WriteFile(filepath.Join(clone, "file.txt"), []byte("two\n"), 0o644)
	note := auditPublishState(clone)
	if !strings.Contains(note, "1 uncommitted change(s)") {
		t.Fatalf("expected uncommitted-change note, got: %s", note)
	}
	if !strings.Contains(note, "do not report it as solved") {
		t.Fatalf("expected do-not-report guidance, got: %s", note)
	}
}

func TestAuditPublishStateUntrackedIgnored(t *testing.T) {
	clone := initPushedRepo(t)
	os.WriteFile(filepath.Join(clone, "scratch.tmp"), []byte("x"), 0o644)
	if note := auditPublishState(clone); note != "" {
		t.Fatalf("untracked scratch must not trigger note, got: %s", note)
	}
}

func TestAuditPublishStateUnpushedCommits(t *testing.T) {
	clone := initPushedRepo(t)
	os.WriteFile(filepath.Join(clone, "file.txt"), []byte("two\n"), 0o644)
	gitIn(t, clone, "add", "file.txt")
	gitIn(t, clone, "commit", "-m", "two")
	note := auditPublishState(clone)
	if !strings.Contains(note, "1 unpushed commit(s) on branch") {
		t.Fatalf("expected unpushed-commit note, got: %s", note)
	}
}

func TestAuditPublishStateNeverPushedBranch(t *testing.T) {
	clone := initPushedRepo(t)
	gitIn(t, clone, "checkout", "-b", "topic")
	os.WriteFile(filepath.Join(clone, "file.txt"), []byte("two\n"), 0o644)
	gitIn(t, clone, "add", "file.txt")
	gitIn(t, clone, "commit", "-m", "two")
	note := auditPublishState(clone)
	if !strings.Contains(note, "no upstream") || !strings.Contains(note, "topic") {
		t.Fatalf("expected never-pushed-branch note, got: %s", note)
	}
}

func TestAuditPublishStateNotARepo(t *testing.T) {
	dir := t.TempDir()
	if note := auditPublishState(dir); note != "" {
		t.Fatalf("expected empty note for non-repo, got: %s", note)
	}
	if note := auditPublishState(""); note != "" {
		t.Fatalf("expected empty note for empty workdir, got: %s", note)
	}
}
