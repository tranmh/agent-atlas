// Package orchestrator runs a scenario's plan: for each step it fetches the
// agent card through the gateway, creates an a2a-go client and delegates the
// work with SendMessage, SendStreamingMessage or a push-notification flow.
// All traffic goes through agentgateway; push notifications come back
// directly to the orchestrator's webhook.
package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"

	"github.com/tranmh/agent-atlas/internal/a2ax"
	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/script"
)

// Options configure one run.
type Options struct {
	RunID       string  `json:"run_id"`
	Speed       float64 `json:"speed"`       // divides all waits; default 1
	Chaos       bool    `json:"chaos"`       // first attempt to the streaming agent fails, the gateway retries
	Breakpoints bool    `json:"breakpoints"` // ask the visualizer before every step
	TraceID     string  `json:"trace_id"`    // one W3C trace for the whole run (Jaeger); generated if empty
}

// Config configures an orchestrator.
type Config struct {
	Scenario      *scenario.Scenario
	GatewayURL    string // e.g. http://agentgateway:3000
	PublicURL     string // where agents reach the push webhook, e.g. http://sc-orchestrator:8090
	VisualizerURL string // gate long-poll; empty disables breakpoints
	Emit          events.Emitter
	Log           *slog.Logger
	PayloadLimit  int
	HTTP          http.RoundTripper // base transport (tests)
	PushTimeout   time.Duration
}

// Orchestrator runs plans and receives push notifications.
type Orchestrator struct {
	cfg Config

	mu      sync.Mutex
	runs    map[string]*RunStatus
	waiters map[string]*pushWaiter
}

// RunStatus is reported by GET /runs.
type RunStatus struct {
	Options
	Scenario string    `json:"scenario"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Step     string    `json:"step,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type pushWaiter struct {
	transport *a2ax.Transport
	ch        chan a2ax.TaskInfo
}

// New returns an orchestrator.
func New(cfg Config) *Orchestrator {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Emit == nil {
		cfg.Emit = &events.Recorder{}
	}
	if cfg.PushTimeout == 0 {
		cfg.PushTimeout = 10 * time.Minute
	}
	return &Orchestrator{cfg: cfg, runs: map[string]*RunStatus{}, waiters: map[string]*pushWaiter{}}
}

// Start begins a run in the background.
func (o *Orchestrator) Start(opts Options) (*RunStatus, error) {
	if opts.RunID == "" {
		opts.RunID = o.cfg.Scenario.Code + "-" + a2ax.NewID()
	}
	if opts.Speed <= 0 {
		opts.Speed = 1
	}
	if len(opts.TraceID) != 32 {
		opts.TraceID = a2ax.NewTraceID()
	}
	o.mu.Lock()
	if _, dup := o.runs[opts.RunID]; dup {
		o.mu.Unlock()
		return nil, fmt.Errorf("run %s exists", opts.RunID)
	}
	st := &RunStatus{Options: opts, Scenario: o.cfg.Scenario.Code, Started: time.Now().UTC()}
	o.runs[opts.RunID] = st
	o.mu.Unlock()
	go o.run(context.Background(), st)
	return st, nil
}

// Runs lists all runs of this orchestrator.
func (o *Orchestrator) Runs() []RunStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]RunStatus, 0, len(o.runs))
	for _, r := range o.runs {
		out = append(out, *r)
	}
	return out
}

func (o *Orchestrator) setStep(st *RunStatus, step string) {
	o.mu.Lock()
	st.Step = step
	o.mu.Unlock()
}

func (o *Orchestrator) emit(st *RunStatus, e events.Event) {
	e.Source = events.SourceOrchestrator
	e.RunID = st.RunID
	e.Scenario = o.cfg.Scenario.Code
	if e.From == "" {
		e.From = o.cfg.Scenario.Orchestrator.ID
	}
	o.cfg.Emit.Emit(e)
}

