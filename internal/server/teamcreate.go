// Team creation proxy (issue #16): POST /api/team/create translates the
// wizard's payload into teams.register, then the queued teams.add_task calls,
// then an optional teams.lead_start — in that order, on the agent-studio
// daemon. The console stores no team state: whatever the daemon accepts is
// owned by the daemon. Partial success is a reportable state (the team may
// exist while a later task was rejected), so the response carries per-task
// errors instead of failing the whole request after register succeeded.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// teamCreateMember mirrors the kernel TeamMember field names exactly — the
// wizard's review step shows this payload verbatim. Context is the resident
// per-member brief (issue #41): the harness injects it as [member context]
// into every member turn, so it carries the worker's role description and
// key conventions instead of relying on the lead restating them each round.
type teamCreateMember struct {
	Name           string   `json:"name"`
	Role           string   `json:"role"`
	Skills         []string `json:"skills"`
	ToolAllowlist  []string `json:"tool_allowlist"`
	PermissionMode string   `json:"permission_mode"`
	BudgetTokens   int64    `json:"budget_tokens"`
	MaxConcurrency int64    `json:"max_concurrency"`
	Context        string   `json:"context"`
}

// defaultToolAllowlist is applied to members registered without an explicit
// allowlist (issue #41): the subagent tool registry is built only from
// spec.tools, so an empty allowlist means no tools at all — members would
// degrade to the injected [team spec] section with no way to read the full
// SPEC.md. file_read is the minimum useful set (read the SPEC on demand);
// callers can always pass a richer allowlist explicitly.
var defaultToolAllowlist = []string{"file_read"}

// teamCreateDefinition mirrors the kernel TeamDefinition field names.
type teamCreateDefinition struct {
	Name             string             `json:"name"`
	NamespaceKey     string             `json:"namespace_key"`
	Members          []teamCreateMember `json:"members"`
	LeadBudgetTokens int64              `json:"lead_budget_tokens"`
	Playbook         bool               `json:"playbook"`
	MaxDepth         int64              `json:"max_depth"`
	Metadata         map[string]any     `json:"metadata"`
}

// teamCreateTask is one queued initial task (teams.add_task after register).
type teamCreateTask struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// teamCreateReq is the POST /api/team/create body. Spec is the optional
// initial SPEC.md text (issue #41): passed through to teams.register, the
// daemon seeds <team state_dir>/SPEC.md with it (skeleton when empty).
type teamCreateReq struct {
	Team      teamCreateDefinition `json:"team"`
	Tasks     []teamCreateTask     `json:"tasks"`
	LeadStart bool                 `json:"lead_start"`
	LeadGoal  string               `json:"lead_goal"`
	Spec      string               `json:"spec"`
}

