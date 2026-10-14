package visualizer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/tranmh/agent-atlas/internal/eventlog"
	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

func newServer(t *testing.T, orch func(id string) string) (*Server, *httptest.Server) {
	t.Helper()
	store, err := eventlog.Open(t.TempDir(), nil)
	require.NoError(t, err)
	cat, err := scenario.LoadEmbedded()
	require.NoError(t, err)
	s := New(Config{Store: store, Catalog: cat, OrchestratorURL: orch, Heartbeat: time.Second, RunStagger: time.Millisecond})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); store.Close() })
	return s, srv
}

func post(t *testing.T, url string, v any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(v)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	return resp
}

// readFrames reads SSE frames (event name + data) until n "atlas" frames arrived.
func readFrames(t *testing.T, body io.Reader, n int) (hello map[string]any, ids []string, evs []events.Event) {
	t.Helper()
	sc := bufio.NewScanner(body)
	var name, id string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			data := []byte(strings.TrimPrefix(line, "data: "))
			if name == "hello" {
				require.NoError(t, json.Unmarshal(data, &hello))
			} else {
				var e events.Event
				require.NoError(t, json.Unmarshal(data, &e))
				evs = append(evs, e)
				ids = append(ids, id)
				if len(evs) == n {
					return
				}
			}
		}
	}
	return
}

func TestEventsStreamWithResume(t *testing.T) {
	s, srv := newServer(t, nil)
	resp := post(t, srv.URL+"/api/events", []events.Event{
		{Kind: events.KindRunStarted, Source: "orchestrator", RunID: "r1", Scenario: "sc"},
		{Kind: events.KindAgentWork, Source: "agent", RunID: "r1"},
	})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events/stream", nil)
	sresp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer sresp.Body.Close()
	require.Equal(t, "text/event-stream", sresp.Header.Get("Content-Type"))
	go func() {
		time.Sleep(100 * time.Millisecond)
		post(t, srv.URL+"/api/events", events.Event{Kind: events.KindRunFinished, Source: "orchestrator", RunID: "r1"})
	}()
	hello, ids, evs := readFrames(t, sresp.Body, 3)
	require.Equal(t, s.cfg.Store.Session(), hello["session"])
	require.Equal(t, []events.Kind{events.KindRunStarted, events.KindAgentWork, events.KindRunFinished}, []events.Kind{evs[0].Kind, evs[1].Kind, evs[2].Kind})
	require.Equal(t, s.cfg.Store.Session()+"/2", ids[1])

	// Resume after event 2 of this session: only event 3 is replayed.
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events/stream", nil)
	req2.Header.Set("Last-Event-ID", ids[1])
	r2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer r2.Body.Close()
	_, _, evs2 := readFrames(t, r2.Body, 1)
	require.Equal(t, int64(3), evs2[0].ID)

	// The run registry follows run_started / run_finished.
	var runs []RunInfo
	rr, _ := http.Get(srv.URL + "/api/runs")
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&runs))
	require.Len(t, runs, 1)
	require.False(t, runs[0].Finished.IsZero())
}

func TestStartAllRunsCallsEveryOrchestrator(t *testing.T) {
	var mu sync.Mutex
	got := map[string]orchRun{}
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var run orchRun
		require.NoError(t, json.NewDecoder(r.Body).Decode(&run))
		mu.Lock()
		got[r.Header.Get("X-Orch")+run.RunID] = run
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer orch.Close()
	_, srv := newServer(t, func(id string) string { return orch.URL })
	resp := post(t, srv.URL+"/api/runs", RunRequest{Scenario: "all", Speed: 2, Chaos: true})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var started []map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&started))
	require.Len(t, started, 5)
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 5 }, 2*time.Second, 10*time.Millisecond)
	for _, run := range got {
		require.Equal(t, 2.0, run.Speed)
		require.True(t, run.Chaos)
	}
	resp = post(t, srv.URL+"/api/runs", RunRequest{Scenario: "nope"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGateWaitsUntilReleased(t *testing.T) {
	s, srv := newServer(t, nil)
	done := make(chan string, 1)
	go func() {
		resp := post(t, srv.URL+"/api/gate/wait", map[string]string{"run_id": "r1", "scenario": "sc", "step": "diagnose", "to": "sc-diagnosis"})
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		done <- out["action"]
	}()
	require.Eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.gates) == 1 }, 2*time.Second, 10*time.Millisecond)
	select {
	case <-done:
		t.Fatal("gate released too early")
	case <-time.After(100 * time.Millisecond):
	}
	resp := post(t, srv.URL+"/api/gate/release", GateRelease{RunID: "r1", Action: "abort"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "abort", <-done)
	kinds := []events.Kind{}
	for _, e := range s.cfg.Store.After(0) {
		kinds = append(kinds, e.Kind)
	}
	require.Equal(t, []events.Kind{events.KindBreakpoint, events.KindGateReleased}, kinds)
}

