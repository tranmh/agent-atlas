// Package visualizer is the event hub behind the browser UI: it receives
// events from agents and orchestrators, decodes the gateway's OTLP access
// logs, stores everything per session (JSONL), streams it over SSE, starts
// runs on the orchestrators and holds breakpoint gates.
package visualizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tranmh/agent-atlas/internal/eventlog"
	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

// Config configures the visualizer.
type Config struct {
	Store        *eventlog.Store
	Catalog      *scenario.Catalog
	Log          *slog.Logger
	PayloadLimit int
	// OrchestratorURL maps an orchestrator id to its base URL.
	OrchestratorURL func(id string) string
	HTTP            *http.Client
	Heartbeat       time.Duration
	RunStagger      time.Duration // pause between orchestrators when starting "all"
}

// Server is the visualizer HTTP API.
type Server struct {
	cfg Config

	mu      sync.Mutex
	gates   map[string]*gate
	runs    map[string]*RunInfo
	counter int
}

// RunInfo is one run, as reported by GET /api/runs.
type RunInfo struct {
	RunID    string    `json:"run_id"`
	Scenario string    `json:"scenario"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Note     string    `json:"note,omitempty"`
}

type gate struct {
	ID       string    `json:"id"`
	RunID    string    `json:"run_id"`
	Scenario string    `json:"scenario"`
	Step     string    `json:"step"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Note     string    `json:"note"`
	Since    time.Time `json:"since"`
	release  chan string
}

// New returns the server.
func New(cfg Config) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 15 * time.Second
	}
	if cfg.OrchestratorURL == nil {
		cfg.OrchestratorURL = func(id string) string { return "http://" + id + ":8090" }
	}
	return &Server{cfg: cfg, gates: map[string]*gate{}, runs: map[string]*RunInfo{}}
}

// Handler returns the router.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(httpx.CORS("*"))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "session": s.cfg.Store.Session(), "events": s.cfg.Store.Len()})
	})
	r.Post("/api/events", s.postEvents)
	r.Get("/api/events/stream", s.stream)
	r.Get("/api/events", s.listEvents)
	r.Get("/api/sessions", s.sessions)
	r.Get("/api/sessions/{id}/events", s.sessionEvents)
	r.Get("/api/scenarios", s.scenarios)
	r.Get("/api/runs", s.listRuns)
	r.Post("/api/runs", s.startRuns)
	r.Post("/api/gate/wait", s.gateWait)
	r.Get("/api/gates", s.listGates)
	r.Post("/api/gate/release", s.gateRelease)
	r.Post("/otlp/v1/logs", s.otlpLogs)
	return r
}

// Append stores events and updates the run registry.
func (s *Server) Append(batch []events.Event) error {
	out, err := s.cfg.Store.Append(batch)
	s.mu.Lock()
	for _, e := range out {
		switch e.Kind {
		case events.KindRunStarted:
			s.runs[e.RunID] = &RunInfo{RunID: e.RunID, Scenario: e.Scenario, Started: e.TS}
		case events.KindRunFinished:
			if ri := s.runs[e.RunID]; ri != nil {
				ri.Finished, ri.Note = e.TS, e.Note
			}
		}
	}
	s.mu.Unlock()
	return err
}

func (s *Server) postEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_body", err.Error(), nil)
		return
	}
	var batch []events.Event
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '{' {
		var one events.Event
		err = json.Unmarshal(t, &one)
		batch = []events.Event{one}
	} else {
		err = json.Unmarshal(body, &batch)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_json", err.Error(), nil)
		return
	}
	if err := s.Append(batch); err != nil {
		s.cfg.Log.Error("append events", "err", err)
	}
	w.WriteHeader(http.StatusAccepted)
}

