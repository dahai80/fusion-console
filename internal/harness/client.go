// Package harness runs the fusion-harness sidecar (Python, stdio JSON-RPC)
// and exposes its chat-pipeline guards: compaction before each LLM call and
// the per-conversation token budget funnel. The harness implementations run
// as-is — no Go re-implementation. Wire protocol: sidecar protocol v1
// (fusion-harness issue #110) — hello handshake with the protocol version,
// typed error objects, fail-closed semantics. When no usable Python is found
// the client disables itself; callers must treat ErrDisabled as a denial
// (fail-closed), never as a silent pass.
package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// protocolVersion pins sidecar protocol v1 (issue #110). A mismatch is a
// hard error: the client disables itself instead of guessing a dialect.
const protocolVersion = 1

// Message mirrors one chat message for the sidecar wire format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// rpcError is the v1 typed error object.
type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Client talks to one harnessd process. Requests are serialized (chat turns
// are per-conversation anyway); a broken pipe triggers a respawn on next use.
type Client struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	nextID   int
	helloed  bool
	python   string
	sidecar  string
	disabled bool
}

func New() *Client {
	c := &Client{nextID: 1}
	c.python, c.sidecar = resolvePaths()
	if c.python == "" {
		c.disabled = true
		log.Printf("harness: no python with fusion_harness found — callers fail closed (deny) on guard checks")
	}
	return c
}

// resolvePaths finds a python that can import fusion_harness and the sidecar
// script next to the console executable (or the repo layout during dev).
func resolvePaths() (python, sidecar string) {
	sidecar = os.Getenv("FUSION_CONSOLE_HARNESS_SIDECAR")
	if sidecar == "" {
		if exe, err := os.Executable(); err == nil {
			sidecar = filepath.Join(filepath.Dir(exe), "sidecar", "harnessd.py")
		}
		if _, err := os.Stat(sidecar); err != nil {
			if wd, err2 := os.Getwd(); err2 == nil {
				alt := filepath.Join(wd, "sidecar", "harnessd.py")
				if _, err3 := os.Stat(alt); err3 == nil {
					sidecar = alt
				}
			}
		}
	}
	if _, err := os.Stat(sidecar); err != nil {
		return "", ""
	}

	candidates := []string{os.Getenv("FUSION_CONSOLE_HARNESS_PYTHON")}
	if exe, err := os.Executable(); err == nil {
		// Monorepo layout: fusion-console sits next to the shared .venv.
		candidates = append(candidates,
			filepath.Join(filepath.Dir(filepath.Dir(exe)), ".venv", "bin", "python"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(wd), ".venv", "bin", "python"))
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "python3", "python")
	}
	for _, py := range candidates {
		if py == "" {
			continue
			// Probe: sidecar imports fusion_harness at startup; a python
			// without the package exits immediately with a traceback.
		}
		cmd := exec.Command(py, sidecar)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			continue
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			continue
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			continue
		}
		ok := probe(stdin, bufio.NewReader(stdout))
		if ok {
			// The probe instance is only a capability check — kill it so it
			// does not leak; spawn() starts the long-lived instance.
			stdin.Close()
			cmd.Process.Kill()
			cmd.Wait()
			log.Printf("harness: sidecar started (python %s, sidecar %s)", py, sidecar)
			return py, sidecar
		}
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}
	return "", ""
}

// probe performs the v1 hello handshake and accepts the reply only when the
// protocol version matches exactly (5s budget).
func probe(stdin io.WriteCloser, out *bufio.Reader) bool {
	res, err := helloOnce(stdin, out)
	if err != nil {
		log.Printf("harness: hello probe failed: %v", err)
		return false
	}
	log.Printf("harness: sidecar protocol v%d (kernel %s, commit %s)",
		res.ProtocolVersion, res.KernelVersion, res.Commit)
	return res.ProtocolVersion == protocolVersion
}

