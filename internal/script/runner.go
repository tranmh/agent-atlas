// Package script executes an agent's scenario script as an A2A executor: each
// step becomes A2A events (status updates, artifacts, input-required) and an
// event for the visualizer. Waits are divided by a speed factor so the same
// script runs slowly for people and fast in tests.
package script

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/llm"
	"github.com/tranmh/agent-atlas/internal/scenario"
)

// Metadata keys the orchestrator puts on every message.
const (
	MetaRunID = "run_id"
	MetaStep  = "step"
	MetaSkill = "skill"
	MetaSpeed = "speed"
)

// Headers the orchestrator sets on every request (the gateway logs them too).
const (
	HeaderRunID  = "X-Run-Id"
	HeaderCaller = "X-Caller"
	HeaderChaos  = "X-Chaos"
)

// Chatter is the LLM client interface (llm.Client implements it).
type Chatter interface {
	Chat(ctx context.Context, system, prompt string, maxTokens int, headers map[string]string) (*llm.Result, error)
}

// Runner executes one agent's script.
type Runner struct {
	Scenario     *scenario.Scenario
	Agent        *scenario.Agent
	Emit         events.Emitter
	LLM          Chatter // nil: llm steps use their fallback text
	DefaultSpeed float64 // used when the message carries no speed; <= 0 means 1
	PayloadLimit int
	// Sleep waits d or until ctx ends; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Executor adapts the runner to a2a-go.
func (r *Runner) Executor() a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(r.Execute)
}

// call is the per-request context of one execution.
type call struct {
	runID, caller, step string
	speed               float64
	input, request      string
}

// Execute runs the script from the start, or resumes after the input-required
// step the incoming message answers.
func (r *Runner) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		c := r.callInfo(ec)
		start := 0
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
			r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "submitted", Note: r.Agent.Name + " accepted the task"})
		} else {
			start = r.resumeIndex(ec)
		}
		for i := start; i < len(r.Agent.Script); i++ {
			st := r.Agent.Script[i]
			switch st.Kind() {
			case "say":
				text := expand(st.Say, c)
				r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "working", Note: text})
				if !yield(status(ec, a2a.TaskStateWorking, text), nil) {
					return
				}
			case "input_required":
				text := expand(st.InputRequired, c)
				r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "input-required", Note: text})
				yield(status(ec, a2a.TaskStateInputRequired, text), nil)
				return
			case "artifact":
				ev := artifactEvent(ec, st.Artifact.Name, expand(st.Artifact.Text, c), st.Artifact.Data, "scripted")
				r.emit(ec, c, *(&events.Event{Kind: events.KindArtifact, Note: "Artifact " + st.Artifact.Name}).WithValue(ev.Artifact, r.PayloadLimit))
				if !yield(ev, nil) {
					return
				}
			case "llm":
				text, source := r.llmStep(ctx, ec, c, st)
				ev := artifactEvent(ec, "answer", text, nil, source)
				r.emit(ec, c, *(&events.Event{Kind: events.KindArtifact, Note: "Artifact answer (" + source + ")"}).WithValue(ev.Artifact, r.PayloadLimit))
				if !yield(ev, nil) {
					return
				}
			case "complete":
				text := expand(st.Complete, c)
				r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "completed", Note: text})
				yield(status(ec, a2a.TaskStateCompleted, text), nil)
				return
			case "fail":
				text := expand(st.Fail, c)
				r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "failed", Note: text})
				yield(status(ec, a2a.TaskStateFailed, text), nil)
				return
			}
			if w := st.Wait.Std(); w > 0 {
				if err := r.sleep(ctx, time.Duration(float64(w)/c.speed)); err != nil {
					r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "canceled", Note: "Stopped: " + err.Error()})
					return
				}
			}
		}
		// Validation guarantees a terminal step; this is a safety net.
		yield(status(ec, a2a.TaskStateCompleted, "Done"), nil)
	}
}

// resumeIndex returns the step after the input-required step that the
// incoming message answers. Every earlier user message in the history (the
// original request plus earlier answers) accounts for one answered question.
func (r *Runner) resumeIndex(ec *a2asrv.ExecutorContext) int {
	answered := 0
	for _, m := range ec.StoredTask.History {
		if m.Role == a2a.MessageRoleUser && (ec.Message == nil || m.ID != ec.Message.ID) {
			answered++
		}
	}
	seen := 0
	for i, st := range r.Agent.Script {
		if st.Kind() == "input_required" {
			seen++
			if seen == answered {
				return i + 1
			}
		}
	}
	return 0
}

