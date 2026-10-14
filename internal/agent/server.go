// Package agent assembles one A2A worker agent: its card (from the scenario),
// the a2a-go JSON-RPC handler running the scripted executor, push
// notifications, and two HTTP middlewares that make the protocol visible
// (request capture) and demonstrate gateway retries (chaos).
package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/push"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
	"github.com/tranmh/agent-atlas/internal/scenario"
	"github.com/tranmh/agent-atlas/internal/script"
)

// Paths served by every agent.
const (
	InvokePath = "/invoke"
	HealthPath = "/healthz"
)

// Config configures one agent.
type Config struct {
	Scenario     *scenario.Scenario
	Agent        *scenario.Agent
	PublicURL    string // base URL the agent advertises, e.g. http://sc-diagnosis:8080
	Emit         events.Emitter
	LLM          script.Chatter
	DefaultSpeed float64
	PayloadLimit int
	Log          *slog.Logger
	// PushAllowPrivate lets push notifications reach private addresses (compose
	// networks, httptest). a2a-go blocks them by default (SSRF guard).
	PushAllowPrivate bool
}

// Card builds the agent card from the scenario.
func Card(cfg Config) *a2a.AgentCard {
	a := cfg.Agent
	skills := make([]a2a.AgentSkill, 0, len(a.Skills))
	for _, s := range a.Skills {
		skills = append(skills, a2a.AgentSkill{ID: s.ID, Name: s.Name, Description: s.Description, Tags: append([]string{}, s.Tags...)})
	}
	return &a2a.AgentCard{
		Name:        a.Name,
		Description: a.Description + " (" + a.City + ", " + a.Country + ")",
		Version:     "1.0.0",
		Provider:    &a2a.AgentProvider{Org: "agent-atlas · " + cfg.Scenario.Title, URL: "https://github.com/tranmh/agent-atlas"},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(strings.TrimRight(cfg.PublicURL, "/")+InvokePath, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text", "application/json"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true, PushNotifications: true},
		Skills:             skills,
	}
}

// NewHandler returns the agent's HTTP handler.
func NewHandler(cfg Config) http.Handler {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Emit == nil {
		cfg.Emit = &events.Recorder{}
	}
	runner := &script.Runner{
		Scenario:     cfg.Scenario,
		Agent:        cfg.Agent,
		Emit:         cfg.Emit,
		LLM:          cfg.LLM,
		DefaultSpeed: cfg.DefaultSpeed,
		PayloadLimit: cfg.PayloadLimit,
	}
	card := Card(cfg)
	sender := push.NewHTTPPushSender(&push.HTTPSenderConfig{Timeout: 10 * time.Second, AllowPrivateNetworks: cfg.PushAllowPrivate})
	h := a2asrv.NewHandler(runner.Executor(),
		a2asrv.WithPushNotifications(push.NewInMemoryStore(), sender),
		a2asrv.WithCapabilityChecks(&card.Capabilities),
		a2asrv.WithLogger(cfg.Log),
	)

	var invoke http.Handler = a2asrv.NewJSONRPCHandler(h)
	if cfg.Agent.Feature == scenario.FeatureStreaming {
		invoke = chaos(cfg, invoke)
	}
	invoke = capture(cfg, invoke)

	mux := http.NewServeMux()
	mux.Handle(InvokePath, invoke)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.HandleFunc(HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "agent": cfg.Agent.ID})
	})
	return mux
}

// rpcPeek is the part of a JSON-RPC request the middlewares need.
type rpcPeek struct {
	Method string `json:"method"`
	Params struct {
		Message struct {
			ID       string         `json:"messageId"`
			TaskID   string         `json:"taskId"`
			Metadata map[string]any `json:"metadata"`
		} `json:"message"`
		ID string `json:"id"` // GetTask / CancelTask
	} `json:"params"`
}

func readBody(r *http.Request) ([]byte, rpcPeek) {
	var peek rpcPeek
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	_ = json.Unmarshal(body, &peek)
	return body, peek
}

// capturedHeaders are the request headers shown in the payload panel: they
// show what the gateway added or kept.
var capturedHeaders = []string{"Host", "X-Run-Id", "X-Caller", "X-Chaos", "X-Forwarded-For", "X-Forwarded-Proto", "Traceparent", "Content-Type", "Accept", "A2a-Version"}

// capture reports every JSON-RPC request as it arrives behind the gateway.
func capture(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		body, peek := readBody(r)
		headers := map[string]string{}
		for _, h := range capturedHeaders {
			if v := r.Header.Get(h); v != "" {
				headers[h] = v
			}
		}
		if r.Host != "" {
			headers["Host"] = r.Host
		}
		step, _ := peek.Params.Message.Metadata[script.MetaStep].(string)
		runID := r.Header.Get(script.HeaderRunID)
		taskID := peek.Params.Message.TaskID
		if taskID == "" {
			taskID = peek.Params.ID
		}
		e := events.Event{
			Source: events.SourceAgent, Kind: events.KindAgentRequest, Scenario: cfg.Scenario.Code,
			RunID: runID, Step: step, From: r.Header.Get(script.HeaderCaller), To: cfg.Agent.ID, Via: events.ViaGateway,
			Method: peek.Method, TaskID: taskID, Note: cfg.Agent.Name + " received " + peek.Method,
		}
		var parsed any
		if json.Unmarshal(body, &parsed) != nil {
			parsed = string(body)
		}
		e.WithValue(map[string]any{"headers": headers, "path": r.URL.Path, "body": parsed}, cfg.PayloadLimit)
		cfg.Emit.Emit(e)
		next.ServeHTTP(w, r)
	})
}

// chaos answers 503 to the first attempt of a message when the request
// carries X-Chaos: 1. The gateway's retry policy then resends it, so the
// retry is real and deterministic (no random()).
func chaos(cfg Config, next http.Handler) http.Handler {
	var mu sync.Mutex
	seen := map[string]bool{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get(script.HeaderChaos) != "1" {
			next.ServeHTTP(w, r)
			return
		}
		_, peek := readBody(r)
		key := r.Header.Get(script.HeaderRunID) + "/" + peek.Params.Message.ID
		mu.Lock()
		first := peek.Params.Message.ID != "" && !seen[key]
		seen[key] = true
		if len(seen) > 10000 {
			seen = map[string]bool{key: true}
		}
		mu.Unlock()
		if !first {
			next.ServeHTTP(w, r)
			return
		}
		step, _ := peek.Params.Message.Metadata[script.MetaStep].(string)
		cfg.Emit.Emit(events.Event{
			Source: events.SourceAgent, Kind: events.KindFaultInjected, Scenario: cfg.Scenario.Code,
			RunID: r.Header.Get(script.HeaderRunID), Step: step, From: cfg.Agent.ID, To: r.Header.Get(script.HeaderCaller),
			Via: events.ViaGateway, Method: peek.Method, HTTPStatus: http.StatusServiceUnavailable,
			Note: "Chaos: " + cfg.Agent.Name + " answered 503 to the first attempt. The gateway's retry policy will resend the request.",
		})
		http.Error(w, "chaos: injected fault (first attempt)", http.StatusServiceUnavailable)
	})
}
