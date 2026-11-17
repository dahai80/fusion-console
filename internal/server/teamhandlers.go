// Team view HTTP surface: the snapshot read side plus the operations the
// daemon exposes — task cancel/rerun, team.set_limits, and the lifecycle ops
// teams.pause / teams.resume / teams.disband plus the lead loop
// teams.lead_start / lead_stop / lead_status (issue #14; upstream RPCs landed
// in fusion-agent-studio). The playbook panel stays hidden until
// fusion-agent-studio#733 (teams.playbook_render/trace) ships.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// teamOpReq is the POST /api/team/op body.
type teamOpReq struct {
	Op             string   `json:"op"`
	TaskID         string   `json:"task_id"`
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
// {"op": "pause"|"resume"|"disband"|"lead_stop"|"lead_status", "team": "..."}
// or {"op": "lead_start", "team": "...", "goal": "..."}.
func (s *Server) handleTeamOp(w http.ResponseWriter, r *http.Request) {
	var in teamOpReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&in); err != nil {
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
