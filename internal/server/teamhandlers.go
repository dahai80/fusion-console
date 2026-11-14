// Team view HTTP surface (T4b): the snapshot read side plus the operations
// the daemon exposes today (task.cancel / task.rerun / team.set_limits).
// pause/resume/disband wait for upstream RPC methods — see issue #6.
package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// handleTeamView serves GET /api/team/view — the latest observer snapshot.
func (s *Server) handleTeamView(w http.ResponseWriter, r *http.Request) {
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
// daemon. Body: {"op": "cancel"|"rerun", "task_id": "..."} or
// {"op": "set_limits", "max_concurrent": N, "max_depth": N}.
func (s *Server) handleTeamOp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Op            string   `json:"op"`
		TaskID        string   `json:"task_id"`
		MaxConcurrent *int     `json:"max_concurrent"`
		MaxDepth      *int     `json:"max_depth"`
		AgentID       string   `json:"agent_id"`
		Name          string   `json:"name"`
		Team          string   `json:"team"`
		Status        string   `json:"status"`
		Capabilities  []string `json:"capabilities"`
	}
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
	var method string
	var params map[string]any
	switch in.Op {
	case "cancel":
		if in.TaskID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id required"})
			return
		}
		method, params = "task.cancel", map[string]any{"task_id": in.TaskID}
	case "rerun":
		if in.TaskID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id required"})
			return
		}
		method, params = "task.rerun", map[string]any{"task_id": in.TaskID}
	case "set_limits":
		p := map[string]any{}
		if in.MaxConcurrent != nil {
			p["max_concurrent"] = *in.MaxConcurrent
		}
		if in.MaxDepth != nil {
			p["max_depth"] = *in.MaxDepth
		}
		method, params = "team.set_limits", p
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "op must be cancel, rerun or set_limits"})
		return
	}
	res, err := teamRPC(studioSocketPath(), jwt, method, params)
	if err != nil {
		log.Printf("server: team op %s: %v", in.Op, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: team op %s executed (task=%s)", in.Op, in.TaskID)
	writeJSON(w, http.StatusOK, res)
}
