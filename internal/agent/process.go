package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 30 * time.Minute

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\r`)

// parseState carries per-run accounting across stdout lines. Parsers stay
// line-local; anything cumulative (usage accumulation for CLIs that only
// report tokens per assistant message, drift sentinels) lives here. Owned by
// the stdout pump goroutine — no locking required.
type parseState struct {
	tokensIn   int
	tokensOut  int
	cacheRead  int
	cacheWrite int
	costSeen   bool // a positive CostUSD event arrived (cost cap authoritative)
	usageSeen  bool // at least one usage event arrived (sentinel input)
	meaningful int  // text/tool/result events seen (sentinel input)
}

// addUsage accumulates one assistant-message usage report into the run tally.
func (st *parseState) addUsage(in, out, cacheRead, cacheWrite int) {
	st.tokensIn += in
	st.tokensOut += out
	st.cacheRead += cacheRead
	st.cacheWrite += cacheWrite
}

// startProcess spawns cmd in its own process group, wires stdout through
// lineFn, merges stderr into error text, and enforces timeout + budget caps.
// It returns the pump goroutine already running; caller consumes events until
// the channel closes.
type processHandle struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	cancel      context.CancelFunc
	mu          sync.Mutex
	budget      float64
	tokenBudget int
	killed      bool
	stderr      strings.Builder
	parser      func(map[string]any, *parseState) []Event
	state       parseState
}

// launch builds and starts the subprocess for an adapter. parser maps one
// stdout JSON object to events; non-JSON lines fall back to plain text.
func launch(ctx context.Context, opts RunOpts, command string, args []string, feedTask bool, parser func(map[string]any, *parseState) []Event) (*processHandle, <-chan Event, error) {
	runCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = opts.Workdir
	if len(opts.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range opts.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	setProcessGroup(cmd)

	out := make(chan Event, 128)
	h := &processHandle{cmd: cmd, cancel: cancel, budget: opts.MaxBudgetUSD, tokenBudget: opts.MaxBudgetTokens, parser: parser}

	var err error
	if feedTask {
		h.stdin, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("agent: stdin pipe: %w", err)
		}
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("agent: stderr pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("agent: stdout pipe: %w", err)
	}

	log.Printf("agent: start cmd=%s workdir=%s", command, opts.Workdir)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("agent: start %s: %w", command, err)
	}

	go func() {
		sc := bufio.NewScanner(stderrPipe)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			h.mu.Lock()
			if h.stderr.Len() < 16*1024 {
				h.stderr.WriteString(sc.Text() + "\n")
			}
			h.mu.Unlock()
		}
	}()

	go func() {
		defer close(out)
		sc := bufio.NewScanner(stdoutPipe)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			for _, ev := range h.handleLine(line) {
				out <- ev
			}
		}
		if err := sc.Err(); err != nil {
			out <- Event{Type: EventError, Text: fmt.Sprintf("stdout read failed: %v", err)}
		}
		err := cmd.Wait()
		h.mu.Lock()
		stderrText := h.stderr.String()
		killed := h.killed
		h.mu.Unlock()
		if err != nil && !killed {
			detail := strings.TrimSpace(stderrText)
			if detail == "" {
				detail = err.Error()
			}
			out <- Event{Type: EventError, Text: truncateString(fmt.Sprintf("%s exited: %s", command, detail), 2000)}
		}
		// Protocol-drift sentinels: a clean exit with nothing parseable, or a
		// stream without usage accounting, must be visible — never silent.
		if err == nil && !killed {
			switch {
			case h.state.meaningful == 0:
				out <- Event{Type: EventError, Text: fmt.Sprintf("%s exited cleanly but produced 0 parseable stream events — suspected protocol drift, check CLI version/output format", command)}
			case !h.state.usageSeen:
				out <- Event{Type: EventNotice, Text: fmt.Sprintf("%s stream contained no usage data — token/cost accounting unavailable for this run", command)}
			}
		}
		out <- Event{Type: EventDone}
		cancel()
	}()
	return h, out, nil
}

// handleLine parses one stdout line into zero or more events, enforcing the
// budget caps by killing the process when exceeded. The cost cap fires on any
// event carrying a positive CostUSD (claude reports it on the result event).
// The token cap is the fallback for CLIs that never report cost: it arms only
// while no positive cost has been observed during this run.
func (h *processHandle) handleLine(line string) []Event {
	events := h.parseLine(line)
	for _, ev := range events {
		if ev.Type == EventUsage {
			h.state.usageSeen = true
		}
		if ev.CostUSD > 0 {
			h.state.costSeen = true
		}
		switch ev.Type {
		case EventTool, EventResult:
			h.state.meaningful++
		case EventText:
			if strings.TrimSpace(ev.Text) != "" {
				h.state.meaningful++
			}
		}
		if h.budget > 0 && ev.CostUSD > h.budget {
			h.kill()
			return []Event{{
				Type: EventError,
				Text: fmt.Sprintf("budget cap $%.2f exceeded (spend $%.2f), process killed", h.budget, ev.CostUSD),
			}}
		}
	}
	if h.tokenBudget > 0 && !h.state.costSeen && h.state.tokensIn+h.state.tokensOut > h.tokenBudget {
		h.kill()
		return []Event{{
			Type: EventError,
			Text: fmt.Sprintf("cost data unavailable; token budget cap %d exceeded (used %d in + %d out), process killed", h.tokenBudget, h.state.tokensIn, h.state.tokensOut),
		}}
	}
	return events
}

func (h *processHandle) kill() {
	h.mu.Lock()
	h.killed = true
	started := h.cmd != nil && h.cmd.Process != nil
	pid := 0
	if started {
		pid = h.cmd.Process.Pid
	}
	h.mu.Unlock()
	if !started {
		return
	}
	log.Printf("agent: killing process tree pid=%d", pid)
	killTree(h.cmd)
}

// parseLine decodes one stdout line: plain text falls through as a text
// delta, JSON goes through the adapter parser, unparsable JSON fails visible.
func (h *processHandle) parseLine(line string) []Event {
	if !strings.HasPrefix(line, "{") {
		return []Event{{Type: EventText, Text: ansiRe.ReplaceAllString(line, "") + "\n"}}
	}
	var raw map[string]any
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return []Event{{Type: EventError, Text: fmt.Sprintf("unparsable agent output line: %v: %.200s", err, line)}}
	}
	return h.parser(raw, &h.state)
}

func truncateString(s string, n int) string {
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}
