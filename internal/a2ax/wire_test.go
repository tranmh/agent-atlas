package a2ax

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/events"
)

func TestExtractTaskInfo(t *testing.T) {
	info := ExtractTaskInfo([]byte(`{"statusUpdate":{"taskId":"t1","contextId":"c1","status":{"state":"TASK_STATE_WORKING","message":{"parts":[{"text":"busy"}]}}}}`))
	require.Equal(t, TaskInfo{Kind: "statusUpdate", TaskID: "t1", ContextID: "c1", State: "working", Text: "busy"}, info)
	info = ExtractTaskInfo([]byte(`{"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_INPUT_REQUIRED"}}}`))
	require.Equal(t, "input-required", info.State)
	info = ExtractTaskInfo([]byte(`{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"}}`))
	require.Equal(t, "task", info.Kind)
	require.Equal(t, "completed", info.State)
	info = ExtractTaskInfo([]byte(`{"artifactUpdate":{"taskId":"t1","artifact":{"name":"a"}}}`))
	require.Equal(t, "a", info.Artifact)
	require.Equal(t, TaskInfo{}, ExtractTaskInfo([]byte(`nope`)))
}

func TestTrackerNeverMovesBackwards(t *testing.T) {
	tr := NewTracker()
	_, ch := tr.Update("t", "submitted")
	require.True(t, ch)
	_, ch = tr.Update("t", "working")
	require.True(t, ch)
	_, ch = tr.Update("t", "submitted") // a late response
	require.False(t, ch)
	_, ch = tr.Update("t", "input-required")
	require.True(t, ch)
	_, ch = tr.Update("t", "working")
	require.True(t, ch)
	_, ch = tr.Update("t", "completed")
	require.True(t, ch)
	_, ch = tr.Update("t", "working")
	require.False(t, ch)
}

func TestTransportSetsRunHeaders(t *testing.T) {
	var got http.Header
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"task":{"id":"t","contextId":"c","status":{"state":"TASK_STATE_COMPLETED"}}}}`))}, nil
	})
	rec := &events.Recorder{}
	tr := &Transport{Base: base, Emit: rec, Tracker: NewTracker(), Call: Call{RunID: "r1", Caller: "sc-orchestrator", Agent: "sc-x", Chaos: true, TraceID: "0123456789abcdef0123456789abcdef"}}
	req, _ := http.NewRequest(http.MethodPost, "http://gw/agents/sc-x/invoke", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{}}`))
	_, err := tr.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, "r1", got.Get("X-Run-Id"))
	require.Equal(t, "sc-orchestrator", got.Get("X-Caller"))
	require.Equal(t, "1", got.Get("X-Chaos"))
	require.Regexp(t, `^00-0123456789abcdef0123456789abcdef-[0-9a-f]{16}-01$`, got.Get("Traceparent"))
	require.Equal(t, []events.Kind{events.KindRPCRequest, events.KindRPCResponse, events.KindTaskState}, rec.Kinds())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