// stream serves the current session over SSE. The first frame is a "hello"
// with the session id. Event ids are "<session>/<n>"; a client reconnecting
// with Last-Event-ID of the same session gets only what it missed, a client
// from an older session gets the whole current session.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	// Subscribe before reading the backlog so nothing slips through the gap.
	ch, cancel := s.cfg.Store.Broadcaster().Subscribe()
	defer cancel()
	sse, err := httpx.NewSSEWriter(w)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "no_flush", err.Error(), nil)
		return
	}
	session := s.cfg.Store.Session()
	var after int64
	last := r.Header.Get("Last-Event-ID")
	if last == "" {
		last = r.URL.Query().Get("last_event_id")
	}
	if sess, n, ok := strings.Cut(last, "/"); ok && sess == session {
		after, _ = strconv.ParseInt(n, 10, 64)
	}
	_ = sse.Retry(3000)
	_ = sse.Event("", "hello", map[string]any{"session": session, "events": s.cfg.Store.Len(), "resumed_after": after})
	lastSent := after
	for _, e := range s.cfg.Store.After(after) {
		if err := sse.Event(fmt.Sprintf("%s/%d", session, e.ID), "atlas", e); err != nil {
			return
		}
		lastSent = e.ID
	}
	hb := time.NewTicker(s.cfg.Heartbeat)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			if sse.Heartbeat() != nil {
				return
			}
		case e, ok := <-ch:
			if !ok {
				return // evicted (slow client) or shutting down; the client reconnects
			}
			if e.ID <= lastSent {
				continue
			}
			if err := sse.Event(fmt.Sprintf("%s/%d", session, e.ID), "atlas", e); err != nil {
				return
			}
			lastSent = e.ID
		}
	}
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	evs := s.cfg.Store.After(after)
	if run := r.URL.Query().Get("run"); run != "" {
		filtered := evs[:0:0]
		for _, e := range evs {
			if e.RunID == run {
				filtered = append(filtered, e)
			}
		}
		evs = filtered
	}
	if evs == nil {
		evs = []events.Event{}
	}
	httpx.WriteJSON(w, http.StatusOK, evs)
}

func (s *Server) sessions(w http.ResponseWriter, _ *http.Request) {
	list, err := s.cfg.Store.Sessions()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "sessions", err.Error(), nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	evs, err := s.cfg.Store.ReadSession(id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown session", nil)
		return
	}
	if evs == nil {
		evs = []events.Event{}
	}
	if r.URL.Query().Get("format") == "jsonl" {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.jsonl"`)
		enc := json.NewEncoder(w)
		for _, e := range evs {
			_ = enc.Encode(e)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, evs)
}

func (s *Server) scenarios(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.cfg.Catalog.Scenarios)
}

func (s *Server) listRuns(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]RunInfo, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, *r)
	}
	s.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, out)
}

// RunRequest starts one or all scenarios.
type RunRequest struct {
	Scenario    string  `json:"scenario"` // a code, an id, or "all"
	Speed       float64 `json:"speed"`
	Chaos       bool    `json:"chaos"`
	Breakpoints bool    `json:"breakpoints"`
}

type orchRun struct {
	RunID       string  `json:"run_id"`
	Speed       float64 `json:"speed"`
	Chaos       bool    `json:"chaos"`
	Breakpoints bool    `json:"breakpoints"`
}