func (o *Orchestrator) run(ctx context.Context, st *RunStatus) {
	s := o.cfg.Scenario
	start := time.Now()
	tracker := a2ax.NewTracker()
	o.emit(st, *(&events.Event{Kind: events.KindRunStarted, Note: fmt.Sprintf("%s starts: %s", s.Orchestrator.Name, s.Summary)}).WithValue(st.Options, o.cfg.PayloadLimit))

	var runErr error
	for _, group := range groups(s.Plan) {
		var wg sync.WaitGroup
		errs := make([]error, len(group))
		for i, step := range group {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = o.step(ctx, st, tracker, step)
			}()
			// Parallel steps start a moment apart so the arcs are distinguishable.
			if i < len(group)-1 {
				o.pause(ctx, st, 300*time.Millisecond)
			}
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			runErr = err
			if errors.Is(err, errAborted) {
				break
			}
		}
	}
	note := s.Closing
	if runErr != nil {
		note = "Run ended with problems: " + runErr.Error()
	}
	o.emit(st, events.Event{Kind: events.KindRunFinished, DurationMS: time.Since(start).Milliseconds(), Note: note})
	o.mu.Lock()
	st.Finished = time.Now().UTC()
	st.Step = ""
	if runErr != nil {
		st.Error = runErr.Error()
	}
	o.mu.Unlock()
}

// groups splits the plan into sequential groups of parallel steps.
func groups(plan []scenario.PlanStep) [][]scenario.PlanStep {
	var out [][]scenario.PlanStep
	for _, p := range plan {
		if p.Parallel && len(out) > 0 {
			out[len(out)-1] = append(out[len(out)-1], p)
			continue
		}
		out = append(out, []scenario.PlanStep{p})
	}
	return out
}

var errAborted = errors.New("aborted at breakpoint")

func (o *Orchestrator) pause(ctx context.Context, st *RunStatus, d time.Duration) {
	t := time.NewTimer(time.Duration(float64(d) / st.Speed))
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (o *Orchestrator) step(ctx context.Context, st *RunStatus, tracker *a2ax.Tracker, p scenario.PlanStep) error {
	s := o.cfg.Scenario
	agent := s.Agent(p.Agent)
	o.setStep(st, p.Step)
	if st.Breakpoints {
		if err := o.gate(ctx, st, p); err != nil {
			return err
		}
	}
	o.emit(st, events.Event{Kind: events.KindStepStarted, Step: p.Step, To: p.Agent, Method: string(p.Call), Note: p.Say})
	o.pause(ctx, st, 1500*time.Millisecond)

	tr := &a2ax.Transport{
		Base: o.cfg.HTTP, Emit: o.cfg.Emit, Tracker: tracker, PayloadLimit: o.cfg.PayloadLimit,
		Call: a2ax.Call{RunID: st.RunID, Scenario: s.Code, Step: p.Step, Caller: s.Orchestrator.ID, Agent: p.Agent,
			Chaos: st.Chaos && agent.Feature == scenario.FeatureStreaming, TraceID: st.TraceID},
	}
	hc := &http.Client{Transport: tr}
	start := time.Now()
	final, err := o.delegate(ctx, st, p, tr, hc)
	fin := events.Event{Kind: events.KindStepFinished, Step: p.Step, To: p.Agent, Method: string(p.Call), DurationMS: time.Since(start).Milliseconds()}
	denied := tr.LastStatus() == http.StatusForbidden
	switch {
	case p.Expect == "denied" && denied:
		fin.StateTo = "denied"
		fin.Note = fmt.Sprintf("As expected, agentgateway refused %q for %s (HTTP 403). The agent never saw the request.", p.Skill, agent.Name)
		err = nil
	case p.Expect == "denied":
		fin.StateTo = "unexpected"
		fin.Note = fmt.Sprintf("Expected the gateway to refuse %q, but it went through. Is the authorization policy loaded?", p.Skill)
	case err != nil:
		fin.StateTo = "error"
		fin.Note = fmt.Sprintf("Step %s failed: %v", p.Step, err)
	default:
		fin.StateTo = final
		fin.Note = fmt.Sprintf("%s finished %s: task %s", agent.Name, p.Step, final)
	}
	o.emit(st, fin)
	if err != nil {
		o.cfg.Log.Warn("step failed", "run", st.RunID, "step", p.Step, "err", err)
		return fmt.Errorf("%s: %w", p.Step, err)
	}
	return nil
}

func (o *Orchestrator) message(st *RunStatus, p scenario.PlanStep, text string) *a2a.Message {
	m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	m.Metadata = map[string]any{script.MetaRunID: st.RunID, script.MetaStep: p.Step, script.MetaSkill: p.Skill, script.MetaSpeed: st.Speed}
	return m
}

// delegate performs one plan step and returns the final short task state.
func (o *Orchestrator) delegate(ctx context.Context, st *RunStatus, p scenario.PlanStep, tr *a2ax.Transport, hc *http.Client) (string, error) {
	cardURL := strings.TrimRight(o.cfg.GatewayURL, "/") + "/agents/" + p.Agent + "/.well-known/agent-card.json"
	card, err := agentcard.NewResolver(hc).Resolve(ctx, cardURL)
	if err != nil {
		return "", fmt.Errorf("agent card: %w", err)
	}
	client, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		return "", fmt.Errorf("client: %w", err)
	}
	o.pause(ctx, st, 700*time.Millisecond)

	switch p.Call {
	case scenario.CallStream:
		return o.stream(ctx, st, p, client, o.message(st, p, p.Text))
	case scenario.CallPush:
		return o.push(ctx, st, p, tr, client)
	default:
		return o.send(ctx, st, p, client)
	}
}

