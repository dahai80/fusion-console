// Worker memory: cross-run learning for workers doing repeated tasks in the
// same workspace. Two mechanisms, selectable via orchestrator.worker_memory:
//
//   - "notes" (default): zero-dependency convention — the task prompt tells
//     the worker to read NOTES.md in its workspace at start and append
//     lessons before finishing. Persistence comes from the workspace itself.
//   - "fusion-memory": additionally recalls relevant past experience from
//     the fusion-memory hub (fm-server HTTP, 127.0.0.1:11435) before the run
//     and commits the task/result afterwards. Best effort: any fm-server
//     failure degrades to the notes convention, never blocks a dispatch.
//
//   - "off": raw task only.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// notesInstruction is prepended to every worker task under the notes and
// fusion-memory modes. English-only per repo convention; the worker reads it
// as part of its task.
const notesInstruction = `[workspace memory] If NOTES.md exists in your workspace, read it first — it holds lessons from previous runs in this directory. Before finishing, append a "## <UTC date> — <one-line task>" entry with durable lessons: pitfalls hit, decisions made, commands that worked, what to try next. Keep NOTES.md under 60 lines by pruning stale entries. Never record secrets or credentials.`

func (s *Server) workerMemoryMode() string {
	m := s.cfg.Orchestrator.WorkerMemory
	if m == "" {
		return "notes"
	}
	return m
}

// memoryServerURL resolves the fusion-memory hub endpoint (env override for
// tests and non-default deployments; the registered port is 11435).
func memoryServerURL() string {
	if u := os.Getenv("FUSION_MEMORY_URL"); u != "" {
		return u
	}
	return "http://127.0.0.1:11435"
}

// memoryPost issues one JSON-RPC-over-HTTP call against fm-server with a hard
// short timeout. fm-server speaks JSON-RPC 2.0 ({"jsonrpc","method","params"},
// RPC method v1.<name> on pinned /v1/ paths) and enforces Bearer auth (B5);
// the key comes from the same env var fm-server itself uses, so both
// processes read one source. Best effort: errors are logged, never propagated.
func memoryPost(ctx context.Context, method string, params any, out any, timeout time.Duration) error {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	envelope := map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": 1}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, memoryServerURL()+"/v1/memory/"+method, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := os.Getenv("FUSION_MEMORY_API_KEY"); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fm-server %s returned %d", method, resp.StatusCode)
	}
	var rpc struct {
		Result *json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return err
	}
	if rpc.Error != nil {
		return fmt.Errorf("fm-server %s rpc error %d: %s", method, rpc.Error.Code, rpc.Error.Message)
	}
	if out != nil && rpc.Result != nil {
		return json.Unmarshal(*rpc.Result, out)
	}
	return nil
}

// recallWorkerMemory fetches past experience relevant to the task from
// fusion-memory. Returns "" when the hub is down or has nothing useful.
func (s *Server) recallWorkerMemory(ctx context.Context, workerID, task string) string {
	var resp struct {
		Blocks []struct {
			TurnsText string  `json:"turns_text"`
			Score     float64 `json:"score"`
		} `json:"blocks"`
	}
	body := map[string]any{"text": task, "top_k": 3, "token_budget": 800}
	if err := memoryPost(ctx, "retrieve", body, &resp, 2*time.Second); err != nil {
		log.Printf("server: worker memory recall unavailable (%v) — falling back to notes", err)
		return ""
	}
	out := ""
	for _, b := range resp.Blocks {
		if t := b.TurnsText; t != "" {
			if out != "" {
				out += "\n---\n"
			}
			out += t
		}
	}
	if out != "" {
		log.Printf("server: worker memory recall worker=%s got %d block(s)", workerID, len(resp.Blocks))
	}
	return out
}

// commitWorkerMemory stores one task/result pair in fusion-memory so future
// dispatches of similar tasks can recall it. Best effort, never fatal.
func (s *Server) commitWorkerMemory(ctx context.Context, workerID, task, result string) {
	if len(result) > 4000 {
		result = result[:4000]
	}
	now := time.Now().Unix()
	body := map[string]any{
		"session_id": "fusion-console-" + workerID,
		"interaction": map[string]any{
			"id":         fmt.Sprintf("ix-%d", now),
			"session_id": "fusion-console-" + workerID,
			"turns": []map[string]any{{
				"turn_idx":          0,
				"user_message":      task,
				"assistant_message": result,
				"tool_calls":        []any{},
			}},
			"timestamp": now,
			"metadata":  map[string]any{"source": "fusion-console", "worker": workerID},
		},
	}
	if err := memoryPost(ctx, "commit", body, nil, 3*time.Second); err != nil {
		log.Printf("server: worker memory commit failed worker=%s: %v", workerID, err)
		return
	}
	log.Printf("server: worker memory committed worker=%s", workerID)
}

// prefixWorkerTask composes the final task text per the configured memory
// mode. Always returns the notes instruction except in "off" mode — the
// workspace convention stays on even when the hub is used, because hub
// recall covers semantics while NOTES.md keeps the workspace-local state.
func (s *Server) prefixWorkerTask(ctx context.Context, workerID, task string) string {
	switch s.workerMemoryMode() {
	case "off":
		return task
	case "fusion-memory":
		if mem := s.recallWorkerMemory(ctx, workerID, task); mem != "" {
			return "[memory — relevant past experience from the memory hub]\n" + mem + "\n\n" + notesInstruction + "\n\n" + task
		}
		return notesInstruction + "\n\n" + task
	default:
		return notesInstruction + "\n\n" + task
	}
}
