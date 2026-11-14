// Team view client (T4b, issue #6): a pure-observer poller over the
// agent-studio daemon's UDS JSON-RPC socket (newline-delimited JSON-RPC 2.0,
// _auth JWT enforced). The console stores no team state — every field in the
// snapshot comes from the daemon, refreshed on a fixed interval and cached in
// memory for the /api/team/view handler.
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
)

// teamSnapshot is the last successful poll (or the connection failure).
type teamSnapshot struct {
	FetchedAt time.Time                 `json:"fetched_at"`
	Connected bool                      `json:"connected"`
	Error     string                    `json:"error,omitempty"`
	Tasks     []map[string]any          `json:"tasks"`
	Roster    []map[string]any          `json:"roster"`
	Health    map[string]any            `json:"health"`
	Limits    map[string]any            `json:"limits"`
	Brief     map[string][]briefWarning `json:"brief_warnings"` // task_id -> warnings
	Alerts    []string                  `json:"alerts"`
}

// teamDialer holds the mutable client state (JWT cache + snapshot).
type teamDialer struct {
	mu   sync.Mutex
	jwt  string
	snap *teamSnapshot
}

const teamPollInterval = 5 * time.Second

// studioSocketPath resolves the daemon's UDS socket: env override first, then
// the default /tmp/fusion-studio.sock written by agent-studio's start.sh.
func studioSocketPath() string {
	if p := os.Getenv("FUSION_STUDIO_SOCKET"); p != "" {
		return p
	}
	return "/tmp/fusion-studio.sock"
}