type helloResult struct {
	ProtocolVersion int      `json:"protocol_version"`
	KernelVersion   string   `json:"kernel_version"`
	Commit          string   `json:"commit"`
	Methods         []string `json:"methods"`
	Features        []string `json:"features"`
}

// helloOnce sends the hello handshake and decodes the reply.
func helloOnce(stdin io.WriteCloser, out *bufio.Reader) (*helloResult, error) {
	req, _ := json.Marshal(map[string]any{"id": 1, "method": "hello",
		"params": map[string]any{"protocol_version": protocolVersion}})
	if _, err := fmt.Fprintf(stdin, "%s\n", req); err != nil {
		return nil, err
	}
	type env struct {
		ID     int         `json:"id"`
		Error  *rpcError   `json:"error"`
		Result helloResult `json:"result"`
	}
	lineCh := make(chan string, 1)
	go func() {
		line, err := out.ReadString('\n')
		if err == nil {
			lineCh <- line
		}
		close(lineCh)
	}()
	select {
	case line, open := <-lineCh:
		if !open {
			return nil, fmt.Errorf("sidecar closed during hello")
		}
		var e env
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("bad hello reply: %w", err)
		}
		if e.Error != nil {
			return nil, fmt.Errorf("%s: %s", e.Error.Code, e.Error.Message)
		}
		return &e.Result, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("hello timed out")
	}
}

