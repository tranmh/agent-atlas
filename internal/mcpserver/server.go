// Package mcpserver serves one scenario MCP server (tools, resources,
// prompts) from its scenario definition, deterministically, over Streamable
// HTTP. Slow tools send progress notifications. Every tools/call,
// resources/read and prompts/get that arrives behind the gateway is reported.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/script"
)

// Paths served by every MCP server.
const (
	MCPPath    = "/mcp"
	HealthPath = "/healthz"
)

// HeaderSpeed carries the run's speed factor (divides progress waits).
const HeaderSpeed = "X-Atlas-Speed"

// Config configures one MCP server.
type Config struct {
	Scenario     *scenario.Scenario
	Server       *scenario.MCPServer
	Emit         events.Emitter
	PayloadLimit int
	Log          *slog.Logger
	// Sleep waits d or until ctx ends; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Build returns the MCP server with the definition's tools, resources and prompts.
func Build(cfg Config) *mcp.Server {
	def := cfg.Server
	s := mcp.NewServer(&mcp.Implementation{Name: def.ID, Title: def.Name, Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: def.Description + " (" + def.City + ", " + def.Country + ")",
	})
	for i := range def.Tools {
		t := &def.Tools[i]
		s.AddTool(&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: InputSchema(t)}, toolHandler(cfg, t))
	}
	for i := range def.Resources {
		r := def.Resources[i]
		s.AddResource(&mcp.Resource{URI: r.URI, Name: r.Name, MIMEType: r.MIME}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: r.MIME, Text: r.Text}}}, nil
		})
	}
	for i := range def.Prompts {
		p := def.Prompts[i]
		s.AddPrompt(&mcp.Prompt{Name: p.Name, Description: p.Description}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: p.Description, Messages: []*mcp.PromptMessage{{Role: "assistant", Content: &mcp.TextContent{Text: p.Text}}}}, nil
		})
	}
	return s
}

// InputSchema is the JSON schema of a tool's parameters (all required).
func InputSchema(t *scenario.MCPTool) map[string]any {
	props := map[string]any{}
	req := []string{}
	for _, p := range t.Params {
		prop := map[string]any{"type": p.Type}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		props[p.Name] = prop
		req = append(req, p.Name)
	}
	sort.Strings(req)
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func toolHandler(cfg Config, t *scenario.MCPTool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult("arguments must be a JSON object"), nil
			}
		}
		for _, p := range t.Params {
			if _, ok := args[p.Name]; !ok {
				return errorResult("missing parameter " + p.Name), nil
			}
		}
		vars := map[string]string{}
		for k, v := range args {
			vars[k] = fmt.Sprint(v)
		}
		if pr := t.Progress; pr != nil {
			speed := 1.0
			if req.Extra != nil {
				if f, err := strconv.ParseFloat(req.Extra.Header.Get(HeaderSpeed), 64); err == nil && f > 0 {
					speed = f
				}
			}
			tok := req.Params.GetProgressToken()
			for step := 1; step <= pr.Steps; step++ {
				vars["step"], vars["steps"] = strconv.Itoa(step), strconv.Itoa(pr.Steps)
				if tok != nil {
					_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
						ProgressToken: tok, Progress: float64(step), Total: float64(pr.Steps), Message: Expand(pr.Say, vars),
					})
				}
				if err := cfg.sleep(ctx, time.Duration(float64(pr.Every.Std())/speed)); err != nil {
					return nil, err
				}
			}
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: Expand(t.Result, vars)}}}
		if len(t.Data) > 0 {
			res.StructuredContent = t.Data
		}
		return res, nil
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// Expand replaces {{name}} placeholders; unknown ones stay as they are.
func Expand(tmpl string, vars map[string]string) string {
	pairs := make([]string, 0, 2*len(vars))
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

func (c Config) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NewHandler serves /mcp (Streamable HTTP, stateful sessions) and /healthz.
func NewHandler(cfg Config) http.Handler {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Emit == nil {
		cfg.Emit = &events.Recorder{}
	}
	srv := Build(cfg)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		SessionTimeout: 10 * time.Minute,
	})
	mux := http.NewServeMux()
	mux.Handle(MCPPath, capture(cfg, stream))
	mux.HandleFunc(HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "mcp_server": cfg.Server.ID})
	})
	return mux
}

// reported are the methods that do work; the handshake and list calls are
// visible on the client side.
var reported = map[string]string{"tools/call": "tool", "resources/read": "resource", "prompts/get": "prompt"}

// capture reports the work requests as they arrive behind the gateway.
func capture(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		var rpc struct {
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
				URI  string `json:"uri"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &rpc)
		if what, ok := reported[rpc.Method]; ok {
			target := rpc.Params.Name + rpc.Params.URI
			e := events.Event{
				Source: events.SourceAgent, Kind: events.KindMCPServerCall, Scenario: cfg.Scenario.Code,
				RunID: r.Header.Get(script.HeaderRunID), From: r.Header.Get(script.HeaderCaller), To: cfg.Server.ID,
				Via: events.ViaGateway, Method: rpc.Method,
				Note: fmt.Sprintf("%s received %s %s from %s", cfg.Server.Name, what, target, r.Header.Get(script.HeaderCaller)),
			}
			var parsed any
			if json.Unmarshal(body, &parsed) != nil {
				parsed = string(body)
			}
			headers := map[string]string{}
			for _, h := range []string{"X-Caller", "X-Run-Id", "Mcp-Session-Id", "Mcp-Protocol-Version", "Traceparent", HeaderSpeed} {
				if v := r.Header.Get(h); v != "" {
					headers[h] = v
				}
			}
			e.WithValue(map[string]any{"headers": headers, "body": parsed}, cfg.PayloadLimit)
			cfg.Emit.Emit(e)
		}
		next.ServeHTTP(w, r)
	})
}
