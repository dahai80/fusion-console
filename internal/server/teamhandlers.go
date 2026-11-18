// Team view HTTP surface: the snapshot read side plus the operations the
// daemon exposes — task cancel/rerun, team.set_limits, and the lifecycle ops
// teams.pause / teams.resume / teams.disband plus the lead loop
// teams.lead_start / lead_stop / lead_status (issue #17; upstream RPCs landed
// in fusion-agent-studio). The playbook panel (issue #14) rides the same
// proxy via teams.playbook_render / teams.playbook_trace.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// teamOpReq is the POST /api/team/op body.
type teamOpReq struct {
	Op             string   `json:"op"`
	TaskID         string   `json:"task_id"`
	EntryID        string   `json:"entry_id"`
	MaxConcurrent  *int     `json:"max_concurrent"`
	MaxDepth       *int     `json:"max_depth"`
	AgentID        string   `json:"agent_id"`
	Name           string   `json:"name"`
	Team           string   `json:"team"`
	Status         string   `json:"status"`
	Capabilities   []string `json:"capabilities"`
	Goal           string   `json:"goal"`
	MaxRounds      int      `json:"max_rounds"`
	TeamBudget     any      `json:"team_budget"`
	CleanupInboxes *bool    `json:"cleanup_inboxes"`
	Text           string   `json:"text"`
}

// teamOpMethod maps a console team op to the daemon JSON-RPC method and
// params. defTeam is the configured default team (used when the request does
// not name one). Returns an error for unknown ops or missing required fields.
func teamOpMethod(req teamOpReq, defTeam string) (string, map[string]any, error) {
	team := req.Team
	if team == "" {
		team = defTeam
	}
	switch req.Op {
	case "cancel", "rerun":
		if req.TaskID == "" {
			return "", nil, errTeamOp("task_id required")
		}
		if req.Op == "cancel" {
			return "task.cancel", map[string]any{"task_id": req.TaskID}, nil
		}
		return "task.rerun", map[string]any{"task_id": req.TaskID}, nil
	case "set_limits":
		p := map[string]any{}
		if req.MaxConcurrent != nil {
			p["max_concurrent"] = *req.MaxConcurrent
		}
		if req.MaxDepth != nil {
			p["max_depth"] = *req.MaxDepth
		}
		return "team.set_limits", p, nil
	case "pause":
		return "teams.pause", map[string]any{"team": team}, nil
	case "resume":
		return "teams.resume", map[string]any{"team": team}, nil
	case "disband":
		cleanup := true
		if req.CleanupInboxes != nil {
			cleanup = *req.CleanupInboxes
		}
		return "teams.disband", map[string]any{"team": team, "cleanup_inboxes": cleanup}, nil
	case "lead_start":
		if req.Goal == "" {
			return "", nil, errTeamOp("goal required")
		}
		p := map[string]any{"team": team, "goal": req.Goal}
		if req.MaxRounds > 0 {
			p["max_rounds"] = req.MaxRounds
		}
		if req.TeamBudget != nil {
			p["team_budget"] = req.TeamBudget
		}
		return "teams.lead_start", p, nil
	case "lead_stop":
		return "teams.lead_stop", map[string]any{"team": team}, nil
	case "lead_status":
		return "teams.lead_status", map[string]any{"team": team}, nil
	case "playbook_render":
		// Read-only playbook surface (issue #14, upstream agent-studio#733).
		// The daemon reports "playbook disabled" / "no playbook store" via the
		// {"status":"error"} shape — surfaced as 502 by handleTeamOp.
		return "teams.playbook_render", map[string]any{"team": team}, nil
	case "playbook_trace":
		if req.EntryID == "" {
			return "", nil, errTeamOp("entry_id required")
		}
		return "teams.playbook_trace", map[string]any{"team": team, "entry_id": req.EntryID}, nil
	case "spec_read":
		// Team SPEC.md view (issue #41) — the group-chat SPEC entry point
		// reads through here; a missing file surfaces as the daemon's error.
		return "teams.spec_read", map[string]any{"team": team}, nil
	case "spec_write":
		// Human/console SPEC edit (v1 write path): text is the full new
		// SPEC.md body; the daemon refuses writes on non-ACTIVE teams.
		if strings.TrimSpace(req.Text) == "" {
			return "", nil, errTeamOp("text required")
		}
		return "teams.spec_write", map[string]any{"team": team, "text": req.Text}, nil
	default:
		return "", nil, errTeamOp("unknown op: " + req.Op)
	}
}

type teamOpError string

func (e teamOpError) Error() string { return string(e) }

func errTeamOp(msg string) error { return teamOpError(msg) }