// call performs one RPC. On transport failure it respawns once and retries.
// A protocol-level error disables the client (version skew cannot heal by
// respawn); other errors surface as typed errors to the caller.
func (c *Client) call(method string, params map[string]any, out any) error {
	if c.disabled {
		return ErrDisabled
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if c.cmd == nil || c.stdin == nil {
			if err := c.spawn(); err != nil {
				return err
			}
		}
		if !c.helloed {
			res, err := helloOnce(c.stdin, c.stdout)
			if err != nil {
				log.Printf("harness: handshake failed, disabling: %v", err)
				c.disableLocked()
				return fmt.Errorf("harness: handshake failed: %w", err)
			}
			if res.ProtocolVersion != protocolVersion {
				log.Printf("harness: protocol mismatch sidecar=v%d client=v%d, disabling",
					res.ProtocolVersion, protocolVersion)
				c.disableLocked()
				return fmt.Errorf("harness: protocol version mismatch: sidecar v%d, client v%d",
					res.ProtocolVersion, protocolVersion)
			}
			c.helloed = true
		}
		c.nextID++
		id := c.nextID
		req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if _, err := fmt.Fprintf(c.stdin, "%s\n", req); err != nil {
			c.kill()
			continue
		}
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			c.kill()
			continue
		}
		var env struct {
			ID     int             `json:"id"`
			Error  *rpcError       `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			return fmt.Errorf("harness: bad reply: %w", err)
		}
		if env.Error != nil {
			if env.Error.Code == "protocol" {
				log.Printf("harness: protocol error from sidecar (%s), disabling: %s",
					env.Error.Code, env.Error.Message)
				c.disableLocked()
			}
			return &RPCError{Code: env.Error.Code, Message: env.Error.Message}
		}
		if out != nil {
			return json.Unmarshal(env.Result, out)
		}
		return nil
	}
	return fmt.Errorf("harness: sidecar unreachable after respawn")
}

// RPCError is a typed sidecar error (code in protocol|state|budget|
// permission|internal) so callers can fail closed with the right severity.
type RPCError struct {
	Code    string
	Message string
}

func (e *RPCError) Error() string { return "harness: " + e.Code + ": " + e.Message }

// disableLocked marks the client permanently unusable (protocol skew).
func (c *Client) disableLocked() {
	c.kill()
	c.disabled = true
}

func (c *Client) spawn() error {
	cmd := exec.Command(c.python, c.sidecar)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("harness: spawn: %w", err)
	}
	c.cmd, c.stdin, c.stdout = cmd, stdin, bufio.NewReader(stdout)
	c.helloed = false
	log.Printf("harness: sidecar (re)started")
	return nil
}

func (c *Client) kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.stdin.Close()
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	c.cmd, c.stdin, c.stdout = nil, nil, nil
	c.helloed = false
}

// ErrDisabled means the sidecar is not available. Callers must FAIL CLOSED:
// deny the guarded action, never proceed unguarded.
var ErrDisabled = fmt.Errorf("harness: disabled")

// PreLLMResult carries the pre_llm gate decision plus the conv budget state
// (spent/limit tokens) so the UI can render a live budget meter (issue #66).
type PreLLMResult struct {
	Proceed  bool
	Reason   string
	Messages []Message
	Spent    int
	Limit    int
}

// PreLLM gates one LLM call: budget check + compaction (harness impl).
func (c *Client) PreLLM(convID int64, budgetTokens int, model string, msgs []Message) (PreLLMResult, error) {
	var res struct {
		Proceed     bool      `json:"proceed"`
		Reason      string    `json:"reason"`
		Messages    []Message `json:"messages"`
		SpentTokens int       `json:"spent_tokens"`
		LimitTokens int       `json:"limit_tokens"`
	}
	params := map[string]any{
		"conv_id": convID, "budget_tokens": budgetTokens, "model": model,
		"node_label": fmt.Sprintf("conv:%d", convID), "messages": msgs,
	}
	if err := c.call("pre_llm", params, &res); err != nil {
		return PreLLMResult{Proceed: false, Reason: err.Error(), Messages: msgs}, err
	}
	return PreLLMResult{
		Proceed:  res.Proceed,
		Reason:   res.Reason,
		Messages: res.Messages,
		Spent:    res.SpentTokens,
		Limit:    res.LimitTokens,
	}, nil
}

// PostLLM records one completed call's token usage into the conv budget and
// reports the updated spend + exceeded flag for the budget meter (issue #66).
func (c *Client) PostLLM(convID int64, budgetTokens, tokens int) (spent int, exceeded bool, err error) {
	var res struct {
		SpentTokens    int  `json:"spent_tokens"`
		BudgetExceeded bool `json:"budget_exceeded"`
	}
	params := map[string]any{
		"conv_id":       convID,
		"budget_tokens": budgetTokens,
		"usage":         map[string]any{"prompt_tokens": 0, "completion_tokens": tokens},
	}
	if err = c.call("post_llm", params, &res); err != nil {
		return 0, false, err
	}
	return res.SpentTokens, res.BudgetExceeded, nil
}

// Admit is the tool_admit verdict from the harness kernel (issue #66):
// needs_approval means the kernel wants a human decision (routed through the
// console's approval flow) — not a plain deny.
type Admit struct {
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason"`
	BlockedBy     string `json:"blocked_by"`
	NeedsApproval bool   `json:"needs_approval"`
	ApprovalID    string `json:"approval_id"`
}

// ToolAdmit runs the tool admission chain for orchestrator dispatch.
func (c *Client) ToolAdmit(convID int64, budgetTokens int, tool string, args map[string]any) (Admit, error) {
	var res Admit
	params := map[string]any{"conv_id": convID, "budget_tokens": budgetTokens, "tool_name": tool, "args": args}
	if err := c.call("tool_admit", params, &res); err != nil {
		return Admit{Allowed: false, Reason: err.Error()}, err
	}
	return res, nil
}

// Resolve closes one approval round-trip: approve=false denies; approve=true
// grants the tool for the session and re-runs the full admission chain
// (issue #110). Call it when the browser approval flow yields a decision.
func (c *Client) Resolve(convID int64, approvalID string, approve bool) (Admit, error) {
	var res Admit
	var raw struct {
		Admit
		GrantedTool string `json:"granted_tool"`
	}
	params := map[string]any{"approval_id": approvalID, "approve": approve}
	if err := c.call("resolve", params, &raw); err != nil {
		return Admit{Allowed: false, Reason: err.Error()}, err
	}
	res = raw.Admit
	return res, nil
}

// Close stops the sidecar.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kill()
}
