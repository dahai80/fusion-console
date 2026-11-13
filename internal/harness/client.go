// Package harness runs the fusion-harness sidecar (Python, stdio JSON-RPC)
// and exposes its chat-pipeline guards: compaction before each LLM call and
// the per-conversation token budget funnel. The harness implementations run
// as-is — no Go re-implementation. When no usable Python is found the client
// disables itself and chat proceeds unguarded (logged, fail-visible state).
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

// Message mirrors one chat message for the sidecar wire format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client talks to one harnessd process. Requests are serialized (chat turns
// are per-conversation anyway); a broken pipe triggers a respawn on next use.
type Client struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	nextID   int
	python   string
	sidecar  string
	disabled bool
}

func New() *Client {
	c := &Client{nextID: 1}
	c.python, c.sidecar = resolvePaths()
	if c.python == "" {
		c.disabled = true
		log.Printf("harness: no python with fusion_harness found — chat runs without compaction/budget guard")
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
			log.Printf("harness: sidecar started (python %s, sidecar %s)", py, sidecar)
			return py, sidecar
		}
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}
	return "", ""
}

// probe sends ping and waits for the ok reply (5s budget).
func probe(stdin io.WriteCloser, out *bufio.Reader) bool {
	req, _ := json.Marshal(map[string]any{"id": 1, "method": "ping", "params": map[string]any{}})
	if _, err := fmt.Fprintf(stdin, "%s\n", req); err != nil {
		return false
	}
	type res struct {
		OK  bool   `json:"ok"`
		Err string `json:"error"`
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
			return false
		}
		var r res
		return json.Unmarshal([]byte(line), &r) == nil && r.OK && r.Err == ""
	case <-time.After(5 * time.Second):
		return false
	}
}

// call performs one RPC. On transport failure it respawns once and retries.
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
			Error  string          `json:"error"`
			Result json.RawMessage `json:"-"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			return fmt.Errorf("harness: bad reply: %w", err)
		}
		if env.Error != "" {
			return fmt.Errorf("harness: %s", env.Error)
		}
		if out != nil {
			return json.Unmarshal([]byte(line), out)
		}
		return nil
	}
	return fmt.Errorf("harness: sidecar unreachable after respawn")
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
}

// ErrDisabled means the sidecar is not available; callers proceed unguarded.
var ErrDisabled = fmt.Errorf("harness: disabled")

// PreLLM gates one LLM call: budget check + compaction (harness impl).
func (c *Client) PreLLM(convID int64, budgetTokens int, model string, msgs []Message) (proceed bool, reason string, out []Message, err error) {
	var res struct {
		Proceed  bool      `json:"proceed"`
		Reason   string    `json:"reason"`
		Messages []Message `json:"messages"`
	}
	params := map[string]any{"conv_id": convID, "budget_tokens": budgetTokens, "model": model, "messages": msgs}
	if err = c.call("pre_llm", params, &res); err != nil {
		return false, "", msgs, err
	}
	return res.Proceed, res.Reason, res.Messages, nil
}

// PostLLM records one completed call's token usage into the conv budget.
func (c *Client) PostLLM(convID int64, budgetTokens, tokens int) error {
	return c.call("post_llm", map[string]any{"conv_id": convID, "budget_tokens": budgetTokens, "tokens": tokens}, nil)
}

// ToolAdmit runs the tool admission chain for orchestrator dispatch.
func (c *Client) ToolAdmit(convID int64, budgetTokens int, tool string, args map[string]any) (allowed bool, reason string, err error) {
	var res struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err = c.call("tool_admit", map[string]any{"conv_id": convID, "budget_tokens": budgetTokens, "tool": tool, "args": args}, &res); err != nil {
		return false, "", err
	}
	return res.Allowed, res.Reason, nil
}

// Close stops the sidecar.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kill()
}
