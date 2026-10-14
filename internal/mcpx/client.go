// Package mcpx is the agents' MCP client. A session talks to the scenario's
// MCP route on agentgateway (/mcp/<code>), which multiplexes the scenario's
// two MCP servers. An http.RoundTripper adds the run headers and reports the
// wire traffic: JSON-RPC requests, SSE frames (progress notifications and
// results) and refusals by the gateway's MCP policy.
package mcpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/wire"
)

// Headers sent on every MCP request (the same ones the A2A calls carry; the
// gateway logs them and the MCP servers report them).
const (
	HeaderSpeed  = "X-Atlas-Speed"
	headerCaller = "X-Caller"
	headerRunID  = "X-Run-Id"
)

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Call identifies who opens a session and for which run.
type Call struct {
	Scenario    *scenario.Scenario
	Caller      string // agent id
	RunID       string
	Step        string
	TaskID      string
	Speed       float64
	Traceparent string
}

// Client opens MCP sessions through the gateway.
type Client struct {
	GatewayURL   string // e.g. http://agentgateway:3000
	Emit         events.Emitter
	PayloadLimit int
	Base         http.RoundTripper // tests
}

// ErrDenied is returned when the gateway refused a call (MCP or HTTP policy).
var ErrDenied = errors.New("refused by the gateway policy")

// Progress is one progress notification of a slow tool.
type Progress struct {
	Progress, Total float64
	Message         string
}

// Session is one MCP session for one agent execution.
type Session struct {
	cs       *mcp.ClientSession
	tr       *transport
	call     Call
	progress chan Progress
}

// Open initializes a session (server/discover, initialize, initialized).
func (c *Client) Open(ctx context.Context, call Call) (*Session, error) {
	if call.Speed <= 0 {
		call.Speed = 1
	}
	tr := &transport{base: c.Base, emit: c.Emit, limit: c.PayloadLimit, call: call}
	if tr.base == nil {
		tr.base = http.DefaultTransport
	}
	s := &Session{tr: tr, call: call, progress: make(chan Progress, 64)}
	client := mcp.NewClient(&mcp.Implementation{Name: call.Caller, Version: "1.0.0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			select {
			case s.progress <- Progress{Progress: req.Params.Progress, Total: req.Params.Total, Message: req.Params.Message}:
			default:
			}
		},
	})
	endpoint := strings.TrimRight(c.GatewayURL, "/") + "/mcp/" + call.Scenario.Code
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: tr},
		DisableStandaloneSSE: true, // progress arrives on each request's own stream
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		if isDenied(err) {
			return nil, fmt.Errorf("%w: %v", ErrDenied, err)
		}
		return nil, err
	}
	s.cs = cs
	return s, nil
}

// Close ends the session (DELETE).
func (s *Session) Close() error { return s.cs.Close() }

// Progress delivers the progress notifications of running tool calls.
func (s *Session) Progress() <-chan Progress { return s.progress }

// Tools lists the tools the gateway lets this caller see (prefixed names).
func (s *Session) Tools(ctx context.Context) ([]*mcp.Tool, error) {
	res, err := s.cs.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// ToolName is the multiplexed name of a server's tool on the scenario route.
func ToolName(server *scenario.MCPServer, tool string) string { return server.Target() + "_" + tool }

// ResourceURI is the multiplexed URI of a server's resource.
func ResourceURI(server *scenario.MCPServer, uri string) string { return server.Target() + "+" + uri }

// CallTool calls a tool by its multiplexed name. A refusal by the gateway
// returns ErrDenied.
func (s *Session) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	p := &mcp.CallToolParams{Name: name, Arguments: args}
	p.SetProgressToken(newID())
	res, err := s.cs.CallTool(ctx, p)
	if err != nil && isDenied(err) {
		return nil, fmt.Errorf("%w: %v", ErrDenied, err)
	}
	return res, err
}

// ReadResource reads a resource by its multiplexed URI and returns its text.
func (s *Session) ReadResource(ctx context.Context, uri string) (string, error) {
	res, err := s.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Contents {
		b.WriteString(c.Text)
	}
	return b.String(), nil
}

// GetPrompt returns the text of a prompt by its multiplexed name.
func (s *Session) GetPrompt(ctx context.Context, name string) (string, error) {
	res, err := s.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range res.Messages {
		if t, ok := m.Content.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), nil
}

// ResultText joins the text content of a tool result.
func ResultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// ---- wire capture ----

type transport struct {
	base  http.RoundTripper
	emit  events.Emitter
	limit int
	call  Call
}

// isDenied classifies the error of one call: the gateway refuses a write tool
// with HTTP 400 "Unknown tool" (the MCP policy hides it) and a foreign caller
// with 403. The SDK keeps both in the error it returns for that very request,
// so no state is shared between requests (calls and the SDK's own
// notifications run on different goroutines).
func isDenied(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Unknown tool") || strings.Contains(msg, http.StatusText(http.StatusForbidden))
}