func (o *Orchestrator) send(ctx context.Context, st *RunStatus, p scenario.PlanStep, client *a2aclient.Client) (string, error) {
	m := o.message(st, p, p.Text)
	for turn := 0; turn < 4; turn++ {
		res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: m})
		if err != nil {
			return "", err
		}
		task, ok := res.(*a2a.Task)
		if !ok {
			return "message", nil
		}
		if task.Status.State != a2a.TaskStateInputRequired {
			return events.ShortState(string(task.Status.State)), nil
		}
		m, err = o.reply(ctx, st, p, task.ID, task.ContextID, task.Status.Message)
		if err != nil {
			return "input-required", err
		}
	}
	return "", errors.New("too many turns")
}

func (o *Orchestrator) stream(ctx context.Context, st *RunStatus, p scenario.PlanStep, client *a2aclient.Client, m *a2a.Message) (string, error) {
	for turn := 0; turn < 4; turn++ {
		var (
			state             a2a.TaskState
			taskID            a2a.TaskID
			contextID         string
			question          *a2a.Message
			sawTask, sawFinal bool
		)
		for ev, err := range client.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: m}) {
			if err != nil {
				return "", err
			}
			switch e := ev.(type) {
			case *a2a.Task:
				sawTask, taskID, contextID, state, question = true, e.ID, e.ContextID, e.Status.State, e.Status.Message
			case *a2a.TaskStatusUpdateEvent:
				taskID, contextID, state, question = e.TaskID, e.ContextID, e.Status.State, e.Status.Message
			case *a2a.Message:
				return "message", nil
			}
			sawFinal = state.Terminal() || state == a2a.TaskStateInputRequired
		}
		_ = sawTask
		if !sawFinal && state == "" {
			return "", errors.New("stream ended without a task")
		}
		if state != a2a.TaskStateInputRequired {
			return events.ShortState(string(state)), nil
		}
		var err error
		if m, err = o.reply(ctx, st, p, taskID, contextID, question); err != nil {
			return "input-required", err
		}
	}
	return "", errors.New("too many turns")
}