// teamRPC opens a UDS connection, returns one JSON-RPC result, closes it.
// Per-call connections keep the client stateless — the poll is a handful of
// requests every 5s, cheap at team-observability scale.
func teamRPC(socketPath, jwt, method string, params map[string]any) (map[string]any, error) {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %v", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if params == nil {
		params = map[string]any{}
	}
	if jwt != "" {
		// daemon expects _auth as {"jwt": <token>} (rpc_auth.authenticate_rpc)
		params["_auth"] = map[string]any{"jwt": jwt}
	}
	reqID := time.Now().UnixNano()
	req, err := jsonMarshal(map[string]any{
		"jsonrpc": "2.0", "id": reqID, "method": method, "params": params,
	})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %v", method, err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("write %s: %v", method, err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", method, err)
	}
	var resp struct {
		ID     any            `json:"id"`
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := jsonUnmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode %s: %v", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: rpc error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// teamEnsureJWT exchanges the configured apikey for a daemon JWT (cached).
func (s *Server) teamEnsureJWT() (string, error) {
	s.team.mu.Lock()
	jwt := s.team.jwt
	s.team.mu.Unlock()
	if jwt != "" {
		return jwt, nil
	}
	apikey, err := config.GetAPIKey(s.cfg.AgentStudio.APIKeyID)
	if err != nil || apikey == "" {
		return "", fmt.Errorf("no agent-studio apikey (set FUSION_CONSOLE_APIKEY_%s or store it in the keychain)", s.cfg.AgentStudio.APIKeyID)
	}
	res, err := teamRPC(studioSocketPath(), "", "auth.login", map[string]any{"apikey": apikey})
	if err != nil {
		return "", fmt.Errorf("auth.login: %v", err)
	}
	jwt, _ = res["jwt"].(string)
	if jwt == "" {
		return "", fmt.Errorf("auth.login returned no jwt")
	}
	s.team.mu.Lock()
	s.team.jwt = jwt
	s.team.mu.Unlock()
	return jwt, nil
}

// startTeamPoller launches the background observer loop when the
// agent-studio integration is enabled. Failure to poll never crashes the
// console — the snapshot carries the error for the UI to render.
func (s *Server) startTeamPoller() {
	if !s.cfg.AgentStudio.Enabled {
		return
	}
	log.Printf("server: team observer started (socket=%s team=%s)", studioSocketPath(), s.cfg.AgentStudio.Team)
	go func() {
		for {
			snap := s.pollTeam()
			s.team.mu.Lock()
			s.team.snap = snap
			s.team.mu.Unlock()
			time.Sleep(teamPollInterval)
		}
	}()
}

// pollTeam runs one full observation cycle: auth, then task board + roster +
// health + limits. Failed/illegal transitions and brief rejections land in
// Alerts so the UI can surface them instead of going silent.
func (s *Server) pollTeam() *teamSnapshot {
	snap := &teamSnapshot{
		FetchedAt: time.Now().UTC(),
		Tasks:     []map[string]any{},
		Roster:    []map[string]any{},
		Brief:     map[string][]briefWarning{},
		Alerts:    []string{},
	}
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		snap.Error = err.Error()
		return snap
	}
	team := s.cfg.AgentStudio.Team

	if res, err := teamRPC(studioSocketPath(), jwt, "task.list", map[string]any{"team": team, "limit": 100}); err != nil {
		snap.Error = err.Error()
	} else if raw, ok := res["tasks"].([]any); ok {
		for _, t := range raw {
			if m, ok := t.(map[string]any); ok {
				snap.Tasks = append(snap.Tasks, m)
			}
		}
	}
	if snap.Error != "" {
		return snap
	}
	snap.Connected = true

	if res, err := teamRPC(studioSocketPath(), jwt, "team.roster_list", map[string]any{"team": team}); err == nil {
		if raw, ok := res["agents"].([]any); ok {
			for _, m := range raw {
				if mm, ok := m.(map[string]any); ok {
					snap.Roster = append(snap.Roster, mm)
				}
			}
		}
	}
	if res, err := teamRPC(studioSocketPath(), jwt, "team.health", map[string]any{"team": team}); err == nil {
		snap.Health = res
	}
	if res, err := teamRPC(studioSocketPath(), jwt, "team.get_limits", map[string]any{}); err == nil {
		snap.Limits = res
	}

	// Brief quality panel: reuse the #4 checker on every task's description —
	// spawn briefs that fail the contract show up as warnings next to the task.
	// Failed tasks and brief warnings surface as alerts (never silent).
	now := time.Now().Unix()
	for _, t := range snap.Tasks {
		id, _ := t["task_id"].(string)
		if id == "" {
			id, _ = t["id"].(string)
		}
		desc, _ := t["description"].(string)
		if desc == "" {
			desc, _ = t["title"].(string)
		}
		if w := checkBrief(desc); len(w) > 0 {
			snap.Brief[id] = w
			snap.Alerts = append(snap.Alerts, fmt.Sprintf("brief quality on task %s: %d contract items missing", id, len(w)))
		}
		status, _ := t["status"].(string)
		if status == "failed" {
			updated, _ := t["updated_at"].(float64)
			errMsg, _ := t["error"].(string)
			if updated == 0 || now-int64(updated) <= 86400 {
				snap.Alerts = append(snap.Alerts, fmt.Sprintf("task %s failed: %s", id, errMsg))
			}
		}
	}
	return snap
}

// teamView returns the cached snapshot for the UI.
func (s *Server) teamView() *teamSnapshot {
	s.team.mu.Lock()
	defer s.team.mu.Unlock()
	if s.team.snap == nil {
		return &teamSnapshot{Tasks: []map[string]any{}, Roster: []map[string]any{}, Brief: map[string][]briefWarning{}, Alerts: []string{}}
	}
	return s.team.snap
}

// teamRegisterMember passes a team.roster_register call through to the
// daemon — the console-side "create team member" entry (user-facing team
// setup without leaving the console). Returns the daemon result verbatim.
func (s *Server) teamRegisterMember(agentID, name, team, status string, capabilities []string) (map[string]any, error) {
	jwt, err := s.teamEnsureJWT()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(team) == "" {
		team = s.cfg.AgentStudio.Team
	}
	return teamRPC(studioSocketPath(), jwt, "team.roster_register", map[string]any{
		"agent_id": agentID, "name": name, "team": team,
		"capabilities": capabilities, "status": status,
	})
}

// jsonMarshal / jsonUnmarshal thin wrappers keep encoding/json usage in one
// place for the UDS client.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(data string, v any) error { return json.Unmarshal([]byte(data), v) }
