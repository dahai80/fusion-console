// Team view client (T4b, issue #6): a pure-observer poller over the
// agent-studio daemon's WebSocket JSON-RPC endpoint. The console stores no
// team state — every field in the snapshot comes from the daemon, refreshed
// on a fixed interval and cached in memory for the /api/team/view handler.
package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
	"github.com/gorilla/websocket"
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

// teamWSToken resolves the daemon's WS shared secret: env first (same
// FUSION_WS_TOKEN the daemon reads), then keychain under a fixed ref.
func teamWSToken() (string, error) {
	if v := os.Getenv("FUSION_WS_TOKEN"); v != "" {
		return v, nil
	}
	return config.GetAPIKey("agent-studio-ws")
}

// teamRPC opens a WS connection, returns one RPC result, closes it. Per-call
// connections keep the client stateless — the poll is 4 requests every 5s,
// cheap enough at team-observability scale.
func teamRPC(wsURL, wsToken, jwt, method string, params map[string]any) (map[string]any, error) {
	hdr := http.Header{}
	if wsToken != "" {
		hdr.Set("Sec-WebSocket-Protocol", "Bearer "+wsToken)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %v", wsURL, err)
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if params == nil {
		params = map[string]any{}
	}
	if jwt != "" {
		params["_auth"] = jwt
	}
	reqID := time.Now().UnixNano()
	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": reqID, "method": method, "params": params,
	}); err != nil {
		return nil, fmt.Errorf("write %s: %v", method, err)
	}
	var resp struct {
		ID     any            `json:"id"`
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		return nil, fmt.Errorf("read %s: %v", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: rpc error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// teamEnsureJWT exchanges the configured apikey for a daemon JWT (cached).
func (s *Server) teamEnsureJWT(wsToken string) (string, error) {
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
	res, err := teamRPC(s.cfg.AgentStudio.WSURL, wsToken, "", "auth.login", map[string]any{"apikey": apikey})
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
	go func() {
		log.Printf("server: team observer started (ws=%s team=%s)", s.cfg.AgentStudio.WSURL, s.cfg.AgentStudio.Team)
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
	wsToken, err := teamWSToken()
	if err != nil || wsToken == "" {
		snap.Error = "no WS token (set FUSION_WS_TOKEN or keychain ref agent-studio-ws)"
		return snap
	}
	jwt, err := s.teamEnsureJWT(wsToken)
	if err != nil {
		snap.Error = err.Error()
		return snap
	}
	team := s.cfg.AgentStudio.Team

	if res, err := teamRPC(s.cfg.AgentStudio.WSURL, wsToken, jwt, "task.list", map[string]any{"team": team, "limit": 100}); err != nil {
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

	if res, err := teamRPC(s.cfg.AgentStudio.WSURL, wsToken, jwt, "team.roster_list", map[string]any{"team": team}); err == nil {
		if raw, ok := res["members"].([]any); ok {
			for _, m := range raw {
				if mm, ok := m.(map[string]any); ok {
					snap.Roster = append(snap.Roster, mm)
				}
			}
		}
	}
	if res, err := teamRPC(s.cfg.AgentStudio.WSURL, wsToken, jwt, "team.health", map[string]any{"team": team}); err == nil {
		snap.Health = res
	}
	if res, err := teamRPC(s.cfg.AgentStudio.WSURL, wsToken, jwt, "team.get_limits", map[string]any{}); err == nil {
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
