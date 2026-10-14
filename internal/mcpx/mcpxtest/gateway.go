// Package mcpxtest is a stand-in for agentgateway's MCP route in tests: one
// MCP server with the scenario's two servers multiplexed the way the gateway
// does it (prefixed tool and prompt names, "<target>+" resource URIs, write
// tools hidden and refused with HTTP 400 "Unknown tool", foreign callers 403).
package mcpxtest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/mcpserver"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

// NewGateway serves /mcp/<code> for the scenario. Server-side events go to emit.
func NewGateway(t *testing.T, sc *scenario.Scenario, emit events.Emitter) *httptest.Server {
	t.Helper()
	combined := scenario.MCPServer{Place: scenario.Place{ID: sc.Code + "-mcp", Name: sc.Title + " MCP route", City: "test", Country: "XX", Lat: 1, Lon: 1}}
	for _, m := range sc.MCPServers {
		for _, tl := range m.Tools {
			if tl.Write {
				continue
			}
			tl.Name = m.Target() + "_" + tl.Name
			combined.Tools = append(combined.Tools, tl)
		}
		for _, r := range m.Resources {
			r.URI = m.Target() + "+" + r.URI
			combined.Resources = append(combined.Resources, r)
		}
		for _, p := range m.Prompts {
			p.Name = m.Target() + "_" + p.Name
			combined.Prompts = append(combined.Prompts, p)
		}
	}
	inner := mcpserver.NewHandler(mcpserver.Config{Scenario: sc, Server: &combined, Emit: emit})
	prefix := "/mcp/" + sc.Code
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("X-Caller"), sc.Code+"-") {
			http.Error(w, "authorization failed", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte(`"method":"tools/call"`)) && bytes.Contains(body, []byte(scenario.WriteToolPrefix)) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Unknown tool: write tool"}}`))
				return
			}
		}
		r.URL.Path = mcpserver.MCPPath
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}
