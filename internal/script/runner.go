// Package script executes an agent's scenario script as an A2A executor: each
// step becomes A2A events (status updates, artifacts, input-required) and an
// event for the visualizer. Waits are divided by a speed factor so the same
// script runs slowly for people and fast in tests.
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/llm"
	"github.com/tranmh/agent-atlas/internal/mcpx"
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
	ChatMessages(ctx context.Context, msgs []llm.Message, tools []llm.Tool, maxTokens int, headers map[string]string) (*llm.Result, error)
}

// Runner executes one agent's script.
type Runner struct {
	Scenario     *scenario.Scenario
	Agent        *scenario.Agent
	Emit         events.Emitter
	LLM          Chatter      // nil: llm steps use their fallback text
	MCP          *mcpx.Client // nil: MCP steps report "MCP unavailable" and continue
	DefaultSpeed float64      // used when the message carries no speed; <= 0 means 1
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
	traceparent         string
	speed               float64
	input, request      string
}

// Execute runs the script from the start, or resumes after the input-required
// step the incoming message answers.
func (r *Runner) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		c := r.callInfo(ec)
		mc := &mcpConn{r: r, ec: ec, c: c}
		defer mc.close()
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
				text, source, ok := r.llmStep(ctx, ec, c, mc, st, yield)
				if !ok {
					return
				}
				ev := artifactEvent(ec, "answer", text, nil, source)
				r.emit(ec, c, *(&events.Event{Kind: events.KindArtifact, Note: "Artifact answer (" + source + ")"}).WithValue(ev.Artifact, r.PayloadLimit))
				if !yield(ev, nil) {
					return
				}
			case "tool":
				if !r.toolStep(ctx, ec, c, mc, st.Tool, yield) {
					return
				}
			case "resource":
				if !r.resourceStep(ctx, ec, c, mc, st.Resource, yield) {
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

// Conversation turns the agent adds to steer a small model.
const (
	answerNow            = "Using these results, answer now in at most three short sentences."
	nudge                = "Call the tool now. Do not answer yet."
	maxToolCallsPerRound = 2
)

// llmStep produces the agent's answer with the model. With prompt_from the
// system prompt comes from an MCP server; with tools the model may call the
// listed MCP servers' tools (as the gateway lists them) for up to two rounds.
// Any failure falls back to the scripted text. ok=false: the A2A caller is gone.
func (r *Runner) llmStep(ctx context.Context, ec *a2asrv.ExecutorContext, c call, mc *mcpConn, st scenario.Step, yield func(a2a.Event, error) bool) (string, string, bool) {
	fallback := expand(st.Fallback, c)
	if r.Agent.Mode != scenario.ModeLLM {
		return fallback, "scripted", true
	}
	if r.LLM == nil {
		r.emit(ec, c, events.Event{Kind: events.KindLLMUnavailable, To: "llm", Note: "LLM disabled, using the scripted answer"})
		return fallback, "fallback", true
	}
	system := r.Agent.LLM.System
	var tools []llm.Tool
	allowed := map[string]bool{}
	if st.PromptFrom != nil || len(st.Tools) > 0 {
		sess, err := mc.session(ctx)
		if err != nil {
			if !r.work(ec, c, "MCP unavailable ("+err.Error()+"): the model answers without tools", yield) {
				return "", "", false
			}
		} else {
			if pf := st.PromptFrom; pf != nil {
				name := mcpx.ToolName(r.Scenario.MCPServer(pf.Server), pf.Name)
				if !r.work(ec, c, fmt.Sprintf("Fetching the system prompt %s over MCP…", name), yield) {
					return "", "", false
				}
				if text, err := sess.GetPrompt(ctx, name); err == nil && strings.TrimSpace(text) != "" {
					system = text
				}
			}
			if len(st.Tools) > 0 {
				list, err := sess.Tools(ctx)
				if err == nil {
					for _, t := range list {
						for _, id := range st.Tools {
							if strings.HasPrefix(t.Name, r.Scenario.MCPServer(id).Target()+"_") {
								tools = append(tools, llm.Tool{Type: "function", Function: llm.FunctionDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
								allowed[t.Name] = true
							}
						}
					}
				}
				names := make([]string, 0, len(tools))
				for _, t := range tools {
					names = append(names, t.Function.Name)
				}
				if !r.work(ec, c, fmt.Sprintf("The model may call %d MCP tools: %s", len(tools), strings.Join(names, ", ")), yield) {
					return "", "", false
				}
			}
		}
	}

	headers := map[string]string{HeaderRunID: c.runID, HeaderCaller: r.Agent.ID}
	if c.traceparent != "" {
		headers["Traceparent"] = c.traceparent // the LLM call joins the run's trace
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	msgs := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: expand(st.LLMPrompt, c)}}
	const maxRounds = 3 // up to two rounds of tool calls, then an answer
	usedTool, nudged := false, false
	for round := 1; round <= maxRounds; round++ {
		offered := tools
		if round == maxRounds {
			offered = nil // the last round must answer
		}
		toolNames := make([]string, 0, len(offered))
		for _, t := range offered {
			toolNames = append(toolNames, t.Function.Name)
		}
		note := "Asking the model through the gateway's LLM route"
		if len(offered) > 0 {
			note = fmt.Sprintf("Asking the model (round %d) with %d MCP tools it may call", round, len(offered))
		}
		r.emit(ec, c, *(&events.Event{Kind: events.KindLLMRequest, To: "llm", Via: events.ViaGateway, Method: "POST /v1/chat/completions", Note: note}).
			WithValue(map[string]any{"round": round, "tools": toolNames, "messages": msgs}, r.PayloadLimit))
		res, err := r.LLM.ChatMessages(cctx, msgs, offered, r.Agent.LLM.MaxTokens, headers)
		if err != nil {
			e := events.Event{Kind: events.KindLLMUnavailable, To: "llm", Note: "Model unavailable (" + err.Error() + "), using the scripted answer"}
			if res != nil {
				e.DurationMS = res.Duration.Milliseconds()
			}
			r.emit(ec, c, e)
			return fallback, "fallback", true
		}
		answered := fmt.Sprintf("%s answered in %.1f s (%d tokens)", res.Model, res.Duration.Seconds(), res.Usage.CompletionTokens)
		if len(res.ToolCalls) > 0 {
			answered = fmt.Sprintf("%s asked for %d tool call(s) in %.1f s", res.Model, len(res.ToolCalls), res.Duration.Seconds())
		}
		ev := events.Event{Kind: events.KindLLMResponse, From: "llm", To: r.Agent.ID, Via: events.ViaGateway, DurationMS: res.Duration.Milliseconds(), Note: answered}
		r.emit(ec, c, *ev.WithPayload(res.RespBody, r.PayloadLimit))
		if len(res.ToolCalls) == 0 {
			if len(offered) > 0 && !usedTool && !nudged && round < maxRounds {
				// Small models often answer from memory. Insist once on data first.
				nudged = true
				if !r.work(ec, c, "The model answered without data; asking it to call a tool first.", yield) {
					return "", "", false
				}
				msgs = append(msgs, llm.Message{Role: "assistant", Content: res.Text}, llm.Message{Role: "user", Content: nudge})
				continue
			}
			return res.Text, "llm", true
		}
		usedTool = true
		msgs = append(msgs, res.Message)
		for i, tc := range res.ToolCalls {
			name := tc.Function.Name
			args := map[string]any{}
			argErr := json.Unmarshal([]byte(tc.Function.Arguments), &args)
			choice := events.Event{Kind: events.KindLLMToolChoice, From: "llm", To: r.Agent.ID, Method: name,
				Note: fmt.Sprintf("The model chose the MCP tool %s(%s)", name, tc.Function.Arguments)}
			r.emit(ec, c, *choice.WithValue(map[string]any{"name": name, "arguments": args, "raw_arguments": tc.Function.Arguments}, r.PayloadLimit))
			var content string
			switch {
			case i >= maxToolCallsPerRound:
				content = "skipped: at most two tool calls per round"
			case !allowed[name]:
				content = "error: unknown tool " + name
			case argErr != nil:
				content = "error: arguments must be a JSON object"
			default:
				if !r.work(ec, c, fmt.Sprintf("The model chose %s(%s)", name, tc.Function.Arguments), yield) {
					return "", "", false
				}
				sess, _ := mc.session(ctx)
				text, err, ok := r.runTool(cctx, ec, c, sess, name, args, yield)
				if !ok {
					return "", "", false
				}
				if err != nil {
					content = "error: " + err.Error()
				} else {
					content = text
					if !r.work(ec, c, fmt.Sprintf("%s → %s", name, text), yield) {
						return "", "", false
					}
				}
			}
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: content})
		}
		// Small models stop calling tools when the system prompt also limits the
		// answer, so the length limit comes only once the tool results are in.
		msgs = append(msgs, llm.Message{Role: "user", Content: answerNow})
	}
	return fallback, "fallback", true
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
	if v, ok := ec.ServiceParams.Get("traceparent"); ok && len(v) > 0 {
		c.traceparent = v[0]
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

// mcpConn opens the agent's MCP session on first use and closes it at the end
// of the execution.
type mcpConn struct {
	r    *Runner
	ec   *a2asrv.ExecutorContext
	c    call
	sess *mcpx.Session
	err  error
}

func (m *mcpConn) session(ctx context.Context) (*mcpx.Session, error) {
	if m.sess != nil || m.err != nil {
		return m.sess, m.err
	}
	if m.r.MCP == nil {
		m.err = errors.New("MCP disabled")
		return nil, m.err
	}
	m.sess, m.err = m.r.MCP.Open(ctx, mcpx.Call{
		Scenario: m.r.Scenario, Caller: m.r.Agent.ID, RunID: m.c.runID, Step: m.c.step,
		TaskID: string(m.ec.TaskID), Speed: m.c.speed, Traceparent: m.c.traceparent,
	})
	return m.sess, m.err
}

func (m *mcpConn) close() {
	if m.sess != nil {
		_ = m.sess.Close()
	}
}

// work emits an agent_work note and yields it as a working status update.
func (r *Runner) work(ec *a2asrv.ExecutorContext, c call, text string, yield func(a2a.Event, error) bool) bool {
	r.emit(ec, c, events.Event{Kind: events.KindAgentWork, StateTo: "working", Note: text})
	return yield(status(ec, a2a.TaskStateWorking, text), nil)
}

// toolStep calls an MCP tool through the gateway (a scripted step).
func (r *Runner) toolStep(ctx context.Context, ec *a2asrv.ExecutorContext, c call, mc *mcpConn, t *scenario.ToolCall, yield func(a2a.Event, error) bool) bool {
	srv := r.Scenario.MCPServer(t.Server)
	name := mcpx.ToolName(srv, t.Name)
	if !r.work(ec, c, fmt.Sprintf("Calling MCP tool %s on %s…", name, srv.Name), yield) {
		return false
	}
	sess, err := mc.session(ctx)
	if err != nil {
		return r.work(ec, c, "MCP unavailable ("+err.Error()+"), continuing without "+name, yield)
	}
	args := map[string]any{}
	for k, v := range t.Args {
		if str, ok := v.(string); ok {
			v = expand(str, c)
		}
		args[k] = v
	}
	text, err, ok := r.runTool(ctx, ec, c, sess, name, args, yield)
	if !ok {
		return false
	}
	switch {
	case errors.Is(err, mcpx.ErrDenied) && t.Expect == "denied":
		return r.work(ec, c, fmt.Sprintf("As expected, agentgateway refused the write tool %s. A human has to do this.", name), yield)
	case errors.Is(err, mcpx.ErrDenied):
		return r.work(ec, c, fmt.Sprintf("agentgateway refused %s.", name), yield)
	case err != nil:
		return r.work(ec, c, fmt.Sprintf("MCP tool %s failed: %v", name, err), yield)
	case t.Expect == "denied":
		return r.work(ec, c, fmt.Sprintf("Unexpected: the write tool %s went through. Is the MCP policy loaded?", name), yield)
	default:
		return r.work(ec, c, fmt.Sprintf("%s → %s", name, text), yield)
	}
}

// runTool calls a tool and turns its progress notifications into A2A status
// updates, so the A2A caller sees them too. ok=false: the A2A caller is gone.
func (r *Runner) runTool(ctx context.Context, ec *a2asrv.ExecutorContext, c call, sess *mcpx.Session, name string, args map[string]any, yield func(a2a.Event, error) bool) (string, error, bool) {
	if sess == nil {
		return "", errors.New("no MCP session"), true
	}
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		res, err := sess.CallTool(ctx, name, args)
		if err != nil {
			done <- result{err: err}
			return
		}
		if res.IsError {
			done <- result{err: errors.New(mcpx.ResultText(res))}
			return
		}
		done <- result{text: mcpx.ResultText(res)}
	}()
	for {
		select {
		case p := <-sess.Progress():
			if !r.work(ec, c, fmt.Sprintf("%s: %s (%g/%g)", name, p.Message, p.Progress, p.Total), yield) {
				return "", nil, false
			}
		case res := <-done:
			return res.text, res.err, true
		}
	}
}

// resourceStep reads an MCP resource through the gateway.
func (r *Runner) resourceStep(ctx context.Context, ec *a2asrv.ExecutorContext, c call, mc *mcpConn, rr *scenario.ResourceRead, yield func(a2a.Event, error) bool) bool {
	srv := r.Scenario.MCPServer(rr.Server)
	uri := mcpx.ResourceURI(srv, rr.URI)
	title := rr.URI
	for _, res := range srv.Resources {
		if res.URI == rr.URI {
			title = res.Name
		}
	}
	if !r.work(ec, c, fmt.Sprintf("Reading MCP resource %q from %s…", title, srv.Name), yield) {
		return false
	}
	sess, err := mc.session(ctx)
	if err != nil {
		return r.work(ec, c, "MCP unavailable ("+err.Error()+"), continuing without "+title, yield)
	}
	text, err := sess.ReadResource(ctx, uri)
	if err != nil {
		return r.work(ec, c, fmt.Sprintf("Reading %s failed: %v", uri, err), yield)
	}
	return r.work(ec, c, fmt.Sprintf("Read %q: %s", title, firstLines(text, 2)), yield)
}

// firstLines returns up to n non-heading lines of a text.
func firstLines(text string, n int) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
		if len(out) == n {
			break
		}
	}
	return strings.Join(out, " ")
}