// teamTaskResult reports one add_task outcome — a rejected task is an entry
// with an error, never a dropped row (partial success stays visible).
type teamTaskResult struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	TaskID string `json:"task_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// teamCreateRes is the proxy response. TasksError is empty when register
// itself failed (no task was attempted).
type teamCreateRes struct {
	Status       string           `json:"status"`
	Team         string           `json:"team"`
	NamespaceKey string           `json:"namespace_key"`
	Tasks        []teamTaskResult `json:"tasks"`
	LeadStarted  bool             `json:"lead_started"`
	LeadError    string           `json:"lead_error,omitempty"`
}

// teamCreate handles POST /api/team/create. Register failure -> 502 with the
// kernel error verbatim (fail visibly, no silent retry). After a successful
// register, per-task and lead errors land in the 200 body.
func (s *Server) handleTeamCreate(w http.ResponseWriter, r *http.Request) {
	var in teamCreateReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	res, status, err := s.teamCreateCore(in)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// teamCreateCore is the create pipeline shared by the wizard's HTTP proxy and
// the chat-side create_team brain tool: teams.register, then the queued
// teams.add_task calls, then an optional teams.lead_start. The error return
// carries the HTTP status the handler should serve (400 bad request, 502
// daemon error); per-task and lead errors ride in the result body.
func (s *Server) teamCreateCore(in teamCreateReq) (*teamCreateRes, int, error) {
	in.Team.Name = strings.TrimSpace(in.Team.Name)
	if in.Team.Name == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("team name required")
	}
	if len(in.Team.Members) == 0 {
		return nil, http.StatusBadRequest, fmt.Errorf("team needs at least one member")
	}
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		log.Printf("server: team create auth: %v", err)
		return nil, http.StatusBadGateway, err
	}

	// Step 1: teams.register — the kernel validates the definition and owns
	// the error text; the console never weakens or rewords it. Spec rides the
	// register params (issue #41): the daemon seeds SPEC.md exactly once.
	def := in.Team
	if def.NamespaceKey == "" {
		def.NamespaceKey = def.Name
	}
	if def.Metadata == nil {
		def.Metadata = map[string]any{}
	}
	for i := range def.Members {
		if len(def.Members[i].ToolAllowlist) == 0 {
			def.Members[i].ToolAllowlist = defaultToolAllowlist
			log.Printf("server: team create member %s/%s: empty allowlist, defaulting to file_read", def.Name, def.Members[i].Name)
		}
	}
	regRes, err := teamRPC(studioSocketPath(), jwt, "teams.register", map[string]any{"definition": def, "spec": in.Spec})
	if err != nil {
		log.Printf("server: team create register %s: %v", def.Name, err)
		return nil, http.StatusBadGateway, err
	}
	if regRes["status"] == "error" {
		msg, _ := regRes["message"].(string)
		log.Printf("server: team create register %s rejected: %s", def.Name, msg)
		return nil, http.StatusBadGateway, fmt.Errorf("%s", msg)
	}
	nsKey, _ := regRes["namespace_key"].(string)
	log.Printf("server: team created name=%s members=%d namespace_key=%s", def.Name, len(def.Members), nsKey)

	// Step 2: queued tasks in order. A rejected task is recorded, not fatal —
	// the team exists and the wizard must show exactly which task failed.
	out := teamCreateRes{Status: "ok", Team: def.Name, NamespaceKey: nsKey, Tasks: []teamTaskResult{}}
	for i, t := range in.Tasks {
		res := teamTaskResult{Index: i, Title: t.Title}
		addRes, err := teamRPC(studioSocketPath(), jwt, "teams.add_task", map[string]any{
			"team": def.Name, "title": t.Title, "detail": t.Detail,
		})
		if err != nil {
			res.Error = err.Error()
		} else if addRes["status"] == "error" {
			msg, _ := addRes["message"].(string)
			res.Error = msg
		} else if id, ok := addRes["task_id"].(string); ok {
			res.TaskID = id
		}
		if res.Error != "" {
			log.Printf("server: team create add_task %s/%d rejected: %s", def.Name, i, res.Error)
		}
		out.Tasks = append(out.Tasks, res)
	}

	// Step 3: optional lead loop start (checkbox default OFF). Goal falls
	// back to the first task's title — the lead loop needs a goal string.
	if in.LeadStart {
		goal := strings.TrimSpace(in.LeadGoal)
		if goal == "" && len(in.Tasks) > 0 {
			goal = in.Tasks[0].Title
		}
		if goal == "" {
			out.LeadError = "lead_start requested without a goal"
		} else {
			leadRes, err := teamRPC(studioSocketPath(), jwt, "teams.lead_start", map[string]any{
				"team": def.Name, "goal": goal,
			})
			if err != nil {
				out.LeadError = err.Error()
			} else if leadRes["status"] == "error" {
				msg, _ := leadRes["message"].(string)
				out.LeadError = msg
			} else {
				out.LeadStarted = true
			}
			if out.LeadError != "" {
				log.Printf("server: team create lead_start %s rejected: %s", def.Name, out.LeadError)
			}
		}
	}
	return &out, http.StatusOK, nil
}

// teamListTeams proxies teams.list — the wizard's step-1 namespace warning
// needs the set of live team anchors (teams.list is the only read the daemon
// exposes for that; anchor collisions show up as live team names).
func (s *Server) teamListTeams() ([]map[string]any, error) {
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		return nil, err
	}
	res, err := teamRPC(studioSocketPath(), jwt, "teams.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	raw, ok := res["teams"].([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, t := range raw {
		if m, ok := t.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// handleTeamTeams serves GET /api/team/teams — the daemon's live teams.
func (s *Server) handleTeamTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := s.teamListTeams()
	if err != nil {
		log.Printf("server: team list: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": teams})
}
