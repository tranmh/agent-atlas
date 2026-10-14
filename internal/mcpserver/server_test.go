package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

type hdr struct{ base http.RoundTripper }

func (h hdr) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Caller", "sc-logistics")
	r.Header.Set("X-Run-Id", "r1")
	r.Header.Set(HeaderSpeed, "100")
	return h.base.RoundTrip(r)
}

func connect(t *testing.T, id string, onProgress func(*mcp.ProgressNotificationParams)) (*mcp.ClientSession, *events.Recorder) {
	t.Helper()
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	sc, def := cat.FindMCPServer(id)
	require.NotNil(t, def)
	rec := &events.Recorder{}
	srv := httptest.NewServer(NewHandler(Config{Scenario: sc, Server: def, Emit: rec}))
	t.Cleanup(srv.Close)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) { onProgress(req.Params) },
	})
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + MCPPath, HTTPClient: &http.Client{Transport: hdr{http.DefaultTransport}}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs, rec
}

func TestToolsResourcesPromptsAndProgress(t *testing.T) {
	var mu sync.Mutex
	var progress []string
	cs, rec := connect(t, "sc-erp", func(p *mcp.ProgressNotificationParams) {
		mu.Lock()
		progress = append(progress, p.Message)
		mu.Unlock()
	})
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	names := []string{}
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	require.ElementsMatch(t, []string{"check_stock", "quote_freight", "list_technicians", "restricted_shutdown_turbine"}, names)

	p := &mcp.CallToolParams{Name: "quote_freight", Arguments: map[string]any{"part": "SKF 23148", "destination": "Foz do Iguaçu"}}
	p.SetProgressToken("tok")
	start := time.Now()
	res, err := cs.CallTool(ctx, p)
	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "speed header shortens the waits")
	require.Equal(t, "Best quote for SKF 23148 to Foz do Iguaçu: AirCargo Express, 3 days, 4 120 EUR.", res.Content[0].(*mcp.TextContent).Text)
	mu.Lock()
	require.Equal(t, []string{"Asking carrier 1 of 4 for a quote…", "Asking carrier 2 of 4 for a quote…", "Asking carrier 3 of 4 for a quote…", "Asking carrier 4 of 4 for a quote…"}, progress)
	mu.Unlock()

	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "check_stock", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, bad.IsError)

	var call events.Event
	for _, e := range rec.Events() {
		if e.Kind == events.KindMCPServerCall && e.Method == "tools/call" {
			call = e
			break
		}
	}
	require.Equal(t, "sc-logistics", call.From)
	require.Equal(t, "sc-erp", call.To)
	require.Equal(t, "r1", call.RunID)
}

func TestDataServerResourceAndPrompt(t *testing.T) {
	cs, _ := connect(t, "sc-historian", func(*mcp.ProgressNotificationParams) {})
	ctx := context.Background()
	r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "historian://sc/manuals/turbine-t3"})
	require.NoError(t, err)
	require.Contains(t, r.Contents[0].Text, "SKF 23148")
	g, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "diagnose"})
	require.NoError(t, err)
	require.Contains(t, g.Messages[0].Content.(*mcp.TextContent).Text, "read_trend")
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "read_trend", Arguments: map[string]any{"asset": "T3", "signal": "bearing_temp", "days": 14}})
	require.NoError(t, err)
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, "bearing_temp of T3 over the last 14 days")
	require.NotNil(t, res.StructuredContent)
}

func TestExpand(t *testing.T) {
	require.Equal(t, "a 1 {{c}}", Expand("{{a}} {{b}} {{c}}", map[string]string{"a": "a", "b": "1"}))
}
