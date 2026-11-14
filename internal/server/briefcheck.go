// C2 brief quality checker (issue #4): deterministic pre-flight validation
// of the run_agent brief before dispatch. Pure rule checks, no model calls —
// missing items become a non-blocking warning surfaced in the chat tool
// feed, per the MAST finding that vague briefs are the top pre-execution
// failure mode (arXiv 2503.13657).
package server

import "strings"

// briefWarning is one missing contract item.
type briefWarning struct {
	Item string // objective | output format | tool guidance | boundaries | budget
	Fix  string
}

// checkBrief validates the spawn payload against the brief contract:
// objective / output format / tool+source guidance / task boundaries / token
// budget. Warnings only — never blocks the dispatch.
func checkBrief(task string) []briefWarning {
	t := strings.ToLower(task)
	var out []briefWarning
	if len(strings.Fields(task)) < 8 {
		out = append(out, briefWarning{"objective", "brief under 8 words — state the goal and the success criteria explicitly"})
	}
	if !containsAny(t, []string{"output", "format", "return", "deliver", "produce", "report", "file ", "json", "markdown", "table"}) {
		out = append(out, briefWarning{"output format", "no expected output format — say what artifact the worker should produce"})
	}
	if !containsAny(t, []string{"use ", "with ", "tool", "source", "docs", "reference", "path", "repo"}) {
		out = append(out, briefWarning{"tool guidance", "no tool or source guidance — point at the files/commands/sources to use"})
	}
	if !containsAny(t, []string{"only", "do not", "don't", "avoid", "within", "scope", "limit", "must not", "stop"}) {
		out = append(out, briefWarning{"boundaries", "no task boundaries — say what is out of scope and when to stop"})
	}
	if !containsAny(t, []string{"budget", "token", "step", "time", "minute", "iteration"}) {
		out = append(out, briefWarning{"budget", "no budget hint — cap the expected tokens/steps so the worker plans within them"})
	}
	return out
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
