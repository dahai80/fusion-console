package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The entire final reply is reserved for this envelope. Quoted examples,
// progress events and ordinary prose never change the dispatch status.
const workerResultContract = "[dispatch result] If a prerequisite prevents you from completing this task, return only a JSON object of the form {\"status\":\"blocked\",\"reason\":\"what is needed to proceed\"} as your final reply. Otherwise reply normally. Publication is part of completion: a task counts as solved only when the work is committed, pushed to a topic branch and a PR is open (report the PR URL). Never claim a merge, release or deployment that did not actually happen — list those steps as pending instead."

type dispatchBlocked struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func (b *dispatchBlocked) Error() string {
	data, _ := json.Marshal(b)
	return string(data)
}

func blockedResult(final string) *dispatchBlocked {
	final = strings.TrimSpace(final)
	if !strings.HasPrefix(final, "{") {
		return nil
	}
	var fields map[string]string
	if json.Unmarshal([]byte(final), &fields) != nil || len(fields) != 2 ||
		fields["status"] != "blocked" || strings.TrimSpace(fields["reason"]) == "" {
		return nil
	}
	return &dispatchBlocked{Status: "blocked", Reason: fields["reason"]}
}

func repoBlocked(problem string) *dispatchBlocked {
	return &dispatchBlocked{Status: "blocked", Reason: problem + ". In Agents > Workers, point a standalone worker's work dir at a Git checkout, bind a GitHub source, or set a worktree worker's local repo path. For a worktree dispatch you can also pass the absolute repository path in run_agent.repo."}
}

func requireGitWorkspace(ctx context.Context, workdir string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = workdir
	// Discover the checkout from the actual working directory, even if the
	// console was launched with Git environment overrides for a different repo.
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return repoBlocked(fmt.Sprintf("Worker needs a Git working tree and could not verify one in %q", workdir))
	}
	return nil
}
