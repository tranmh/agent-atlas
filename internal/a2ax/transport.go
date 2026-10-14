package a2ax

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/script"
)

// Call describes who talks to whom; it stamps every reported event.
type Call struct {
	RunID    string
	Scenario string
	Step     string
	Caller   string // orchestrator id, also sent as X-Caller
	Agent    string // target agent id
	Chaos    bool   // send X-Chaos: 1
	TraceID  string // 32 hex chars; every request carries a W3C traceparent in this trace
}

// NewTraceID returns a random W3C trace id (32 hex chars).
func NewTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// traceparent builds a W3C traceparent header with a fresh parent span id.
func traceparent(traceID string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "00-" + traceID + "-" + hex.EncodeToString(b) + "-01"
}

// Transport reports wire traffic of one orchestrator step.
type Transport struct {
	Base         http.RoundTripper
	Emit         events.Emitter
	Call         Call
	Tracker      *Tracker
	PayloadLimit int

	lastStatus atomic.Int64
}

// LastStatus is the HTTP status of the most recent response (0 if none).
func (t *Transport) LastStatus() int { return int(t.lastStatus.Load()) }

// NewID returns a short random id (correlation ids, push tokens).
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) event(kind events.Kind, from, to string) events.Event {
	return events.Event{
		Source: events.SourceOrchestrator, Kind: kind, RunID: t.Call.RunID, Scenario: t.Call.Scenario,
		Step: t.Call.Step, From: from, To: to, Via: events.ViaGateway,
	}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(script.HeaderRunID, t.Call.RunID)
	req.Header.Set(script.HeaderCaller, t.Call.Caller)
	if t.Call.Chaos {
		req.Header.Set(script.HeaderChaos, "1")
	}
	if len(t.Call.TraceID) == 32 {
		req.Header.Set("Traceparent", traceparent(t.Call.TraceID))
	}
	corr := NewID()
	if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "agent-card.json") {
		return t.cardFetch(req, corr)
	}

	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	var env rpcEnvelope
	_ = json.Unmarshal(body, &env)
	var params struct {
		Message struct {
			TaskID string `json:"taskId"`
		} `json:"message"`
		ID string `json:"id"`
	}
	_ = json.Unmarshal(env.Params, &params)
	taskID := params.Message.TaskID
	if taskID == "" {
		taskID = params.ID
	}

	e := t.event(events.KindRPCRequest, t.Call.Caller, t.Call.Agent)
	e.Method, e.TaskID, e.Corr = env.Method, taskID, corr
	e.Note = fmt.Sprintf("%s → %s: %s", t.Call.Caller, t.Call.Agent, env.Method)
	t.Emit.Emit(*e.WithPayload(body, t.PayloadLimit))

	start := time.Now()
	resp, err := t.base().RoundTrip(req)
	if err != nil {
		e := t.event(events.KindRPCResponse, t.Call.Agent, t.Call.Caller)
		e.Method, e.Corr, e.DurationMS, e.Note = env.Method, corr, time.Since(start).Milliseconds(), "transport error: "+err.Error()
		t.Emit.Emit(e)
		return nil, err
	}
	t.lastStatus.Store(int64(resp.StatusCode))

	if resp.StatusCode == http.StatusForbidden {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		e := t.event(events.KindPolicyDenied, "agentgateway", t.Call.Caller)
		e.Method, e.Corr, e.TaskID, e.HTTPStatus, e.DurationMS = env.Method, corr, taskID, resp.StatusCode, time.Since(start).Milliseconds()
		e.Route = t.Call.Agent
		e.Note = fmt.Sprintf("agentgateway refused %s → %s (403): authorization policy", env.Method, t.Call.Agent)
		t.Emit.Emit(*e.WithValue(map[string]any{"status": resp.StatusCode, "headers": pickHeaders(resp.Header), "body": string(raw)}, t.PayloadLimit))
		return resp, nil
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &sseTee{rc: resp.Body, t: t, corr: corr, method: env.Method, start: start, status: resp.StatusCode}
		return resp, nil
	}

	raw, rerr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	e = t.event(events.KindRPCResponse, t.Call.Agent, t.Call.Caller)
	e.Method, e.Corr, e.HTTPStatus, e.DurationMS = env.Method, corr, resp.StatusCode, time.Since(start).Milliseconds()
	var renv rpcEnvelope
	_ = json.Unmarshal(raw, &renv)
	info := ExtractTaskInfo(renv.Result)
	e.TaskID, e.ContextID, e.StateTo = info.TaskID, info.ContextID, info.State
	switch {
	case rerr != nil:
		e.Note = "read error: " + rerr.Error()
	case renv.Error != nil:
		e.Note = fmt.Sprintf("%s ← %s: JSON-RPC error %d %s", t.Call.Caller, t.Call.Agent, renv.Error.Code, renv.Error.Message)
	case resp.StatusCode >= 300:
		e.Note = fmt.Sprintf("%s ← %s: HTTP %d", t.Call.Caller, t.Call.Agent, resp.StatusCode)
	default:
		e.Note = describe(t.Call.Agent, t.Call.Caller, env.Method, info)
	}
	t.Emit.Emit(*e.WithPayload(raw, t.PayloadLimit))
	t.trackState(info, corr)
	return resp, nil
}

