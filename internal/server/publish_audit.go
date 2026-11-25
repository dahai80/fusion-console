package server

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// auditPublishState inspects the worker's git workspace after a finished run
// and returns a warning note when finished work exists only locally: uncommitted
// edits, unpushed commits, or a branch that was never pushed. Workers have been
// reporting "solved" for local-only fixes; the note is prepended to the run
// result so the brain, the dispatch summary and auto-continue cannot present it
// as published. Empty string = nothing local-only detected, or workdir is not
// a git working tree (audit does not apply).
func auditPublishState(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		return ""
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workdir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := git("rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return ""
	}
	branch, err := git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" {
		return ""
	}
	problems := make([]string, 0, 2)
	if out, err := git("status", "--porcelain"); err == nil {
		dirty := 0
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			// Untracked scratch files are noise; only tracked edits count.
			if line != "" && !strings.HasPrefix(line, "??") {
				dirty++
			}
		}
		if dirty > 0 {
			problems = append(problems, fmt.Sprintf("%d uncommitted change(s)", dirty))
		}
	}
	upstream, upErr := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if upErr == nil {
		if out, err := git("rev-list", "--count", upstream+"..HEAD"); err == nil {
			if n, cerr := strconv.Atoi(out); cerr == nil && n > 0 {
				problems = append(problems, fmt.Sprintf("%d unpushed commit(s) on branch %s (upstream %s)", n, branch, upstream))
			}
		}
	} else if branch != "HEAD" {
		// No upstream: a never-pushed new branch in an otherwise normal clone.
		problems = append(problems, fmt.Sprintf("branch %s has no upstream — never pushed", branch))
	}
	if len(problems) == 0 {
		return ""
	}
	return fmt.Sprintf("[dispatch audit] Publication not verified in %s: %s. This work is local-only — do not report it as solved, merged or released. If publishing was part of the task, push the topic branch, open the PR and report its URL; otherwise state plainly what remains.", workdir, strings.Join(problems, "; "))
}