// handleTeamView serves GET /api/team/view — the latest observer snapshot.
// An optional ?team=<name> observes a different team on demand (synchronous
// poll, no caching) — the wizard's success deep-link lands here filtered to
// the newly created team (issue #16).
func (s *Server) handleTeamView(w http.ResponseWriter, r *http.Request) {
	team := strings.TrimSpace(r.URL.Query().Get("team"))
	if team != "" && team != s.cfg.AgentStudio.Team {
		writeJSON(w, http.StatusOK, s.pollTeam(team))
		return
	}
	writeJSON(w, http.StatusOK, s.teamView())
}

// handleTeamAgents serves GET /api/team/agents — the daemon's registered
// agents for the add-member pick-list (real workers only).
func (s *Server) handleTeamAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.teamListAgents()
	if err != nil {
		log.Printf("server: team agents list: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

// handleTeamOp serves POST /api/team/op — pass-through operations to the
// daemon. Body: {"op": "cancel"|"rerun", "task_id": "..."},
// {"op": "set_limits", "max_concurrent": N, "max_depth": N},
// {"op": "pause"|"resume"|"disband"|"lead_stop"|"lead_status", "team": "..."},
// {"op": "lead_start", "team": "...", "goal": "..."} or
// {"op": "spec_read"|"spec_write", "team": "...", "text": "..."} (issue #41).
func (s *Server) handleTeamOp(w http.ResponseWriter, r *http.Request) {
	var in teamOpReq
	// 256KB: spec_write carries the full SPEC.md body (issue #41) — the old
	// 8KB op budget would truncate legit specs. Ops other than spec_write
	// never come close; the daemon still bounds what it accepts.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		log.Printf("server: team op auth: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// register_member = the console-side team-creation entry: passes
	// team.roster_register through to the daemon verbatim.
	if in.Op == "register_member" {
		if in.AgentID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id required"})
			return
		}
		status := in.Status
		if status == "" {
			status = "idle"
		}
		res, err := s.teamRegisterMember(in.AgentID, in.Name, in.Team, status, in.Capabilities)
		if err != nil {
			log.Printf("server: team register_member: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("server: team register_member agent=%s team=%s", in.AgentID, in.Team)
		writeJSON(w, http.StatusOK, res)
		return
	}
	method, params, err := teamOpMethod(in, s.cfg.AgentStudio.Team)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := teamRPC(studioSocketPath(), jwt, method, params)
	if err != nil {
		log.Printf("server: team op %s: %v", in.Op, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// The daemon's teams.* dispatchers return {"status":"error","message":...}
	// as a successful RPC result — surface it as a failure, never silent.
	if res["status"] == "error" {
		msg, _ := res["message"].(string)
		log.Printf("server: team op %s rejected: %s", in.Op, msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	log.Printf("server: team op %s executed (team=%s task=%s)", in.Op, in.Team, in.TaskID)
	writeJSON(w, http.StatusOK, res)
}

// teamSend proxies teams.send for the chat-side brain tool: one TeamMessage
// into the team mailbox. Sender and recipient must be existing team members
// (kernel-validated) — register one with create_team_member first if needed.
// Non-draining: this only delivers; read replies back with teamInbox.
func (s *Server) teamSend(team, from, to, body, msgType, replyTo string, mentions []string) (string, string, error) {
	if from == "" || to == "" {
		return "", "", fmt.Errorf("team send needs both from and to (existing member names)")
	}
	if body == "" {
		return "", "", fmt.Errorf("team send body is required")
	}
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		return "", "", err
	}
	if msgType == "" {
		msgType = "text"
	}
	params := map[string]any{"team": team, "type": msgType, "from": from, "to": to, "body": body}
	if replyTo != "" {
		params["reply_to"] = replyTo
	}
	if len(mentions) > 0 {
		params["mentions"] = mentions
	}
	res, err := teamRPC(studioSocketPath(), jwt, "teams.send", params)
	if err != nil {
		return "", "", err
	}
	if res["status"] == "error" {
		msg, _ := res["message"].(string)
		return "", "", fmt.Errorf("%s", msg)
	}
	msgID, _ := res["msg_id"].(string)
	return msgID, to, nil
}

// teamInbox proxies teams.inbox — the non-draining message list for one
// member (messages stay until the kernel-side consumer claims them).
func (s *Server) teamInbox(team, member string) ([]map[string]any, error) {
	if member == "" {
		return nil, fmt.Errorf("team inbox needs a member name")
	}
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		return nil, err
	}
	res, err := teamRPC(studioSocketPath(), jwt, "teams.inbox", map[string]any{"team": team, "member": member})
	if err != nil {
		return nil, err
	}
	if res["status"] == "error" {
		msg, _ := res["message"].(string)
		return nil, fmt.Errorf("%s", msg)
	}
	raw, ok := res["messages"].([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if mm, ok := m.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out, nil
}