type rpc struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name     string  `json:"name"`
		URI      string  `json:"uri"`
		Progress float64 `json:"progress"`
		Total    float64 `json:"total"`
		Message  string  `json:"message"`
	} `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// handshake methods are reported but not drawn as tool traffic.
var handshake = map[string]bool{"server/discover": true, "initialize": true, "notifications/initialized": true}

// target maps a multiplexed name or URI to the MCP server id it belongs to.
func (t *transport) target(name string) string {
	for _, m := range t.call.Scenario.MCPServers {
		if strings.HasPrefix(name, m.Target()+"_") || strings.HasPrefix(name, m.Target()+"+") {
			return m.ID
		}
	}
	return "agentgateway"
}

func (t *transport) event(kind events.Kind, from, to string) events.Event {
	return events.Event{
		Source: events.SourceAgent, Kind: kind, RunID: t.call.RunID, Scenario: t.call.Scenario.Code, Step: t.call.Step,
		From: from, To: to, Via: events.ViaGateway, TaskID: t.call.TaskID, Route: t.call.Scenario.Code + "-mcp",
	}
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(headerCaller, t.call.Caller)
	req.Header.Set(headerRunID, t.call.RunID)
	req.Header.Set(HeaderSpeed, strconv.FormatFloat(t.call.Speed, 'f', -1, 64))
	if t.call.Traceparent != "" {
		req.Header.Set("Traceparent", t.call.Traceparent)
	}
	if req.Method != http.MethodPost {
		return t.base.RoundTrip(req) // GET (standalone stream) and DELETE (session end) are not reported
	}
	body := wire.BufferRequest(req)
	var r rpc
	_ = json.Unmarshal(body, &r)
	subject := r.Params.Name + r.Params.URI
	to := "agentgateway"
	if subject != "" {
		to = t.target(subject)
	}
	corr := newID()
	e := t.event(events.KindMCPRequest, t.call.Caller, to)
	e.Method, e.Corr = r.Method, corr
	e.Note = fmt.Sprintf("%s → MCP %s %s", t.call.Caller, r.Method, subject)
	if handshake[r.Method] {
		e.Note = fmt.Sprintf("%s → MCP %s (session handshake with the gateway)", t.call.Caller, r.Method)
	}
	t.emit.Emit(*e.WithPayload(body, t.limit))

	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden || (resp.StatusCode == http.StatusBadRequest && r.Method == "tools/call") {
		raw, _ := wire.BufferResponse(resp, 64<<10)
		var er rpc
		_ = json.Unmarshal(raw, &er)
		if resp.StatusCode == http.StatusForbidden || (er.Error != nil && strings.Contains(er.Error.Message, "Unknown tool")) {
			d := t.event(events.KindMCPDenied, "agentgateway", t.call.Caller)
			d.Method, d.Corr, d.HTTPStatus, d.DurationMS = r.Method, corr, resp.StatusCode, time.Since(start).Milliseconds()
			if resp.StatusCode == http.StatusForbidden {
				d.Note = fmt.Sprintf("agentgateway refused %s's MCP %s (403): this route only serves the scenario's own agents", t.call.Caller, r.Method)
			} else {
				d.Note = fmt.Sprintf("agentgateway's MCP policy refused %s: write tools are hidden from tools/list and never reach the server", r.Params.Name)
			}
			var parsed any
			if json.Unmarshal(raw, &parsed) != nil {
				parsed = string(raw)
			}
			t.emit.Emit(*d.WithValue(map[string]any{"status": resp.StatusCode, "body": parsed}, t.limit))
			return resp, nil
		}
	}
	if wire.IsSSE(resp) {
		resp.Body = wire.NewSSETee(resp.Body, func(raw []byte) {
			var f rpc
			_ = json.Unmarshal(raw, &f)
			if f.Method == "notifications/progress" {
				p := t.event(events.KindMCPProgress, to, t.call.Caller)
				p.Method, p.Corr, p.DurationMS = f.Method, corr, time.Since(start).Milliseconds()
				p.Note = fmt.Sprintf("Progress %g/%g from %s: %s", f.Params.Progress, f.Params.Total, to, f.Params.Message)
				t.emit.Emit(*p.WithPayload(raw, t.limit))
				return
			}
			t.response(to, r, f, raw, corr, resp.StatusCode, start)
		}, nil)
		return resp, nil
	}
	if resp.StatusCode == http.StatusAccepted || resp.ContentLength == 0 {
		return resp, nil // notifications get 202 without a body
	}
	raw, _ := wire.BufferResponse(resp, 4<<20)
	var f rpc
	_ = json.Unmarshal(raw, &f)
	t.response(to, r, f, raw, corr, resp.StatusCode, start)
	return resp, nil
}

func (t *transport) response(to string, req, f rpc, raw []byte, corr string, status int, start time.Time) {
	e := t.event(events.KindMCPResponse, to, t.call.Caller)
	e.Method, e.Corr, e.HTTPStatus, e.DurationMS = req.Method, corr, status, time.Since(start).Milliseconds()
	subject := req.Params.Name + req.Params.URI
	switch {
	case f.Error != nil:
		e.Note = fmt.Sprintf("MCP %s %s failed: %s", req.Method, subject, f.Error.Message)
	case req.Method == "tools/list":
		var l struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(f.Result, &l)
		names := make([]string, 0, len(l.Tools))
		for _, tl := range l.Tools {
			names = append(names, tl.Name)
		}
		e.Note = fmt.Sprintf("The gateway lists %d tools from both servers: %s", len(names), strings.Join(names, ", "))
	case handshake[req.Method]:
		e.Note = fmt.Sprintf("MCP %s answered by the gateway", req.Method)
	default:
		e.Note = fmt.Sprintf("%s answered MCP %s %s", to, req.Method, subject)
	}
	t.emit.Emit(*e.WithPayload(raw, t.limit))
}
