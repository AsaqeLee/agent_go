package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Client is one MCP server connection (stdio JSON-RPC).
type Client struct {
	Name string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu      sync.Mutex
	w       io.Writer
	nextID  atomic.Int64
	pending map[int]chan rpcResponse
	closed  atomic.Bool
}

// Dial starts cfg.Command and performs MCP initialize.
func Dial(ctx context.Context, cfg ServerConfig) (*Client, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: empty command")
	}
	if cfg.Name == "" {
		cfg.Name = "mcp"
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}
	c := newClient(cfg.Name, stdin, stdout)
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	go copyLog(cfg.Name, stderr)
	if err := c.Initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func newClient(name string, w io.Writer, r io.Reader) *Client {
	c := &Client{
		Name:    name,
		w:       w,
		pending: map[int]chan rpcResponse{},
	}
	go c.readLoop(r)
	return c
}

func copyLog(name string, r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fmt.Fprintf(os.Stderr, "[mcp:%s] %s\n", name, sc.Text())
	}
}

func (c *Client) readLoop(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		raw, err := readMsg(br)
		if err != nil {
			c.failAll(err)
			return
		}
		if len(raw) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		if resp.Method != "" && resp.ID == 0 && resp.Result == nil {
			// notification from server — ignore
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcResponse{ID: id, Error: &rpcError{Message: err.Error()}}
		delete(c.pending, id)
	}
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp: client closed")
	}
	id := int(c.nextID.Add(1))
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	err := writeMsg(c.w, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	c.mu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeMsg(c.w, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

// Initialize performs the MCP handshake.
func (c *Client) Initialize(ctx context.Context) error {
	raw, err := c.call(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ImplInfo{Name: "agent_go", Version: "0.2.0"},
	})
	if err != nil {
		return err
	}
	var res initializeResult
	_ = json.Unmarshal(raw, &res)
	return c.notify("notifications/initialized", map[string]any{})
}

// ListTools returns the server's tools.
func (c *Client) ListTools(ctx context.Context) ([]toolSchema, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res toolListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// CallTool invokes a server tool by its unprefixed name.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	raw, err := c.call(ctx, "tools/call", callParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	var res callResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	var b []byte
	for _, p := range res.Content {
		if p.Type == "text" || p.Type == "" {
			b = append(b, p.Text...)
		}
	}
	out := string(b)
	if res.IsError {
		return "", fmt.Errorf("%s", out)
	}
	if out == "" {
		out = string(raw)
	}
	return out, nil
}

// Close stops the reader and the child process if any.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}
