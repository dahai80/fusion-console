// Live worker run registry: which workers are busy right now and what they
// are doing. Entries appear when a dispatch starts (orchestrator run_agent
// or a manual agent run), update on tool progress, and disappear when the
// run finishes or fails. In-memory only — a console restart clears it,
// which matches reality (no subprocess survives the restart).
package server

import (
	"log"
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
	mu    sync.Mutex
	live  map[string]*workerRun
	last  map[string]*workerRun // most recent finished run, for context
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