func (t *Transport) cardFetch(req *http.Request, corr string) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base().RoundTrip(req)
	e := t.event(events.KindCardFetch, t.Call.Caller, t.Call.Agent)
	e.Method, e.Corr = "GET agent-card.json", corr
	if err != nil {
		e.Note = "card fetch failed: " + err.Error()
		t.Emit.Emit(e)
		return nil, err
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	t.lastStatus.Store(int64(resp.StatusCode))
	e.HTTPStatus, e.DurationMS = resp.StatusCode, time.Since(start).Milliseconds()
	var card struct {
		Name                string `json:"name"`
		SupportedInterfaces []struct {
			URL string `json:"url"`
		} `json:"supportedInterfaces"`
	}
	_ = json.Unmarshal(raw, &card)
	url := ""
	if len(card.SupportedInterfaces) > 0 {
		url = card.SupportedInterfaces[0].URL
	}
	e.Note = fmt.Sprintf("%s read the agent card of %s. The gateway rewrote its URL to %s", t.Call.Caller, card.Name, url)
	var parsed any
	if json.Unmarshal(raw, &parsed) != nil {
		parsed = string(raw)
	}
	t.Emit.Emit(*e.WithValue(map[string]any{"request": map[string]string{"method": "GET", "url": req.URL.String()}, "status": resp.StatusCode, "card": parsed}, t.PayloadLimit))
	return resp, nil
}

// TrackPush reports a push notification body and any state change it reveals.
func (t *Transport) trackState(info TaskInfo, corr string) {
	if t.Tracker == nil || info.State == "" {
		return
	}
	if prev, changed := t.Tracker.Update(info.TaskID, info.State); changed {
		e := t.event(events.KindTaskState, t.Call.Agent, t.Call.Caller)
		e.TaskID, e.ContextID, e.StateFrom, e.StateTo, e.Corr = info.TaskID, info.ContextID, prev, info.State, corr
		e.Note = fmt.Sprintf("Task at %s: %s → %s", t.Call.Agent, orNone(prev), info.State)
		if info.Text != "" {
			e.Note += ": " + info.Text
		}
		t.Emit.Emit(e)
	}
}

// ReportPush is used by the orchestrator webhook: it emits push_received and
// a task_state event if the notification changed the task state.
func (t *Transport) ReportPush(body []byte) TaskInfo {
	info := ExtractTaskInfo(body)
	e := t.event(events.KindPushReceived, t.Call.Agent, t.Call.Caller)
	e.Via = events.ViaDirect
	e.TaskID, e.ContextID, e.StateTo = info.TaskID, info.ContextID, info.State
	e.Note = "Push notification from " + t.Call.Agent + " (webhook, not via the gateway): " + describeInfo(info)
	t.Emit.Emit(*e.WithPayload(body, t.PayloadLimit))
	t.trackState(info, "")
	return info
}

func describe(from, to, method string, info TaskInfo) string {
	return fmt.Sprintf("%s ← %s: %s result, %s", to, from, method, describeInfo(info))
}

func describeInfo(info TaskInfo) string {
	switch info.Kind {
	case "task", "statusUpdate":
		s := "task " + info.State
		if info.Text != "" {
			s += ": " + info.Text
		}
		return s
	case "artifactUpdate":
		return "artifact " + info.Artifact
	case "message":
		return "message"
	}
	return "no task data"
}

func orNone(s string) string {
	if s == "" {
		return "(new)"
	}
	return s
}

func pickHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}

// sseTee reports every SSE frame of a streaming response while the a2a client
// reads it, and the end of the stream when the body is exhausted or closed.
type sseTee struct {
	rc     io.ReadCloser
	t      *Transport
	corr   string
	method string
	start  time.Time
	status int

	mu     sync.Mutex
	buf    bytes.Buffer
	frames int
	done   bool
}

func (s *sseTee) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	if n > 0 {
		s.mu.Lock()
		s.buf.Write(p[:n])
		s.drain()
		s.mu.Unlock()
	}
	if err == io.EOF {
		s.finish("")
	} else if err != nil {
		s.finish(err.Error())
	}
	return n, err
}

func (s *sseTee) Close() error {
	s.finish("")
	return s.rc.Close()
}

// drain emits complete frames (terminated by a blank line) from the buffer.
func (s *sseTee) drain() {
	for {
		data := s.buf.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			return
		}
		frame := string(data[:idx])
		s.buf.Next(idx + 2)
		s.emitFrame(frame)
	}
}

func (s *sseTee) emitFrame(frame string) {
	var payload strings.Builder
	sc := bufio.NewScanner(strings.NewReader(frame))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			payload.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if payload.Len() == 0 {
		return
	}
	s.frames++
	raw := []byte(payload.String())
	var env rpcEnvelope
	_ = json.Unmarshal(raw, &env)
	info := ExtractTaskInfo(env.Result)
	t := s.t
	e := t.event(events.KindStreamEvent, t.Call.Agent, t.Call.Caller)
	e.Method, e.Corr, e.TaskID, e.ContextID, e.StateTo = s.method, s.corr, info.TaskID, info.ContextID, info.State
	e.DurationMS = time.Since(s.start).Milliseconds()
	e.Note = fmt.Sprintf("SSE frame %d from %s: %s", s.frames, t.Call.Agent, describeInfo(info))
	t.Emit.Emit(*e.WithPayload(raw, t.PayloadLimit))
	t.trackState(info, s.corr)
}

func (s *sseTee) finish(errText string) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	if s.buf.Len() > 0 {
		s.buf.WriteString("\n\n")
		s.drain()
	}
	frames := s.frames
	s.mu.Unlock()
	t := s.t
	e := t.event(events.KindRPCResponse, t.Call.Agent, t.Call.Caller)
	e.Method, e.Corr, e.HTTPStatus, e.DurationMS = s.method, s.corr, s.status, time.Since(s.start).Milliseconds()
	e.Note = fmt.Sprintf("%s ← %s: stream closed after %d frames", t.Call.Caller, t.Call.Agent, frames)
	if errText != "" {
		e.Note += " (" + errText + ")"
	}
	t.Emit.Emit(e)
}