// reply answers an input-required question with the plan's scripted reply.
func (o *Orchestrator) reply(ctx context.Context, st *RunStatus, p scenario.PlanStep, task a2a.TaskID, contextID string, question *a2a.Message) (*a2a.Message, error) {
	if p.Reply == "" {
		return nil, errors.New("agent asked for input but the plan has no reply")
	}
	q := ""
	if question != nil && len(question.Parts) > 0 {
		q = question.Parts[0].Text()
	}
	o.emit(st, events.Event{Kind: events.KindStepStarted, Step: p.Step, To: p.Agent, TaskID: string(task), ContextID: contextID,
		Note: fmt.Sprintf("%s asked %q. %s answers %q in the same task.", p.Agent, q, o.cfg.Scenario.Orchestrator.Name, p.Reply)})
	o.pause(ctx, st, 2*time.Second)
	m := o.message(st, p, p.Reply)
	m.TaskID = task
	m.ContextID = contextID
	return m, nil
}

func (o *Orchestrator) push(ctx context.Context, st *RunStatus, p scenario.PlanStep, tr *a2ax.Transport, client *a2aclient.Client) (string, error) {
	token := a2ax.NewID() + a2ax.NewID()
	w := &pushWaiter{transport: tr, ch: make(chan a2ax.TaskInfo, 64)}
	o.mu.Lock()
	o.waiters[token] = w
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.waiters, token)
		o.mu.Unlock()
	}()

	hook := strings.TrimRight(o.cfg.PublicURL, "/") + "/push/" + url.PathEscape(token)
	res, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: o.message(st, p, p.Text),
		Config:  &a2a.SendMessageConfig{ReturnImmediately: true, PushConfig: &a2a.PushConfig{URL: hook, Token: token}},
	})
	if err != nil {
		return "", err
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		return "message", nil
	}
	if task.Status.State.Terminal() {
		return events.ShortState(string(task.Status.State)), nil
	}
	o.emit(st, events.Event{Kind: events.KindStepStarted, Step: p.Step, To: p.Agent, TaskID: string(task.ID),
		Note: fmt.Sprintf("%s returned at once (task %s). %s waits for push notifications at its webhook.", p.Agent, events.ShortState(string(task.Status.State)), o.cfg.Scenario.Orchestrator.Name)})

	timeout := time.NewTimer(o.cfg.PushTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("no final push notification")
		case info := <-w.ch:
			if info.State != "completed" && info.State != "failed" && info.State != "canceled" && info.State != "rejected" {
				continue
			}
			// The push said "done"; fetch the full task (with artifacts) with GetTask.
			o.pause(ctx, st, 800*time.Millisecond)
			final, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: task.ID})
			if err != nil {
				return info.State, err
			}
			return events.ShortState(string(final.Status.State)), nil
		}
	}
}

// Handler serves POST /run, GET /runs, POST /push/{token} and GET /healthz.
func (o *Orchestrator) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "orchestrator": o.cfg.Scenario.Orchestrator.ID})
	})
	mux.HandleFunc("POST /run", func(w http.ResponseWriter, r *http.Request) {
		var opts Options
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&opts); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		st, err := o.Start(opts)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, st)
	})
	mux.HandleFunc("GET /runs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, o.Runs())
	})
	mux.HandleFunc("POST /push/{token}", func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		o.mu.Lock()
		waiter := o.waiters[token]
		o.mu.Unlock()
		if waiter == nil || r.Header.Get("A2A-Notification-Token") != token {
			http.Error(w, "unknown push token", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		info := waiter.transport.ReportPush(body)
		select {
		case waiter.ch <- info:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// gate asks the visualizer whether to continue (breakpoint mode). It blocks
// until a person releases the gate in the UI.
func (o *Orchestrator) gate(ctx context.Context, st *RunStatus, p scenario.PlanStep) error {
	if o.cfg.VisualizerURL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{
		"run_id": st.RunID, "scenario": o.cfg.Scenario.Code, "step": p.Step, "from": o.cfg.Scenario.Orchestrator.ID, "to": p.Agent, "note": p.Say,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.cfg.VisualizerURL, "/")+"/api/gate/wait", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		o.cfg.Log.Warn("gate unavailable, continuing", "err", err)
		return nil
	}
	defer resp.Body.Close()
	var out struct {
		Action string `json:"action"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Action == "abort" {
		return errAborted
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
