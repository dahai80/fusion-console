// Live worker run registry: which workers are busy right now and what they
// are doing. Entries appear when a dispatch starts (orchestrator run_agent
// or a manual agent run), update on tool progress, and disappear when the
// run finishes or fails. In-memory only — a console restart clears it. The
// registry is reconciled against reality by discoverOrphans: agent CLI
// subprocesses survive the console restart (they are only process-group
// killed on budget/timeout), so a worker whose work_dir has a live agent
// process is reported busy even after the registry was wiped.
package server

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// workerRun is one in-flight dispatch.
type workerRun struct {
	Task      string
	StartedAt time.Time
	Detail    string // latest activity line (agent tool event or heartbeat)
	UpdatedAt time.Time
}

// workerRuns guards the registry.
type workerRuns struct {
	mu   sync.Mutex
	live map[string]*workerRun
	last map[string]*workerRun // most recent finished run, for context
}

func (w *workerRuns) start(id, task string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.live == nil {
		w.live = map[string]*workerRun{}
		w.last = map[string]*workerRun{}
	}
	now := time.Now()
	w.live[id] = &workerRun{Task: task, StartedAt: now, UpdatedAt: now}
	log.Printf("server: worker %s busy: %s", id, task)
}

func (w *workerRuns) progress(id, detail string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.live[id]; ok {
		r.Detail = detail
		r.UpdatedAt = time.Now()
	}
}

func (w *workerRuns) end(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.live[id]; ok {
		delete(w.live, id)
		w.last[id] = r
		log.Printf("server: worker %s idle again (ran %ds)", id, int(time.Since(r.StartedAt).Seconds()))
	}
}

// snapshot returns the live state for one worker id: busy plus what it is
// doing, or idle plus its previous run when known.
func (w *workerRuns) snapshot(id string) map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.live[id]; ok {
		return map[string]any{
			"busy": true, "task": r.Task, "detail": r.Detail,
			"elapsed_s": int(time.Since(r.StartedAt).Seconds()),
		}
	}
	if r, ok := w.last[id]; ok {
		return map[string]any{
			"busy": false, "last_task": r.Task, "last_elapsed_s": int(r.UpdatedAt.Sub(r.StartedAt).Seconds()),
		}
	}
	return map[string]any{"busy": false}
}

// orphanScan is the cached result of one process-walk pass.
type orphanScan struct {
	at   time.Time
	vals map[string]string // work_dir -> "pid startElapsed"
}

var (
	orphanMu    sync.Mutex
	orphanCache orphanScan
)

// agentProcessNames are the CLI commands a worker dispatch can spawn.
var agentProcessNames = map[string]bool{
	"claude": true, "codex": true, "opencode": true, "fusion": true,
}

// discoverOrphans finds live agent CLI processes and maps their working
// directory to "PID <seconds since start>". Used after a console restart:
// the run registry is in-memory and gets wiped, but the agent subprocess
// keeps running and must still show as busy on the dashboard and dispatch
// rail. Cached for 15s — lsof is not cheap and the pollers hit this often.
func discoverOrphans() map[string]string {
	orphanMu.Lock()
	defer orphanMu.Unlock()
	if orphanCache.vals != nil && time.Since(orphanCache.at) < 15*time.Second {
		return orphanCache.vals
	}
	vals := map[string]string{}
	// One ps pass for candidate agent processes...
	out, err := exec.Command("ps", "-axo", "pid=,lstart=,command=").CombinedOutput()
	if err != nil {
		log.Printf("server: orphan scan ps failed: %v", err)
		return vals
	}
	var pids []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 9 {
			continue
		}
		pid, cmd := f[0], strings.Join(f[8:], " ")
		base := cmd
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if !agentProcessNames[base] {
			continue
		}
		pids = append(pids, pid)
	}
	// ...then one lsof pass for their working directories.
	if len(pids) > 0 {
		lo, err := exec.Command("lsof", "-nP", "-d", "cwd", "-Fn", "-p", strings.Join(pids, ",")).CombinedOutput()
		if err == nil {
			var pid string
			for _, line := range strings.Split(string(lo), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "p") {
					pid = strings.TrimPrefix(line, "p")
				} else if strings.HasPrefix(line, "n/") && pid != "" {
					vals[strings.TrimPrefix(line, "n")] = pid
				}
			}
		}
	}
	orphanCache = orphanScan{at: time.Now(), vals: vals}
	return vals
}

// elapsedFromPs returns "how long since this pid started" as a formatted
// string; empty when the pid is gone.
func elapsedFromPs(pid string) string {
	out, err := exec.Command("ps", "-o", "etime=", "-p", pid).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fmtOrphanDetail renders the busy reason for a recovered worker.
func fmtOrphanDetail(pid string) string {
	if e := elapsedFromPs(pid); e != "" {
		return fmt.Sprintf("agent process %s alive since %s (recovered after console restart)", pid, e)
	}
	return ""
}
