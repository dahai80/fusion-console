// mcp.go — MCP stdio brain server. When the brain is set to "claude code",
// the console spawns a claude CLI session with --mcp-config pointing back at
// this binary in "mcp" mode; the agent-brain gets the same dispatch tools the
// chat planner has (list_agents / run_agent / read_artifact / team tools).
// Protocol: JSON-RPC 2.0 over stdio, MCP 2024-11-05 shape.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/dahai80/fusion-console/internal/chat"
)

// ServeMCPBrain serves one MCP session on stdin/stdout. Blocks until stdin
// closes. Every tool call runs through the same executeToolCall path as the
// chat planner, so budget, guards and repo-binding rules apply identically.
func ServeMCPBrain(s *Server) {
	s.mcpMode = true
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	log.SetOutput(os.Stderr) // stdout is the RPC channel; logs go to stderr
	for {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fusion-console", "version": "1.0"},
			}
		case "notifications/initialized":
			continue
		case "tools/list":
			tools := []map[string]any{}
			for _, t := range orchestratorTools() {
				tools = append(tools, map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"inputSchema": json.RawMessage(t.Parameters),
				})
			}
			result = map[string]any{"tools": tools}
		case "tools/call":
			args, err := json.Marshal(req.Params.Arguments)
			call := chat.ToolCall{Name: req.Params.Name, Arguments: string(args)}
			if err != nil {
				result = mcpError(err.Error())
				break
			}
			out, err := s.executeToolCall(context.Background(), call, "agent-brain", 0, nil)
			if err != nil {
				log.Printf("mcp: tool %s failed: %v", req.Params.Name, err)
				result = mcpError(err.Error())
				break
			}
			result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": out}},
			}
		default:
			if len(req.ID) == 0 || string(req.ID) == "null" {
				continue
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found: " + req.Method},
			})
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			log.Printf("mcp: encode response: %v", err)
			return
		}
	}
}

func mcpError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("error: %s", msg)}},
		"isError": true,
	}
}
