// Team group chat: a conversation with kind "team" (agent_name = team name).
// A send in this conversation assigns work to the team — teams.add_task on
// the agent-studio daemon with the message text as the brief, no LLM turn.
// Team replies reach the conversation through inbox polling
// (handleTeamChatPoll) as assistant messages, deduped by msg_id (the daemon
// inbox is non-draining).
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/store"
)

// chatOperatorMember is the console's own roster identity inside a team —
// the sender of task messages and the member whose inbox is polled for
// replies. Registered lazily; the roster accepts any agent id (dispatch to
// it is never attempted).
const chatOperatorMember = "console"

func (s *Server) ensureOperatorMember(team string) {
	if _, err := s.teamRegisterMember(chatOperatorMember, chatOperatorMember, team, "idle", []string{"operator"}); err != nil {
		log.Printf("server: operator member on team %s not registered (may already exist): %v", team, err)
	}
}

// taskTitleFromBrief derives a short task title from the chat message: the
// first line, trimmed to 80 chars.
func taskTitleFromBrief(content string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(content), "\n", 2)[0])
	if len(line) > 80 {
		line = line[:80]
	}
	if line == "" {
		line = "task " + strconv.FormatInt(time.Now().Unix(), 10)
	}
	return line
}

// handleTeamChatTurn serves POST /api/chat for a kind=team conversation:
// persist the user message, assign it as a task, reply with the assignment
// receipt over the same SSE shape the client already consumes.
func (s *Server) handleTeamChatTurn(w http.ResponseWriter, r *http.Request, conv *store.Conversation, content string) {
	if _, err := s.store.AppendMessage(conv.ID, "user", content, "{}"); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	if _, err := s.teamEnsureJWT(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent-studio integration unavailable: " + err.Error()})
		return
	}
	s.ensureOperatorMember(conv.AgentName)

	taskID, hint, err := s.teamChatAssign(conv, content)
	if err != nil {
		log.Printf("server: team chat assign conv=%d team=%s failed: %v", conv.ID, conv.AgentName, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "task assignment failed: " + err.Error()})
		return
	}
	reply := "task assigned to team " + conv.AgentName + ": " + taskID + "." + hint
	meta, _ := json.Marshal(map[string]any{"team": conv.AgentName, "task_id": taskID})
	if _, err := s.store.AppendMessage(conv.ID, "assistant", reply, string(meta)); err != nil {
		log.Printf("server: team chat receipt persist conv=%d: %v", conv.ID, err)
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(ev map[string]any) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	send(map[string]any{"type": "delta", "text": reply})
	send(map[string]any{"type": "done"})
}

// handleTeamChatPoll serves GET /api/team/chat_poll?conversation_id=N: pull
// the operator inbox for the conversation's team and append unseen messages
// as assistant messages. The inbox is non-draining, so dedupe by msg_id
// against the messages already stored.
func (s *Server) handleTeamChatPoll(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("conversation_id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id required"})
		return
	}
	out, err := s.teamChatPullMessages(s.auth.User(r), id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// teamChatPullMessages is the poll core, split from the HTTP handler so the
// dedupe behavior is testable without going through auth.
func (s *Server) teamChatPullMessages(user string, id int64) ([]map[string]any, error) {
	conv, err := s.store.Conversation(user, id)
	if err != nil {
		return nil, fmt.Errorf("conversation not found")
	}
	if conv.Kind != "team" {
		return nil, fmt.Errorf("not a team conversation")
	}
	msgs, err := s.teamInbox(conv.AgentName, chatOperatorMember)
	if err != nil {
		// Operator member may not be registered yet — register and retry once.
		s.ensureOperatorMember(conv.AgentName)
		if msgs, err = s.teamInbox(conv.AgentName, chatOperatorMember); err != nil {
			return nil, err
		}
	}
	existing, err := s.store.ListMessages(id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, m := range existing {
		var meta map[string]any
		if json.Unmarshal([]byte(m.Meta), &meta) == nil {
			if mid, ok := meta["team_msg_id"].(string); ok {
				seen[mid] = true
			}
		}
	}
	out := []map[string]any{}
	for _, m := range msgs {
		mid, _ := m["msg_id"].(string)
		from, _ := m["from"].(string)
		body, _ := m["body"].(string)
		// console#79 R4: kernel-typed messages must not degrade into chat
		// text — they surface as actionable cards via inbox_typed instead.
		if mt, _ := m["type"].(string); mt == "plan_approval" || mt == "shutdown_request" {
			continue
		}
		if mid == "" || seen[mid] || strings.TrimSpace(body) == "" {
			continue
		}
		text := from + ": " + body
		if from == "" {
			text = body
		}
		meta, _ := json.Marshal(map[string]any{"team": conv.AgentName, "team_msg_id": mid, "from": from})
		if _, err := s.store.AppendMessage(id, "assistant", text, string(meta)); err != nil {
			log.Printf("server: team chat poll persist conv=%d msg=%s: %v", id, mid, err)
			continue
		}
		out = append(out, map[string]any{"role": "assistant", "content": text, "meta": string(meta)})
	}
	return out, nil
}

// teamChatAssign assigns one task to the team and returns the receipt text.
func (s *Server) teamChatAssign(conv *store.Conversation, content string) (string, string, error) {
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		return "", "", err
	}
	res, err := teamRPC(studioSocketPath(), jwt, "teams.add_task", map[string]any{
		"team": conv.AgentName, "title": taskTitleFromBrief(content), "detail": content,
	})
	if err != nil {
		return "", "", err
	}
	res, err = teamResult(res)
	if err != nil {
		return "", "", err
	}
	taskID, _ := res["task_id"].(string)
	// Lead-loop hint so the user knows whether anything will pick the task up.
	hint := " Lead loop is not running — start it on the Team page or with teams.lead_start to dispatch queued tasks."
	if ls, err := teamRPC(studioSocketPath(), jwt, "teams.lead_status", map[string]any{"team": conv.AgentName}); err == nil {
		if running, _ := ls["running"].(bool); running {
			hint = " The lead loop is running and will dispatch it."
		}
	}
	return taskID, hint, nil
}
