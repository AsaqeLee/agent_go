package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// ServerTool is a handler used by Serve (tests and examples).
type ServerTool struct {
	Name        string
	Description string
	Schema      map[string]any
	ReadOnly    bool
	Run         func(ctx context.Context, args map[string]any) (string, error)
}

// Serve speaks MCP over r/w until the client hangs up. Used by tests and examples/mcp-echo.
func Serve(ctx context.Context, r io.Reader, w io.Writer, info ImplInfo, tools []ServerTool) error {
	if info.Name == "" {
		info.Name = "agent_go_mcp"
	}
	if info.Version == "" {
		info.Version = "0.1.0"
	}
	byName := map[string]ServerTool{}
	var listed []toolSchema
	for _, t := range tools {
		byName[t.Name] = t
		sch := toolSchema{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		}
		if sch.InputSchema == nil {
			sch.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if t.ReadOnly {
			sch.Annotations = &toolAnnot{ReadOnlyHint: true}
		}
		listed = append(listed, sch)
	}

	br := bufio.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := readMsg(br)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(raw) == 0 {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id,omitempty"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if req.ID == 0 && strings.HasPrefix(req.Method, "notifications/") {
			continue
		}
		var result any
		var rpcErr *rpcError
		switch req.Method {
		case "initialize":
			result = initializeResult{
				ProtocolVersion: protocolVersion,
				Capabilities:    map[string]any{"tools": map[string]any{}},
				ServerInfo:      info,
			}
		case "tools/list":
			result = toolListResult{Tools: listed}
		case "ping":
			result = map[string]any{}
		case "tools/call":
			var p callParams
			_ = json.Unmarshal(req.Params, &p)
			t, ok := byName[p.Name]
			if !ok {
				rpcErr = &rpcError{Code: -32601, Message: "unknown tool " + p.Name}
				break
			}
			out, err := t.Run(ctx, p.Arguments)
			if err != nil {
				result = callResult{
					Content: []contentPart{{Type: "text", Text: err.Error()}},
					IsError: true,
				}
				break
			}
			result = callResult{Content: []contentPart{{Type: "text", Text: out}}}
		default:
			if req.ID == 0 {
				continue
			}
			rpcErr = &rpcError{Code: -32601, Message: "unknown method " + req.Method}
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			b, _ := json.Marshal(result)
			resp.Result = b
		}
		if err := writeMsg(w, resp); err != nil {
			return err
		}
	}
}