func (s *Server) startRuns(w http.ResponseWriter, r *http.Request) {
	var req RunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_json", err.Error(), nil)
		return
	}
	var targets []*scenario.Scenario
	if req.Scenario == "all" {
		targets = s.cfg.Catalog.Scenarios
	} else if sc := s.cfg.Catalog.ByCode(req.Scenario); sc != nil {
		targets = []*scenario.Scenario{sc}
	} else {
		httpx.WriteError(w, http.StatusBadRequest, "unknown_scenario", "scenario must be a code, an id or all", httpx.Field("scenario"))
		return
	}
	if req.Speed <= 0 {
		req.Speed = 1
	}
	type started struct {
		RunID    string `json:"run_id"`
		Scenario string `json:"scenario"`
	}
	var out []started
	stamp := time.Now().UTC().Format("150405")
	s.mu.Lock()
	s.counter++
	n := s.counter
	s.mu.Unlock()
	plans := make([]orchRun, len(targets))
	for i, sc := range targets {
		plans[i] = orchRun{RunID: fmt.Sprintf("%s-%s-%d", sc.Code, stamp, n), Speed: req.Speed, Chaos: req.Chaos, Breakpoints: req.Breakpoints}
		out = append(out, started{RunID: plans[i].RunID, Scenario: sc.Code})
	}
	// The first orchestrator is started synchronously so errors reach the caller.
	if err := s.startOne(r.Context(), targets[0], plans[0]); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "orchestrator", err.Error(), nil)
		return
	}
	go func() {
		for i := 1; i < len(targets); i++ {
			time.Sleep(s.cfg.RunStagger)
			if err := s.startOne(context.Background(), targets[i], plans[i]); err != nil {
				s.cfg.Log.Warn("start run", "scenario", targets[i].Code, "err", err)
			}
		}
	}()
	httpx.WriteJSON(w, http.StatusAccepted, out)
}

func (s *Server) startOne(ctx context.Context, sc *scenario.Scenario, run orchRun) error {
	body, _ := json.Marshal(run)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.OrchestratorURL(sc.Orchestrator.ID)+"/run", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", sc.Orchestrator.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: HTTP %d: %s", sc.Orchestrator.ID, resp.StatusCode, b)
	}
	return nil
}

func (s *Server) gateWait(w http.ResponseWriter, r *http.Request) {
	var g gate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&g); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_json", err.Error(), nil)
		return
	}
	s.mu.Lock()
	s.counter++
	g.ID = fmt.Sprintf("g%d", s.counter)
	g.Since = time.Now().UTC()
	g.release = make(chan string, 1)
	s.gates[g.ID] = &g
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.gates, g.ID)
		s.mu.Unlock()
	}()
	_ = s.Append([]events.Event{{Source: events.SourceVisualizer, Kind: events.KindBreakpoint, RunID: g.RunID, Scenario: g.Scenario,
		Step: g.Step, From: g.From, To: g.To, Corr: g.ID, Note: "Breakpoint before " + g.Step + ": " + g.Note}})
	var action string
	select {
	case action = <-g.release:
	case <-r.Context().Done():
		action = "continue"
	}
	_ = s.Append([]events.Event{{Source: events.SourceVisualizer, Kind: events.KindGateReleased, RunID: g.RunID, Scenario: g.Scenario,
		Step: g.Step, From: g.From, To: g.To, Corr: g.ID, Note: "Released (" + action + ")"}})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"action": action})
}

func (s *Server) listGates(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]gate, 0, len(s.gates))
	for _, g := range s.gates {
		out = append(out, *g)
	}
	s.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, out)
}

// GateRelease releases one gate (id), all gates of a run (run_id), or all gates (all).
type GateRelease struct {
	ID     string `json:"id"`
	RunID  string `json:"run_id"`
	All    bool   `json:"all"`
	Action string `json:"action"` // continue (default) or abort
}

func (s *Server) gateRelease(w http.ResponseWriter, r *http.Request) {
	var req GateRelease
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_json", err.Error(), nil)
		return
	}
	if req.Action != "abort" {
		req.Action = "continue"
	}
	released := 0
	s.mu.Lock()
	for _, g := range s.gates {
		if req.All || g.ID == req.ID || (req.RunID != "" && g.RunID == req.RunID) {
			select {
			case g.release <- req.Action:
				released++
			default:
			}
		}
	}
	s.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"released": released})
}

func (s *Server) otlpLogs(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	records, err := DecodeOTLPLogs(body, r.Header.Get("Content-Type"))
	if err != nil {
		s.cfg.Log.Warn("otlp decode", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var batch []events.Event
	for _, attrs := range records {
		if e, ok := AccessEvent(attrs, s.cfg.PayloadLimit); ok {
			batch = append(batch, e)
		}
	}
	if len(batch) > 0 {
		_ = s.Append(batch)
	}
	// An empty ExportLogsServiceResponse.
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}