func TestOTLPAccessLogBecomesGatewayEvent(t *testing.T) {
	s, srv := newServer(t, nil)
	kv := func(k, v string) *common.KeyValue {
		return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
	}
	req := &collogs.ExportLogsServiceRequest{ResourceLogs: []*logs.ResourceLogs{{ScopeLogs: []*logs.ScopeLogs{{
		Scope: &common.InstrumentationScope{Name: "agentgateway.access"},
		LogRecords: []*logs.LogRecord{{Attributes: []*common.KeyValue{
			kv("route", "default/sc-diagnosis"), kv("http.method", "POST"), kv("http.path", "/agents/sc-diagnosis/invoke"),
			{Key: "http.status", Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: 200}}},
			kv("a2a.method", "SendStreamingMessage"), kv("a2a.task.state", "TASK_STATE_COMPLETED"),
			kv("duration", "1004ms"), kv("run_id", "sc-1"), kv("caller", "sc-orchestrator"),
		}}},
	}}}}}
	b, err := proto.Marshal(req)
	require.NoError(t, err)
	resp, err := http.Post(srv.URL+"/otlp/v1/logs", "application/x-protobuf", bytes.NewReader(b))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	evs := s.cfg.Store.After(0)
	require.Len(t, evs, 1)
	e := evs[0]
	require.Equal(t, events.KindGatewayAccess, e.Kind)
	require.Equal(t, "sc-diagnosis", e.To)
	require.Equal(t, "sc-orchestrator", e.From)
	require.Equal(t, "sc", e.Scenario)
	require.Equal(t, "SendStreamingMessage", e.Method)
	require.Equal(t, "completed", e.StateTo)
	require.Equal(t, 200, e.HTTPStatus)
	require.Equal(t, int64(1004), e.DurationMS)
	require.Equal(t, "sc-1", e.RunID)
}

func TestOTLPMCPAccessLogNamesServerAndTool(t *testing.T) {
	s, srv := newServer(t, nil)
	kv := func(k, v string) *common.KeyValue {
		return &common.KeyValue{Key: k, Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: v}}}
	}
	status := func(n int64) *common.KeyValue {
		return &common.KeyValue{Key: "http.status", Value: &common.AnyValue{Value: &common.AnyValue_IntValue{IntValue: n}}}
	}
	req := &collogs.ExportLogsServiceRequest{ResourceLogs: []*logs.ResourceLogs{{ScopeLogs: []*logs.ScopeLogs{{LogRecords: []*logs.LogRecord{
		{Attributes: []*common.KeyValue{kv("route", "default/sc-mcp"), kv("http.method", "POST"), kv("http.path", "/mcp/sc"), status(200),
			kv("mcp.method.name", "tools/call"), kv("mcp.target", "erp"), kv("mcp_tool", "quote_freight"), kv("caller", "sc-logistics"), kv("duration", "6012ms")}},
		{Attributes: []*common.KeyValue{kv("route", "default/sc-mcp"), kv("http.method", "POST"), kv("http.path", "/mcp/sc"), status(400),
			kv("mcp.method.name", "tools/call"), kv("mcp.target", "erp"), kv("mcp_tool", "restricted_shutdown_turbine"), kv("reason", "MCP"), kv("caller", "sc-field-service")}},
		{Attributes: []*common.KeyValue{kv("route", "default/sc-mcp"), kv("http.method", "POST"), kv("http.path", "/mcp/sc"), status(200),
			kv("mcp.method.name", "initialize"), kv("caller", "sc-logistics")}},
	}}}}}}
	b, err := proto.Marshal(req)
	require.NoError(t, err)
	resp, err := http.Post(srv.URL+"/otlp/v1/logs", "application/x-protobuf", bytes.NewReader(b))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	evs := s.cfg.Store.After(0)
	require.Len(t, evs, 3)
	require.Equal(t, "sc-erp", evs[0].To)
	require.Equal(t, "MCP tools/call quote_freight", evs[0].Method)
	require.Equal(t, int64(6012), evs[0].DurationMS)
	require.Contains(t, evs[1].Note, "MCP policy refused restricted_shutdown_turbine")
	require.Equal(t, "agentgateway", evs[2].To)
	require.Equal(t, "sc", evs[2].Scenario)
}