func (r *Runner) llmStep(ctx context.Context, ec *a2asrv.ExecutorContext, c call, st scenario.Step) (string, string) {
	fallback := expand(st.Fallback, c)
	if r.Agent.Mode != scenario.ModeLLM {
		return fallback, "scripted"
	}
	if r.LLM == nil {
		r.emit(ec, c, events.Event{Kind: events.KindLLMUnavailable, To: "llm", Note: "LLM disabled, using the scripted answer"})
		return fallback, "fallback"
	}
	prompt := expand(st.LLMPrompt, c)
	r.emit(ec, c, *(&events.Event{Kind: events.KindLLMRequest, To: "llm", Via: events.ViaGateway, Method: "POST /v1/chat/completions",
		Note: "Asking the model through the gateway's LLM route"}).WithValue(map[string]any{"system": r.Agent.LLM.System, "prompt": prompt}, r.PayloadLimit))
	headers := map[string]string{HeaderRunID: c.runID, HeaderCaller: r.Agent.ID}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	res, err := r.LLM.Chat(cctx, r.Agent.LLM.System, prompt, r.Agent.LLM.MaxTokens, headers)
	if err != nil {
		e := events.Event{Kind: events.KindLLMUnavailable, To: "llm", Note: "Model unavailable (" + err.Error() + "), using the scripted answer"}
		if res != nil {
			e.DurationMS = res.Duration.Milliseconds()
		}
		r.emit(ec, c, e)
		return fallback, "fallback"
	}
	ev := events.Event{Kind: events.KindLLMResponse, From: "llm", To: r.Agent.ID, Via: events.ViaGateway, DurationMS: res.Duration.Milliseconds(),
		Note: fmt.Sprintf("%s answered in %.1f s (%d tokens)", res.Model, res.Duration.Seconds(), res.Usage.CompletionTokens)}
	r.emit(ec, c, *ev.WithPayload(res.RespBody, r.PayloadLimit))
	return res.Text, "llm"
}

func (r *Runner) callInfo(ec *a2asrv.ExecutorContext) call {
	c := call{speed: r.DefaultSpeed}
	if c.speed <= 0 {
		c.speed = 1
	}
	if v, ok := ec.ServiceParams.Get(HeaderRunID); ok && len(v) > 0 {
		c.runID = v[0]
	}
	if v, ok := ec.ServiceParams.Get(HeaderCaller); ok && len(v) > 0 {
		c.caller = v[0]
	}
	if ec.Message != nil {
		md := ec.Message.Metadata
		if s, ok := md[MetaRunID].(string); ok && c.runID == "" {
			c.runID = s
		}
		if s, ok := md[MetaStep].(string); ok {
			c.step = s
		}
		if sp := toFloat(md[MetaSpeed]); sp > 0 {
			c.speed = sp
		}
		c.input = messageText(ec.Message)
	}
	c.request = c.input
	if ec.StoredTask != nil {
		for _, m := range ec.StoredTask.History {
			if m.Role == a2a.MessageRoleUser {
				c.request = messageText(m)
				break
			}
		}
	}
	return c
}

func (r *Runner) emit(ec *a2asrv.ExecutorContext, c call, e events.Event) {
	if r.Emit == nil {
		return
	}
	e.Source = events.SourceAgent
	e.Scenario = r.Scenario.Code
	e.RunID = c.runID
	e.Step = c.step
	if e.From == "" {
		e.From = r.Agent.ID
	}
	if e.To == "" {
		e.To = c.caller
	}
	e.TaskID = string(ec.TaskID)
	e.ContextID = ec.ContextID
	r.Emit.Emit(e)
}

func (r *Runner) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
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

func status(ec *a2asrv.ExecutorContext, state a2a.TaskState, text string) *a2a.TaskStatusUpdateEvent {
	return a2a.NewStatusUpdateEvent(ec, state, a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(text)))
}

func artifactEvent(ec *a2asrv.ExecutorContext, name, text string, data map[string]any, source string) *a2a.TaskArtifactUpdateEvent {
	var parts []*a2a.Part
	if text != "" {
		parts = append(parts, a2a.NewTextPart(text))
	}
	if len(data) > 0 {
		parts = append(parts, a2a.NewDataPart(data))
	}
	ev := a2a.NewArtifactEvent(ec, parts...)
	ev.Artifact.Name = name
	ev.Artifact.Metadata = map[string]any{"source": source}
	ev.LastChunk = true
	return ev
}

func messageText(m *a2a.Message) string {
	var b strings.Builder
	for _, p := range m.Parts {
		if t := p.Text(); t != "" {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(t)
		}
	}
	return b.String()
}

func expand(tmpl string, c call) string {
	return strings.NewReplacer("{{input}}", c.input, "{{request}}", c.request).Replace(tmpl)
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
